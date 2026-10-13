<script setup lang="ts">
import { Gauge } from 'lucide-vue-next'
import { ref } from 'vue'
import Badge from '@/components/Badge.vue'
import EmptyState from '@/components/EmptyState.vue'
import ErrorBanner from '@/components/ErrorBanner.vue'
import PageHeader from '@/components/PageHeader.vue'
import RangePicker from '@/components/RangePicker.vue'
import TimeChart from '@/components/TimeChart.vue'
import { api, type Timeseries } from '@/lib/api'
import { ago, num } from '@/lib/format'
import { useProject } from '@/lib/project'
import { useAsync } from '@/lib/useAsync'

const { pid } = useProject()
const range = ref('24h')

interface MetricInfo {
  name: string
  kind: 'counter' | 'gauge' | 'timing'
  count: number
  sum: number
  min: number
  max: number
  lastSeen: number
}

const list = useAsync(() => api.get<MetricInfo[]>(`/projects/${pid.value}/metrics`, { range: range.value }), [pid, range])
const series = useAsync(
  async () => {
    const metrics = list.data.value ?? []
    const results = await Promise.all(metrics.map((m) => api.get<Timeseries>(`/projects/${pid.value}/metrics/series`, { name: m.name, range: range.value })))
    return Object.fromEntries(metrics.map((m, i) => [m.name, results[i]!]))
  },
  [list.data],
)

const unit = (m: MetricInfo) => (m.kind === 'timing' ? ' ms' : '')
</script>

<template>
  <PageHeader title="Metrics" subtitle="Counters, gauges and timings from Workshell.increment(), gauge() and time().">
    <RangePicker v-model="range" :options="['1h', '6h', '24h', '7d', '30d']" />
  </PageHeader>
  <ErrorBanner :message="list.error.value" />

  <div v-if="list.data.value && list.data.value.length === 0" class="card">
    <EmptyState title="No metrics in this range" :icon="Gauge">
      Count things with <code class="mono">Workshell.increment("shop.opened")</code>, record levels with
      <code class="mono">Workshell.gauge("queue.size", n)</code>, and time code with
      <code class="mono">Workshell.time("datastore.load", fn)</code>.
    </EmptyState>
  </div>

  <div class="grid gap-3 lg:grid-cols-2">
    <div v-for="m in list.data.value" :key="m.name" class="card">
      <div class="card-header">
        <div class="flex min-w-0 items-center gap-2">
          <span class="mono card-title truncate">{{ m.name }}</span>
          <Badge>{{ m.kind }}</Badge>
        </div>
        <span class="text-xs text-faint">{{ ago(m.lastSeen) }}</span>
      </div>
      <div class="grid grid-cols-3 gap-2 border-b border-border px-4 py-2.5 text-xs text-muted">
        <template v-if="m.kind === 'counter'">
          <div>Total <div class="tabular text-sm font-medium text-fg">{{ num(m.sum) }}</div></div>
          <div>Samples <div class="tabular text-sm font-medium text-fg">{{ num(m.count) }}</div></div>
          <div>Per sample <div class="tabular text-sm font-medium text-fg">{{ num(m.sum / Math.max(1, m.count)) }}</div></div>
        </template>
        <template v-else>
          <div>Avg <div class="tabular text-sm font-medium text-fg">{{ num(m.sum / Math.max(1, m.count)) }}{{ unit(m) }}</div></div>
          <div>Min <div class="tabular text-sm font-medium text-fg">{{ num(m.min) }}{{ unit(m) }}</div></div>
          <div>Max <div class="tabular text-sm font-medium text-fg">{{ num(m.max) }}{{ unit(m) }}</div></div>
        </template>
      </div>
      <div class="p-3">
        <TimeChart
          :data="series.data.value?.[m.name]"
          :type="m.kind === 'counter' ? 'bar' : 'line'"
          :height="170"
          :series="m.kind === 'counter' ? [{ key: 'sum', label: 'Total' }] : [{ key: 'avg', label: 'Avg' }, { key: 'max', label: 'Max' }, { key: 'min', label: 'Min' }]"
        />
      </div>
    </div>
  </div>
</template>
