<script setup lang="ts">
import { Network, Timer, X } from 'lucide-vue-next'
import { computed, ref } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import Badge from '@/components/Badge.vue'
import EmptyState from '@/components/EmptyState.vue'
import ErrorBanner from '@/components/ErrorBanner.vue'
import PageHeader from '@/components/PageHeader.vue'
import RangePicker from '@/components/RangePicker.vue'
import RelTime from '@/components/RelTime.vue'
import Skeleton from '@/components/Skeleton.vue'
import TimeChart from '@/components/TimeChart.vue'
import { api, type Timeseries } from '@/lib/api'
import { num, pct, shortId, unit } from '@/lib/format'
import { useProject } from '@/lib/project'
import { useAsync } from '@/lib/useAsync'
import TraceDialog from '@/components/TraceDialog.vue'

const { pid } = useProject()
const route = useRoute()
const router = useRouter()
const range = ref('24h')

interface Txn {
  name: string
  count: number
  errors: number
  avgMs: number
  p50: number
  p95: number
  p99: number
  perMinute: number
}
interface Perf {
  transactions: Txn[]
  platforms: { platform: string; samples: number; fps: number; pingMs: number; memoryMb: number; lowFpsShare: number }[]
  client: Timeseries
  server: Timeseries
}
const { data, error } = useAsync(() => api.get<Perf>(`/projects/${pid.value}/performance`, { range: range.value }), [pid, range])

const selected = computed(() => (typeof route.query.span === 'string' ? route.query.span : ''))
function select(name: string) {
  router.replace({ query: name ? { span: name } : {} })
}
interface SpanDetail {
  series: Timeseries
  slowest: { ts: number; durationMs: number; status: string; sessionKey: string; userId: number | null; jobId: string; release: string; traceId: string }[]
  releases: { release: string; count: number; avgMs: number }[]
}
const detail = useAsync(
  async () => (selected.value ? api.get<SpanDetail>(`/projects/${pid.value}/performance/spans`, { name: selected.value, range: range.value }) : null),
  [pid, selected, range],
)
const traceId = ref<string | null>(null)
const ms = (v: number) => (v >= 1000 ? `${num(v / 1000)} s` : `${num(v)} ms`)
</script>

<template>
  <TraceDialog :trace-id="traceId" @close="traceId = null" />
  <PageHeader title="Performance" subtitle="Traced operations, client frame rate and ping, and server health.">
    <RangePicker v-model="range" :options="['1h', '24h', '7d', '30d']" />
  </PageHeader>
  <ErrorBanner :message="error" />
  <div v-if="!data && !error" class="space-y-3"><Skeleton h="12rem" /><Skeleton h="12rem" /></div>

  <template v-if="data">
    <section class="mb-6">
      <h2 class="section-title">Traced operations</h2>
      <div class="card overflow-x-auto">
        <EmptyState v-if="data.transactions.length === 0" title="No traces yet" :icon="Timer">
          Time code with <code class="mono">Workshell.trace("datastore.load", fn, player)</code> or
          <code class="mono">Workshell.startSpan(name)</code> to see p50/p95 durations here.
        </EmptyState>
        <table v-else class="table">
          <thead>
            <tr><th>Operation</th><th class="text-right">Throughput</th><th class="text-right">p50</th><th class="text-right">p95</th><th class="text-right">p99</th><th class="text-right">Errors</th></tr>
          </thead>
          <tbody>
            <tr v-for="t in data.transactions" :key="t.name" class="clickable" :class="selected === t.name ? '[&>td]:bg-surface-2' : ''" @click="select(t.name)">
              <td class="mono font-medium">{{ t.name }}</td>
              <td class="tabular text-right text-muted">{{ num(t.perMinute) }}/min</td>
              <td class="tabular text-right">{{ ms(t.p50) }}</td>
              <td class="tabular text-right" :class="t.p95 > 1000 ? 'text-warning' : ''">{{ ms(t.p95) }}</td>
              <td class="tabular text-right text-muted">{{ ms(t.p99) }}</td>
              <td class="tabular text-right" :class="t.errors ? 'text-danger' : 'text-faint'">{{ pct(t.errors / Math.max(1, t.count)) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <section v-if="selected" class="mb-6">
      <div class="card">
        <div class="card-header">
          <span class="card-title mono">{{ selected }}</span>
          <button class="btn btn-ghost btn-icon size-7" aria-label="Close" @click="select('')"><X class="size-4" /></button>
        </div>
        <div v-if="detail.data.value" class="grid gap-5 p-4 lg:grid-cols-[1fr_20rem]">
          <div class="min-w-0 space-y-4">
            <TimeChart :data="detail.data.value.series" type="line" :height="180" :format="(v: number) => ms(v)" :series="[{ key: 'p50', label: 'p50' }, { key: 'p95', label: 'p95' }]" />
            <div>
              <h3 class="mb-2 text-[13px] font-medium">Slowest</h3>
              <div class="overflow-hidden rounded-lg border border-border">
                <div v-for="(s, i) in detail.data.value.slowest" :key="i" class="flex items-center gap-3 border-b border-border px-3 py-2 text-[13px] last:border-0">
                  <span class="tabular w-20 font-medium">{{ ms(s.durationMs) }}</span>
                  <Badge v-if="s.status !== 'ok'" tone="danger">{{ s.status }}</Badge>
                  <RouterLink v-if="s.sessionKey" :to="{ name: 'session', params: { pid, sid: s.sessionKey } }" class="mono link text-muted">session {{ shortId(s.sessionKey) }}</RouterLink>
                  <span v-else class="mono text-faint">server {{ shortId(s.jobId) }}</span>
                  <span class="mono text-faint">{{ s.release }}</span>
                  <span class="ml-auto text-xs text-faint"><RelTime :ts="s.ts" /></span>
                  <button v-if="s.traceId" class="btn btn-sm -my-1 shrink-0" @click="traceId = s.traceId"><Network class="size-3.5" /> Trace</button>
                </div>
              </div>
            </div>
          </div>
          <div>
            <h3 class="mb-2 text-[13px] font-medium">By release</h3>
            <div class="overflow-hidden rounded-lg border border-border">
              <div v-for="r in detail.data.value.releases" :key="r.release" class="flex justify-between border-b border-border px-3 py-2 text-[13px] last:border-0">
                <span class="mono">{{ r.release || '—' }}</span>
                <span class="tabular text-muted">{{ ms(r.avgMs) }} avg · {{ num(r.count) }}</span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>

    <div class="grid gap-4 lg:grid-cols-2">
      <div class="card">
        <div class="card-header"><span class="card-title">Client frame rate & ping</span></div>
        <div class="p-3">
          <TimeChart :data="data.client" type="line" :series="[{ key: 'fps', label: 'FPS', format: unit('fps') }, { key: 'pingMs', label: 'Ping', right: true, format: unit('ms') }]" />
        </div>
      </div>
      <div class="card">
        <div class="card-header"><span class="card-title">Server frame rate & heartbeat</span></div>
        <div class="p-3">
          <TimeChart :data="data.server" type="line" :series="[{ key: 'fps', label: 'Server FPS', format: unit('fps') }, { key: 'heartbeatMs', label: 'Heartbeat', right: true, format: unit('ms') }]" />
        </div>
      </div>
    </div>

    <section class="mt-6">
      <h2 class="section-title">Clients by platform</h2>
      <div class="card overflow-x-auto">
        <div v-if="data.platforms.length === 0" class="px-4 py-8 text-center text-[13px] text-muted">
          No client samples yet. Require the Workshell module from a LocalScript to collect client FPS, memory and ping.
        </div>
        <table v-else class="table">
          <thead><tr><th>Platform</th><th class="text-right">Avg FPS</th><th class="text-right">Below 30 FPS</th><th class="text-right">Ping</th><th class="text-right">Memory</th><th class="text-right">Samples</th></tr></thead>
          <tbody>
            <tr v-for="p in data.platforms" :key="p.platform">
              <td>{{ p.platform }}</td>
              <td class="tabular text-right" :class="p.fps < 30 ? 'text-danger' : p.fps < 50 ? 'text-warning' : ''">{{ num(p.fps) }}</td>
              <td class="tabular text-right" :class="p.lowFpsShare > 0.2 ? 'text-warning' : 'text-muted'">{{ pct(p.lowFpsShare) }}</td>
              <td class="tabular text-right">{{ num(p.pingMs) }} ms</td>
              <td class="tabular text-right">{{ num(p.memoryMb) }} MB</td>
              <td class="tabular text-right text-muted">{{ num(p.samples) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
  </template>
</template>
