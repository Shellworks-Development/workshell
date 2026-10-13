<script setup lang="ts">
import { Pause, Play, ScrollText, Search, X } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import EmptyState from '@/components/EmptyState.vue'
import ErrorBanner from '@/components/ErrorBanner.vue'
import PageHeader from '@/components/PageHeader.vue'
import RangePicker from '@/components/RangePicker.vue'
import TimeChart from '@/components/TimeChart.vue'
import { api, type LiveMsg, type LogLine, type Timeseries } from '@/lib/api'
import { useLive } from '@/lib/live'
import { levelColors } from '@/lib/chartTheme'
import { dateTime, num, shortId } from '@/lib/format'
import { useProject } from '@/lib/project'

const { pid } = useProject()
const route = useRoute()
const router = useRouter()

const levels = ['debug', 'info', 'warn', 'error'] as const
const enabled = ref<Set<string>>(new Set(['info', 'warn', 'error']))
const range = ref('24h')
const search = ref('')
const query = ref('')
const jobId = ref(typeof route.query.jobId === 'string' ? route.query.jobId : '')
const userId = ref(typeof route.query.userId === 'string' ? route.query.userId : '')
const logs = ref<LogLine[]>([])
const histogram = ref<Timeseries | null>(null)
const error = ref<string | null>(null)
const loading = ref(false)
const hasMore = ref(false)
const expanded = ref<Set<number>>(new Set())
const live = ref(false)

let searchTimer: ReturnType<typeof setTimeout> | undefined
watch(search, (v) => {
  clearTimeout(searchTimer)
  searchTimer = setTimeout(() => (query.value = v), 300)
})

const filters = computed(() => ({
  range: range.value,
  level: [...enabled.value].join(','),
  q: query.value,
  jobId: jobId.value,
  userId: userId.value,
}))

const PAGE = 200
let seq = 0
async function load() {
  const id = ++seq
  loading.value = true
  try {
    const res = await api.get<{ logs: LogLine[]; histogram: Timeseries }>(`/projects/${pid.value}/logs`, {
      ...filters.value,
      histogram: 1,
      limit: PAGE,
    })
    if (id !== seq) return
    logs.value = res.logs
    histogram.value = res.histogram
    hasMore.value = res.logs.length === PAGE
    error.value = null
  } catch (e) {
    if (id === seq) error.value = (e as Error).message
  } finally {
    if (id === seq) loading.value = false
  }
}

async function loadOlder() {
  const last = logs.value[logs.value.length - 1]
  if (!last) return
  const res = await api.get<{ logs: LogLine[] }>(`/projects/${pid.value}/logs`, { ...filters.value, before: last.id, limit: PAGE })
  logs.value = [...logs.value, ...res.logs]
  hasMore.value = res.logs.length === PAGE
}

// Live tail: new lines stream in over SSE; those matching the current
// filters are prepended with a brief highlight.
const arrived = ref<Set<number>>(new Set())
const { connected } = useLive(
  pid,
  (m: LiveMsg) => {
    if (m.t !== 'log') return
    const l = m.d
    if (!enabled.value.has(l.level)) return
    if (query.value && !l.message.toLowerCase().includes(query.value.toLowerCase())) return
    if (jobId.value && l.jobId !== jobId.value) return
    if (userId.value && String(l.userId ?? '') !== userId.value) return
    arrived.value = new Set([...arrived.value, l.id].slice(-200))
    logs.value = [{ ...l, attrs: {} }, ...logs.value].slice(0, 2000)
  },
  live,
)

watch([pid, filters], load, { immediate: true, deep: true })
watch([jobId, userId], () => {
  router.replace({ query: { ...(jobId.value ? { jobId: jobId.value } : {}), ...(userId.value ? { userId: userId.value } : {}) } })
})

function toggleLevel(l: string) {
  const s = new Set(enabled.value)
  if (s.has(l)) s.delete(l)
  else s.add(l)
  if (s.size === 0) return
  enabled.value = s
}
function toggleExpand(id: number) {
  const s = new Set(expanded.value)
  if (s.has(id)) s.delete(id)
  else s.add(id)
  expanded.value = s
}
const hasAttrs = (l: LogLine) => l.attrs && Object.keys(l.attrs).length > 0

const levelClass: Record<string, string> = {
  debug: 'text-faint',
  info: 'text-info',
  warn: 'text-warning',
  error: 'text-danger',
}
</script>

<template>
  <PageHeader title="Logs" subtitle="Output and structured logs from every game server.">
    <button class="btn" :class="live ? 'border-success/40 text-success' : ''" :aria-pressed="live" @click="live = !live">
      <span v-if="live" class="relative flex size-2">
        <span class="absolute inline-flex size-full animate-ping rounded-full bg-success opacity-60" />
        <span class="relative inline-flex size-2 rounded-full bg-success" />
      </span>
      <Play v-else class="size-4" />
      {{ live ? (connected ? 'Live' : 'Connecting…') : 'Go live' }}
      <Pause v-if="live" class="size-3.5 opacity-60" />
    </button>
    <RangePicker v-model="range" :options="['1h', '6h', '24h', '7d']" />
  </PageHeader>
  <ErrorBanner :message="error" />

  <div class="card mb-3">
    <div class="flex flex-wrap items-center gap-2 border-b border-border p-2.5">
      <div class="flex gap-1">
        <button
          v-for="l in levels"
          :key="l"
          class="flex h-7 cursor-pointer items-center gap-1.5 rounded-md border px-2 text-xs font-medium transition-colors"
          :class="enabled.has(l) ? 'border-border-strong bg-surface-2 text-fg' : 'border-transparent text-faint hover:text-muted'"
          @click="toggleLevel(l)"
        >
          <span class="size-2 rounded-full" :style="{ background: enabled.has(l) ? levelColors[l] : 'var(--surface-3)' }" />
          {{ l }}
        </button>
      </div>
      <div class="relative min-w-48 flex-1">
        <Search class="pointer-events-none absolute top-2 left-2.5 size-4 text-faint" />
        <input v-model="search" class="input pl-8" placeholder="Search messages" />
      </div>
      <input v-model="jobId" class="input w-40" placeholder="Server job id" />
      <input v-model="userId" class="input w-32" placeholder="User id" inputmode="numeric" />
    </div>
    <div class="p-3">
      <TimeChart
        :data="histogram"
        type="bar"
        stacked
        :height="110"
        :legend="false"
        :series="levels.filter((l) => enabled.has(l)).map((l) => ({ key: l, label: l, color: levelColors[l] }))"
      />
    </div>
  </div>

  <div class="card overflow-hidden">
    <EmptyState v-if="!loading && logs.length === 0" title="No logs match" :icon="ScrollText">
      Try a wider time range or fewer filters. Server output is captured automatically; use
      <code class="mono">Workshell.info()</code> for structured logs.
    </EmptyState>
    <div class="mono divide-y divide-border">
      <div v-for="l in logs" :key="l.id" class="group">
        <div
          class="flex items-start gap-3 px-3 py-1.5 hover:bg-surface-2"
          :class="{ 'cursor-pointer': hasAttrs(l), 'bg-danger-soft/40': l.level === 'error', 'just-arrived': arrived.has(l.id) }"
          @click="hasAttrs(l) && toggleExpand(l.id)"
        >
          <span class="w-36 shrink-0 text-faint">{{ dateTime(l.ts) }}</span>
          <span class="w-10 shrink-0 font-semibold uppercase" :class="levelClass[l.level]">{{ l.level }}</span>
          <span class="min-w-0 flex-1 break-all whitespace-pre-wrap">{{ l.message }}</span>
          <span v-if="hasAttrs(l)" class="chip shrink-0">{{ Object.keys(l.attrs).length }} attrs</span>
          <button
            v-if="l.userId"
            class="hidden shrink-0 text-faint hover:text-accent sm:block"
            title="Filter by this player"
            @click.stop="userId = String(l.userId)"
          >
            user:{{ l.userId }}
          </button>
          <button class="hidden w-20 shrink-0 text-right text-faint hover:text-accent sm:block" title="Filter by this server" @click.stop="jobId = l.jobId">
            {{ shortId(l.jobId) }}
          </button>
        </div>
        <div v-if="expanded.has(l.id)" class="bg-surface-2 px-3 py-2 pl-52">
          <pre class="whitespace-pre-wrap">{{ JSON.stringify(l.attrs, null, 2) }}</pre>
        </div>
      </div>
    </div>
    <div v-if="hasMore" class="border-t border-border p-2 text-center">
      <button class="btn btn-sm" @click="loadOlder">Load older</button>
    </div>
  </div>

  <div v-if="jobId || userId" class="fixed right-4 bottom-4 flex gap-2">
    <span v-if="jobId" class="chip border border-border bg-surface py-1 shadow-sm">
      server {{ shortId(jobId) }} <button aria-label="Clear server filter" @click="jobId = ''"><X class="size-3" /></button>
    </span>
    <span v-if="userId" class="chip border border-border bg-surface py-1 shadow-sm">
      <RouterLink :to="{ name: 'player', params: { pid, userId } }" class="hover:text-accent">user {{ userId }}</RouterLink>
      <button aria-label="Clear user filter" @click="userId = ''"><X class="size-3" /></button>
    </span>
  </div>
  <div class="sr-only">{{ num(logs.length) }} log lines loaded</div>
</template>
