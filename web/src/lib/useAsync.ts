import { onBeforeUnmount, ref, shallowRef, watch, type WatchSource } from 'vue'

/**
 * Loads data with `fn`, re-running when any watched source changes and,
 * optionally, polling every `refreshMs` while the tab is visible. Earlier
 * in-flight results are discarded if a newer load has started.
 */
export function useAsync<T>(fn: () => Promise<T>, sources: WatchSource[] = [], opts: { refreshMs?: number } = {}) {
  const data = shallowRef<T | null>(null)
  const error = ref<string | null>(null)
  const loading = ref(false)
  let seq = 0

  async function load(quiet = false) {
    const id = ++seq
    if (!quiet) loading.value = true
    try {
      const result = await fn()
      if (id === seq) {
        data.value = result
        error.value = null
      }
    } catch (e) {
      if (id === seq) error.value = e instanceof Error ? e.message : String(e)
    } finally {
      if (id === seq) loading.value = false
    }
  }

  watch(sources, () => load(), { immediate: true })

  if (opts.refreshMs) {
    const timer = setInterval(() => {
      if (document.visibilityState === 'visible') load(true)
    }, opts.refreshMs)
    onBeforeUnmount(() => clearInterval(timer))
  }

  return { data, error, loading, reload: load }
}
