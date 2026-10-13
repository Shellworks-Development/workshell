import { ref } from 'vue'

// Captured "install app" prompt (Chromium); shown as a menu item when available.
interface InstallPrompt extends Event {
  prompt(): Promise<void>
  userChoice: Promise<{ outcome: 'accepted' | 'dismissed' }>
}
let deferred: InstallPrompt | null = null
export const canInstall = ref(false)
export const isStandalone = matchMedia('(display-mode: standalone)').matches

window.addEventListener('beforeinstallprompt', (e) => {
  e.preventDefault()
  deferred = e as InstallPrompt
  canInstall.value = true
})
window.addEventListener('appinstalled', () => {
  deferred = null
  canInstall.value = false
})

export async function install() {
  if (!deferred) return
  await deferred.prompt()
  await deferred.userChoice
  deferred = null
  canInstall.value = false
}

export function registerServiceWorker() {
  if (!('serviceWorker' in navigator) || import.meta.env.DEV) return
  window.addEventListener('load', () => {
    navigator.serviceWorker.register('/sw.js').catch(() => {})
  })
}
