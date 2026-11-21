/* Applied before first paint so Carbon's theme tokens are correct immediately. */
(function () {
  'use strict';
  var KEY = 'vartalap.theme';
  var dark = false;
  try {
    var saved = localStorage.getItem(KEY);
    dark = saved ? saved === 'dark' : window.matchMedia('(prefers-color-scheme: dark)').matches;
  } catch (error) {
    dark = window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches;
  }
  var root = document.documentElement;
  root.classList.remove('cds--white', 'cds--g100');
  root.classList.add(dark ? 'cds--g100' : 'cds--white');
  root.style.colorScheme = dark ? 'dark' : 'light';
})();
