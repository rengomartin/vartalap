/* Small shared UI helpers: toasts, theme toggle, API fetch, formatting. */
(function (global) {
  'use strict';

  function toastArea() {
    var area = global.document.getElementById('toast-area');
    if (!area) {
      area = global.document.createElement('div');
      area.id = 'toast-area';
      area.setAttribute('aria-live', 'polite');
      global.document.body.appendChild(area);
    }
    return area;
  }

  function toast(message, kind) {
    var area = toastArea();
    var element = global.document.createElement('div');
    element.className = 'toast' + (kind === 'error' ? ' toast--error' : '');
    element.setAttribute('role', kind === 'error' ? 'alert' : 'status');
    var accent = global.document.createElement('span');
    accent.className = 'toast-accent';
    var copy = global.document.createElement('span');
    copy.textContent = message;
    element.appendChild(accent);
    element.appendChild(copy);
    area.appendChild(element);
    global.setTimeout(function () {
      if (element.parentNode) {
        element.parentNode.removeChild(element);
      }
    }, kind === 'error' ? 7000 : 4500);
  }

  function applyTheme(dark) {
    var root = global.document.documentElement;
    root.classList.remove('cds--white', 'cds--g100');
    root.classList.add(dark ? 'cds--g100' : 'cds--white');
    root.style.colorScheme = dark ? 'dark' : 'light';
  }

  function toggleTheme() {
    var dark = !global.document.documentElement.classList.contains('cds--g100');
    applyTheme(dark);
    try {
      global.localStorage.setItem('vartalap.theme', dark ? 'dark' : 'light');
    } catch (error) {
      /* localStorage may be unavailable in private modes. */
    }
    return dark;
  }

  function bindTheme(selector) {
    var buttons = global.document.querySelectorAll(selector || '#theme-toggle');
    Array.prototype.forEach.call(buttons, function (button) {
      button.addEventListener('click', function () { toggleTheme(); });
    });
  }

  function csrfToken() {
    var meta = global.document.querySelector('meta[name="csrf-token"]');
    return meta ? meta.getAttribute('content') || '' : '';
  }

  async function api(path, options) {
    options = options || {};
    var method = (options.method || 'GET').toUpperCase();
    var headers = Object.assign({}, options.headers || {});
    if (method !== 'GET' && method !== 'HEAD') {
      headers['X-CSRF-Token'] = csrfToken();
    }
    if (options.json !== undefined) {
      headers['Content-Type'] = 'application/json';
      options.body = JSON.stringify(options.json);
      delete options.json;
    }
    var response = await global.fetch(path, Object.assign({}, options, {
      method: method,
      headers: headers,
      credentials: 'same-origin'
    }));
    var contentType = response.headers.get('content-type') || '';
    var data = null;
    if (contentType.indexOf('application/json') >= 0) {
      data = await response.json().catch(function () { return null; });
    }
    if (!response.ok) {
      var error = new Error((data && data.message) || 'That request failed.');
      error.status = response.status;
      error.code = data && data.error;
      throw error;
    }
    return data;
  }

  function setBusy(button, busy, busyLabel) {
    if (!button) {
      return;
    }
    if (busy) {
      button.dataset.idleHtml = button.innerHTML;
      button.disabled = true;
      if (busyLabel) {
        button.textContent = busyLabel;
      }
    } else {
      button.disabled = false;
      if (button.dataset.idleHtml) {
        button.innerHTML = button.dataset.idleHtml;
        delete button.dataset.idleHtml;
      }
    }
  }

  function pad(value) {
    return String(value).padStart(2, '0');
  }

  function formatTime(timestamp) {
    var date = new Date(timestamp);
    if (Number.isNaN(date.getTime())) {
      return '';
    }
    var now = new Date();
    var sameDay = date.toDateString() === now.toDateString();
    if (sameDay) {
      return pad(date.getHours()) + ':' + pad(date.getMinutes());
    }
    return date.toLocaleDateString(undefined, { day: 'numeric', month: 'short' }) +
      ' ' + pad(date.getHours()) + ':' + pad(date.getMinutes());
  }

  function formatBytes(bytes) {
    if (!bytes || bytes < 0) {
      return '0 B';
    }
    var units = ['B', 'KiB', 'MiB', 'GiB'];
    var value = bytes;
    var unit = 0;
    while (value >= 1024 && unit < units.length - 1) {
      value /= 1024;
      unit += 1;
    }
    return (unit === 0 ? value : value.toFixed(1)) + ' ' + units[unit];
  }

  function initial(name) {
    var text = String(name || '').trim();
    return text ? text.charAt(0).toUpperCase() : '?';
  }

  function debounce(fn, delay) {
    var timer = null;
    return function () {
      var args = arguments;
      var context = this;
      global.clearTimeout(timer);
      timer = global.setTimeout(function () { fn.apply(context, args); }, delay);
    };
  }

  async function copyText(text) {
    if (global.navigator.clipboard && global.isSecureContext) {
      await global.navigator.clipboard.writeText(text);
      return true;
    }
    var area = global.document.createElement('textarea');
    area.value = text;
    area.setAttribute('readonly', '');
    area.style.position = 'fixed';
    area.style.opacity = '0';
    global.document.body.appendChild(area);
    area.select();
    var ok = false;
    try {
      ok = global.document.execCommand('copy');
    } catch (error) {
      ok = false;
    }
    global.document.body.removeChild(area);
    return ok;
  }

  global.VartalapUI = {
    toast: toast,
    toggleTheme: toggleTheme,
    bindTheme: bindTheme,
    api: api,
    setBusy: setBusy,
    formatTime: formatTime,
    formatBytes: formatBytes,
    initial: initial,
    debounce: debounce,
    copyText: copyText
  };
})(window);
