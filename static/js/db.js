/*
 * Vartalap local storage. IndexedDB is authoritative for message history:
 * the server deletes ciphertext as soon as every recipient device acks it.
 * Private keys are stored as non-extractable CryptoKey objects.
 */
(function (global) {
  'use strict';

  var DB_NAME = 'vartalap';
  var DB_VERSION = 1;
  var dbPromise = null;

  function open() {
    if (dbPromise) {
      return dbPromise;
    }
    dbPromise = new Promise(function (resolve, reject) {
      var request = global.indexedDB.open(DB_NAME, DB_VERSION);
      request.onupgradeneeded = function (event) {
        var db = event.target.result;
        if (!db.objectStoreNames.contains('identity')) {
          db.createObjectStore('identity', { keyPath: 'id' });
        }
        if (!db.objectStoreNames.contains('contacts')) {
          db.createObjectStore('contacts', { keyPath: 'userId' });
        }
        if (!db.objectStoreNames.contains('conversations')) {
          db.createObjectStore('conversations', { keyPath: 'id' });
        }
        if (!db.objectStoreNames.contains('messages')) {
          var messages = db.createObjectStore('messages', { keyPath: 'id' });
          messages.createIndex('conversation', 'conversationId', { unique: false });
          messages.createIndex('sortAt', 'sortAt', { unique: false });
        }
        if (!db.objectStoreNames.contains('outbox')) {
          db.createObjectStore('outbox', { keyPath: 'id' });
        }
        if (!db.objectStoreNames.contains('settings')) {
          db.createObjectStore('settings', { keyPath: 'key' });
        }
      };
      request.onsuccess = function () {
        resolve(request.result);
      };
      request.onerror = function () {
        dbPromise = null;
        reject(request.error || new Error('Could not open local storage.'));
      };
      request.onblocked = function () {
        reject(new Error('Local storage is blocked by another tab. Close other Vartalap tabs and try again.'));
      };
    });
    return dbPromise;
  }

  function requestResult(request) {
    return new Promise(function (resolve, reject) {
      request.onsuccess = function () { resolve(request.result); };
      request.onerror = function () { reject(request.error); };
    });
  }

  async function withStore(name, mode, operation) {
    var db = await open();
    return new Promise(function (resolve, reject) {
      var transaction = db.transaction(name, mode);
      var store = transaction.objectStore(name);
      var result;
      try {
        result = operation(store);
      } catch (error) {
        reject(error);
        return;
      }
      transaction.oncomplete = function () { resolve(result); };
      transaction.onerror = function () { reject(transaction.error); };
      transaction.onabort = function () { reject(transaction.error || new Error('Local transaction aborted.')); };
    });
  }

  async function get(storeName, key) {
    var db = await open();
    return requestResult(db.transaction(storeName, 'readonly').objectStore(storeName).get(key));
  }

  async function getAll(storeName) {
    var db = await open();
    return requestResult(db.transaction(storeName, 'readonly').objectStore(storeName).getAll());
  }

  async function put(storeName, value) {
    await withStore(storeName, 'readwrite', function (store) {
      store.put(value);
    });
    return value;
  }

  async function remove(storeName, key) {
    await withStore(storeName, 'readwrite', function (store) {
      store.delete(key);
    });
  }

  async function clearStores(names) {
    var db = await open();
    return new Promise(function (resolve, reject) {
      var transaction = db.transaction(names, 'readwrite');
      names.forEach(function (name) { transaction.objectStore(name).clear(); });
      transaction.oncomplete = function () { resolve(); };
      transaction.onerror = function () { reject(transaction.error); };
      transaction.onabort = function () { reject(transaction.error || new Error('Local transaction aborted.')); };
    });
  }

  var api = {
    open: open,
    getIdentity: function () { return get('identity', 'self'); },
    saveIdentity: function (identity) {
      identity.id = 'self';
      return put('identity', identity);
    },
    getContact: function (userId) { return get('contacts', userId); },
    saveContact: function (contact) { return put('contacts', contact); },
    listContacts: function () { return getAll('contacts'); },
    getConversation: function (id) { return get('conversations', id); },
    saveConversation: function (conversation) { return put('conversations', conversation); },
    listConversations: async function () {
      var conversations = await getAll('conversations');
      return conversations.sort(function (a, b) {
        return (b.lastMessageAt || b.createdAt || 0) - (a.lastMessageAt || a.createdAt || 0);
      });
    },
    deleteConversation: function (id) { return remove('conversations', id); },
    getMessage: function (id) { return get('messages', id); },
    saveMessage: function (message) {
      message.sortAt = message.clientCreatedAt || message.serverCreatedAt || Date.now();
      return put('messages', message);
    },
    deleteMessage: function (id) { return remove('messages', id); },
    listMessages: async function (conversationId) {
      var db = await open();
      var index = db.transaction('messages', 'readonly').objectStore('messages').index('conversation');
      var rows = await requestResult(index.getAll(conversationId));
      return rows.sort(function (a, b) { return (a.sortAt || 0) - (b.sortAt || 0); });
    },
    addOutbox: function (entry) { return put('outbox', entry); },
    removeOutbox: function (id) { return remove('outbox', id); },
    listOutbox: function () { return getAll('outbox'); },
    getSetting: async function (key) {
      var row = await get('settings', key);
      return row ? row.value : undefined;
    },
    setSetting: function (key, value) { return put('settings', { key: key, value: value }); },
    clearMessagingData: function () {
      return clearStores(['identity', 'contacts', 'conversations', 'messages', 'outbox']);
    },
    clearAll: function () {
      return clearStores(['identity', 'contacts', 'conversations', 'messages', 'outbox', 'settings']);
    }
  };

  global.VartalapDB = api;
})(window);
