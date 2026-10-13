// Applies the saved theme before first paint (kept out of index.html so the
// Content-Security-Policy can forbid inline scripts).
try {
  var saved = JSON.parse(localStorage.getItem('workshell-theme-vars') || 'null')
  var pref = localStorage.getItem('workshell-theme') || 'system'
  var root = document.documentElement
  if (saved && pref !== 'system') {
    root.classList.toggle('dark', !!saved.dark)
    for (var k in saved.vars) root.style.setProperty(k, saved.vars[k])
  } else {
    root.classList.toggle('dark', matchMedia('(prefers-color-scheme: dark)').matches)
  }
} catch (e) {}
