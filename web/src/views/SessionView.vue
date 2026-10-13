<script setup lang="ts">
import { ArrowLeft, ScrollText } from 'lucide-vue-next'
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'
import Badge from '@/components/Badge.vue'
import CopyButton from '@/components/CopyButton.vue'
import ErrorBanner from '@/components/ErrorBanner.vue'
import KeyValues from '@/components/KeyValues.vue'
import RelTime from '@/components/RelTime.vue'
import Skeleton from '@/components/Skeleton.vue'
import TimeChart from '@/components/TimeChart.vue'
import Timeline from '@/components/Timeline.vue'
import { api, type Session, type Timeseries, type TimelineItem } from '@/lib/api'
import { duration, num, shortId } from '@/lib/format'
import { useProject } from '@/lib/project'
import { useAsync } from '@/lib/useAsync'

const props = defineProps<{ sid: string }>()
const { pid } = useProject()
interface Detail {
  session: Session
  timeline: TimelineItem[]
  perf: { ts: number; fps: number; memoryMb: number; pingMs: number }[]
}
const { data, error } = useAsync(() => api.get<Detail>(`/projects/${pid.value}/sessions/${encodeURIComponent(props.sid)}`), [pid, () => props.sid], {
  refreshMs: 20_000,
})

const kinds = [
  { value: 'error', label: 'Errors' },
  { value: 'event', label: 'Events' },
  { value: 'purchase', label: 'Purchases' },
  { value: 'log', label: 'Logs' },
  { value: 'span', label: 'Spans' },
  { value: 'detection', label: 'Detections' },
]
const shown = ref<string[]>([])
function toggleKind(k: string) {
  shown.value = shown.value.includes(k) ? shown.value.filter((x) => x !== k) : [...shown.value, k]
}
const filter = computed(() => (shown.value.length ? ['join', 'leave', ...shown.value] : undefined))
const counts = computed(() => {
  const c: Record<string, number> = {}
  for (const it of data.value?.timeline ?? []) c[it.kind] = (c[it.kind] ?? 0) + 1
  return c
})

const perfSeries = computed<Timeseries | null>(() => {
  const p = data.value?.perf
  if (!p?.length) return null
  return {
    bucket: 30_000,
    points: p.map((x) => x.ts),
    series: [
      { name: 'fps', values: p.map((x) => x.fps) },
      { name: 'pingMs', values: p.map((x) => x.pingMs) },
      { name: 'memoryMb', values: p.map((x) => x.memoryMb) },
    ],
  }
})
</script>

<template>
  <RouterLink :to="{ name: 'sessions', params: { pid } }" class="mb-4 inline-flex items-center gap-1 text-[13px] text-muted hover:text-fg"><ArrowLeft class="size-3.5" /> Sessions</RouterLink>
  <ErrorBanner :message="error" />
  <div v-if="!data && !error" class="space-y-3"><Skeleton w="40%" h="1.6rem" /><Skeleton h="20rem" /></div>

  <template v-if="data">
    <div class="mb-6 flex flex-wrap items-start justify-between gap-4 border-b border-border pb-5">
      <div>
        <div class="flex items-center gap-2">
          <h1 class="text-2xl font-semibold tracking-tight">Session <span class="mono text-xl font-normal text-muted">{{ shortId(data.session.key) }}</span></h1>
          <Badge :tone="data.session.endedAt ? 'neutral' : 'success'" dot>{{ data.session.endedAt ? 'Ended' : 'Live' }}</Badge>
        </div>
        <p class="mt-1.5 text-[13px] text-muted">
          <RouterLink :to="{ name: 'player', params: { pid, userId: data.session.userId } }" class="mono link text-fg">Player {{ data.session.userId }}</RouterLink>
          · started <RelTime :ts="data.session.startedAt" />
          · {{ duration((data.session.endedAt ?? Date.now()) - data.session.startedAt) }}
        </p>
      </div>
      <div class="flex gap-2">
        <CopyButton :text="data.session.key" label="Copy session id" />
        <RouterLink :to="{ name: 'logs', params: { pid }, query: { userId: String(data.session.userId) } }" class="btn btn-sm"><ScrollText class="size-3.5" /> Logs</RouterLink>
      </div>
    </div>

    <div class="grid gap-8 lg:grid-cols-[1fr_17rem]">
      <div class="min-w-0 space-y-5">
        <div v-if="perfSeries" class="card">
          <div class="card-header"><span class="card-title">Client performance</span></div>
          <div class="p-3">
            <TimeChart :data="perfSeries" type="line" :height="150" :series="[{ key: 'fps', label: 'FPS' }, { key: 'pingMs', label: 'Ping ms' }, { key: 'memoryMb', label: 'Memory MB', right: true }]" />
          </div>
        </div>

        <div class="card">
          <div class="card-header flex-wrap">
            <span class="card-title">Timeline</span>
            <div class="flex flex-wrap gap-1">
              <button
                v-for="k in kinds"
                :key="k.value"
                class="h-6 cursor-pointer rounded-full border px-2 text-xs transition-colors"
                :class="shown.includes(k.value) ? 'border-fg/40 bg-surface-3 text-fg' : 'border-border text-muted hover:text-fg'"
                :aria-pressed="shown.includes(k.value)"
                @click="toggleKind(k.value)"
              >
                {{ k.label }} <span class="tabular text-faint">{{ counts[k.value] ?? 0 }}</span>
              </button>
            </div>
          </div>
          <div class="p-4"><Timeline :items="data.timeline" :pid="pid" :filter="filter" /></div>
        </div>
      </div>

      <aside class="space-y-5 text-[13px] lg:sticky lg:top-20 lg:self-start">
        <section>
          <h3 class="mb-2 font-medium text-muted">Details</h3>
          <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5">
            <dt class="text-muted">Platform</dt><dd>{{ data.session.platform || '—' }}</dd>
            <dt class="text-muted">Country</dt><dd>{{ data.session.country || '—' }}</dd>
            <dt class="text-muted">Release</dt>
            <dd><RouterLink :to="{ name: 'release', params: { pid, version: data.session.release } }" class="mono link">{{ data.session.release || '—' }}</RouterLink></dd>
            <dt class="text-muted">Server</dt>
            <dd><RouterLink :to="{ name: 'server', params: { pid, jobId: data.session.jobId } }" class="mono link">{{ shortId(data.session.jobId) }}</RouterLink></dd>
            <dt class="text-muted">New player</dt><dd>{{ data.session.isNew ? 'Yes' : 'No' }}</dd>
            <dt class="text-muted">Errors</dt><dd :class="data.session.errorCount ? 'text-danger' : ''">{{ num(data.session.errorCount) }}</dd>
          </dl>
        </section>
        <section class="border-t border-border pt-5">
          <h3 class="mb-2 font-medium text-muted">Metadata</h3>
          <KeyValues :data="data.session.attrs" />
          <p class="hint">Set with <code class="mono">Workshell.setUser(player, {…})</code>.</p>
        </section>
      </aside>
    </div>
  </template>
</template>
