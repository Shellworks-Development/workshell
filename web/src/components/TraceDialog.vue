<script setup lang="ts">
import { Presentation } from 'lucide-vue-next'
import { computed, nextTick, ref, watch } from 'vue'
import { api, type TraceSpan } from '@/lib/api'
import { spanMs } from '@/lib/format'
import { useProject } from '@/lib/project'
import { here, sendToWhiteboard, traceItems } from '@/lib/whiteboard'
import ErrorBanner from './ErrorBanner.vue'
import Modal from './Modal.vue'

/** A trace as a waterfall: one row per span, nested under its parent. */
const props = defineProps<{ traceId: string | null }>()
const emit = defineEmits<{ close: [] }>()
const { pid } = useProject()
const spans = ref<TraceSpan[] | null>(null)
const error = ref('')

watch(
  () => props.traceId,
  async (id) => {
    spans.value = null
    error.value = ''
    if (!id) return
    try {
      spans.value = (await api.get<{ spans: TraceSpan[] }>(`/projects/${pid.value}/traces/${encodeURIComponent(id)}`)).spans
    } catch (e) {
      error.value = (e as Error).message
    }
  },
  { immediate: true },
)

const rows = computed(() => {
  const list = spans.value ?? []
  if (!list.length) return []
  const byParent = new Map<string, TraceSpan[]>()
  const ids = new Set(list.map((s) => s.spanId))
  for (const s of list) {
    const k = s.parent && ids.has(s.parent) ? s.parent : ''
    byParent.set(k, [...(byParent.get(k) ?? []), s])
  }
  const t0 = Math.min(...list.map((s) => s.start))
  const total = Math.max(...list.map((s) => s.start + s.durationMs)) - t0 || 1
  const out: { s: TraceSpan; depth: number; left: number; width: number }[] = []
  const walk = (parent: string, depth: number) => {
    for (const s of (byParent.get(parent) ?? []).sort((a, b) => a.start - b.start)) {
      if (out.length > 500 || out.some((o) => o.s === s)) continue
      out.push({ s, depth, left: ((s.start - t0) / total) * 100, width: (s.durationMs / total) * 100 })
      if (s.spanId) walk(s.spanId, depth + 1)
    }
  }
  walk('', 0)
  return out
})
const total = computed(() => {
  const list = spans.value ?? []
  return list.length ? Math.max(...list.map((s) => s.start + s.durationMs)) - Math.min(...list.map((s) => s.start)) : 0
})

function send() {
  if (!spans.value?.length || !props.traceId) return
  const items = traceItems(props.traceId, spans.value, here())
  emit('close')
  nextTick(() => sendToWhiteboard(pid.value, items))
}
</script>

<template>
  <Modal :open="!!traceId" :title="rows[0] ? `Trace · ${rows[0].s.name}` : 'Trace'" :description="spans ? `${spans.length} spans · ${spanMs(total)} end to end` : undefined" width="46rem" @close="emit('close')">
    <ErrorBanner :message="error" />
    <div v-if="!spans && !error" class="space-y-2"><div v-for="i in 4" :key="i" class="skeleton h-6" /></div>
    <div v-else-if="rows.length" class="max-h-[55vh] overflow-y-auto overscroll-contain">
      <div v-for="r in rows" :key="r.s.spanId + r.s.start" class="grid grid-cols-[minmax(0,13rem)_minmax(0,1fr)] items-center gap-3 py-1 text-[12.5px] max-sm:grid-cols-1 max-sm:gap-1">
        <div class="flex min-w-0 items-center gap-1.5" :style="{ paddingLeft: `${r.depth * 12}px` }">
          <span class="mono truncate" :class="r.s.status === 'error' ? 'text-danger' : 'text-fg'" :title="r.s.name">{{ r.s.name }}</span>
        </div>
        <div class="relative h-5 rounded bg-surface-2">
          <div
            class="absolute inset-y-0.5 rounded-sm"
            :class="r.s.status === 'error' ? 'bg-danger/70' : 'bg-accent/70'"
            :style="{ left: `${r.left}%`, width: `max(2px, ${r.width}%)` }"
          />
          <!-- Long bars carry their label inside; short ones put it just after. -->
          <span
            class="tabular absolute inset-y-0 flex items-center text-[11px] whitespace-nowrap"
            :class="r.left + r.width > 70 ? 'font-medium text-fg' : 'text-muted'"
            :style="r.left + r.width > 70 ? { right: `calc(${100 - r.left - r.width}% + 6px)` } : { left: `calc(${r.left + r.width}% + 6px)` }"
          >
            {{ spanMs(r.s.durationMs) }}
          </span>
        </div>
      </div>
    </div>
    <template #footer>
      <button class="btn" @click="emit('close')">Close</button>
      <button class="btn btn-primary" :disabled="!rows.length" @click="send"><Presentation class="size-4" /> Send to whiteboard</button>
    </template>
  </Modal>
</template>
