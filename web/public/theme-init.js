// Applies the saved theme before first paint (the CSP forbids inline scripts).
// Kept in sync with src/lib/theme.tsx.
(function () {
  var pref = 'dark'
  try { pref = localStorage.getItem('kairos-theme') || 'dark' } catch (e) {}
  var dark = pref === 'dark' || (pref === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches)
  document.documentElement.classList.toggle('dark', dark)
})()
