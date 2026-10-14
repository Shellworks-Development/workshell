// Notes pinned to dashboard charts. Charts on a page ask for their notes
// separately; requests made in the same tick are batched into one.
import { reactive } from 'vue'
import { api, type ChartAnnotation } from './api'

const cache = reactive(new Map<string, ChartAnnotation[]>())
const queued = new Map<number, Set<string>>()
let timer: ReturnType<typeof setTimeout> | null = null

const cacheKey = (pid: number, key: string) => `${pid}\u0000${key}`

function flush() {
  timer = null
  for (const [pid, keys] of queued) {
    const list = [...keys]
    queued.delete(pid)
    for (let i = 0; i < list.length; i += 40) {
      const chunk = list.slice(i, i + 40)
      api
        .get<ChartAnnotation[]>(`/projects/${pid}/annotations`, { keys: chunk.join(',') })
        .then((rows) => {
          for (const k of chunk) cache.set(cacheKey(pid, k), rows.filter((r) => r.chartKey === k))
        })
        .catch(() => {
          // Notes are an extra; a failed load just shows none.
        })
    }
  }
}

/** Notes for one chart (reactive; loads on first use). */
export function chartNotes(pid: number, key: string): ChartAnnotation[] {
  const k = cacheKey(pid, key)
  if (!cache.has(k)) {
    cache.set(k, [])
    const set = queued.get(pid) ?? new Set()
    set.add(key)
    queued.set(pid, set)
    timer ??= setTimeout(flush, 0)
  }
  return cache.get(k)!
}

export async function addChartNote(pid: number, key: string, n: { ts: number; value: number | null; series: string; text: string }) {
  const a = await api.post<ChartAnnotation>(`/projects/${pid}/annotations`, { chartKey: key, ...n })
  cache.set(cacheKey(pid, key), [...(cache.get(cacheKey(pid, key)) ?? []), a])
  return a
}

export async function removeChartNote(pid: number, key: string, id: number) {
  await api.del(`/projects/${pid}/annotations/${id}`)
  cache.set(
    cacheKey(pid, key),
    (cache.get(cacheKey(pid, key)) ?? []).filter((a) => a.id !== id),
  )
}
