import { onBeforeUnmount, ref, watch, type Ref } from 'vue'
import type { LiveMsg } from './api'

/**
 * Subscribes to a project's live stream (Server-Sent Events). The browser
 * reconnects automatically; `connected` reflects the current state.
 */
export function useLive(pid: Ref<number>, onMessage: (m: LiveMsg) => void, enabled: Ref<boolean> = ref(true)) {
  const connected = ref(false)
  let es: EventSource | null = null

  function close() {
    es?.close()
    es = null
    connected.value = false
  }
  function open() {
    close()
    if (!enabled.value) return
    es = new EventSource(`/api/projects/${pid.value}/live`)
    es.onopen = () => (connected.value = true)
    es.onerror = () => (connected.value = false)
    es.onmessage = (e) => {
      try {
        for (const m of JSON.parse(e.data) as LiveMsg[]) onMessage(m)
      } catch {
        // ignore malformed frames
      }
    }
  }
  watch([pid, enabled], open, { immediate: true })
  onBeforeUnmount(close)
  return { connected }
}
