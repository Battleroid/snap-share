/* Patchbay theme boot. Load in <head> as a plain blocking script (no defer, no module) before patchbay.css paints:
   <script src="pb-theme-boot.js"></script>. Reads localStorage 'pb-theme' (light|dark|system), else <html data-theme-default>,
   else the system setting, and sets data-theme and data-theme-choice on <html>. Safe under script-src 'self'. */
(function(d){var r=d.documentElement,c=null,ok={light:1,dark:1,system:1};
  try{c=localStorage.getItem('pb-theme');}catch(e){}
  if(!ok[c])c=r.getAttribute('data-theme-default');if(!ok[c])c='system';
  var dark=c==='dark'||(c==='system'&&!!window.matchMedia&&matchMedia('(prefers-color-scheme: dark)').matches);
  r.setAttribute('data-theme',dark?'dark':'light');r.setAttribute('data-theme-choice',c);})(document);
