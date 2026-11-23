/* Public profile page: theme toggle and shareable link copy. */
(function (global) {
  'use strict';

  function start() {
    var UI = global.VartalapUI;
    UI.bindTheme('#theme-toggle');
    var copy = global.document.getElementById('profile-copy-link');
    if (copy) {
      copy.addEventListener('click', async function () {
        var link = copy.getAttribute('data-link') || global.location.pathname;
        try {
          await UI.copyText(global.location.origin + link);
          UI.toast('Profile link copied.');
        } catch (error) {
          UI.toast('Could not copy the link. Copy the address bar instead.', 'error');
        }
      });
    }
  }

  if (global.document.readyState === 'loading') {
    global.document.addEventListener('DOMContentLoaded', start);
  } else {
    start();
  }
})(window);
