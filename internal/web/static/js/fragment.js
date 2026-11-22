// Classic script (not a module, no defer or async) for pages whose URL
// fragment carries a key or token: the secret reveal page (/s/{id}#key) and
// the token pages (/verify, /reset and /invite #t=…, /x#token). Referenced
// from the page's <head>, it runs before the rest of the page is parsed and
// before any module is fetched: it copies the fragment into
// window.snapFragment and removes it from the address bar and the session
// history entry, so the key does not stay there when a module loads slowly
// or fails to load. The page's module reads window.snapFragment and deletes
// it.
//
// It can't stop the browser from recording the full URL in its history
// database when the navigation commits, before any script runs: see
// docs/security.md.
(function () {
  "use strict";
  var hash = window.location.hash;
  window.snapFragment = hash ? hash.slice(1) : "";
  if (hash) {
    try {
      window.history.replaceState(window.history.state, "", window.location.pathname + window.location.search);
    } catch (e) {
      // The module tries again.
    }
  }
})();
