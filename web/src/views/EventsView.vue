<script setup lang="ts">
import { Filter, Plus, X, Zap } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import EmptyState from '@/components/EmptyState.vue'
import ErrorBanner from '@/components/ErrorBanner.vue'
import PageHeader from '@/components/PageHeader.vue'
import RangePicker from '@/components/RangePicker.vue'
import Select from '@/components/Select.vue'
import TimeChart from '@/components/TimeChart.vue'
import { api, type AnalyticsEvent, type Timeseries } from '@/lib/api'
import { ago, num, pct } from '@/lib/format'
import { useProject } from '@/lib/project'
import { useAsync } from '@/lib/useAsync'

const { pid } = useProject()
const range = ref('7d')

interface EventName {
  name: string
  count: number
  users: number
  valueSum: number
  lastSeen: number
}
const names = useAsync(() => api.get<EventName[]>(`/projects/${pid.value}/events`, { range: range.value }), [pid, range])

const selected = ref<string | null>(null)
watch(names.data, (list) => {
  if (list && (!selected.value || !list.some((n) => n.name === selected.value))) selected.value = list[0]?.name ?? null
})

const measure = ref('count')
const groupBy = ref('')
watch(selected, () => (groupBy.value = ''))

const seriesData = useAsync(
  async () =>
    selected.value
      ? api.get<{ series: Timeseries; breakdown: { value: string; total: number }[]; propKeys: string[] }>(
          `/projects/${pid.value}/events/series`,
          { name: selected.value, measure: measure.value, groupBy: groupBy.value, range: range.value },
        )
      : null,
  [pid, selected, measure, groupBy, range],
)
const recent = useAsync(
  async () => (selected.value ? api.get<AnalyticsEvent[]>(`/projects/${pid.value}/events/recent`, { name: selected.value, limit: 25 }) : []),
  [pid, selected],
)

const chartSeries = computed(() => (seriesData.data.value?.series.series ?? []).map((s) => ({ key: s.name, label: s.name })))
const groupOptions = computed(() => [
  { value: '', label: 'No grouping' },
  ...(seriesData.data.value?.propKeys ?? []).map((k) => ({ value: k, label: `by ${k}` })),
])
const stepOptions = computed(() => [
  { value: '$session', label: '$session (joined)' },
  ...(names.data.value ?? []).map((n) => ({ value: n.name, label: n.name, hint: num(n.count) })),
])
const windowOptions = [
  { value: 1, label: '1 hour' },
  { value: 24, label: '1 day' },
  { value: 168, label: '7 days' },
]
const measures = [
  { value: 'count', label: 'Count' },
  { value: 'users', label: 'Unique players' },
  { value: 'sum', label: 'Sum of value' },
  { value: 'avg', label: 'Avg. value' },
]

// ---- Funnel -----------------------------------------------------------------
const steps = ref<string[]>(['$session', ''])
const windowHours = ref(24)
const funnel = ref<{ steps: string[]; counts: number[] } | null>(null)
const funnelError = ref<string | null>(null)
const funnelRun = ref(0)
async function runFunnel() {
  funnelError.value = null
  try {
    funnel.value = await api.post(`/projects/${pid.value}/events/funnel`, {
      steps: steps.value.filter(Boolean),
      range: range.value,
      windowHours: windowHours.value,
    })
    funnelRun.value++
  } catch (e) {
    funnelError.value = (e as Error).message
  }
}
</script>

<template>
  <PageHeader title="Events" subtitle="Custom analytics events sent with Workshell.track().">
    <RangePicker v-model="range" :options="['24h', '7d', '30d', '90d']" />
  </PageHeader>
  <ErrorBanner :message="names.error.value" />

  <div v-if="names.data.value && names.data.value.length === 0" class="card">
    <EmptyState title="No events yet" :icon="Zap">
      Track gameplay with <code class="mono">Workshell.track("level_up", { level = 5 }, player)</code>. Purchases tracked with
      <code class="mono">Workshell.trackPurchase</code> appear as <code class="mono">$purchase</code>.
    </EmptyState>
  </div>

  <div v-else class="grid gap-3 lg:grid-cols-[18rem_1fr]">
    <div class="card self-start overflow-hidden">
      <div class="card-header"><span class="card-title">Events</span></div>
      <div class="max-h-[32rem] overflow-y-auto">
        <button
          v-for="n in names.data.value"
          :key="n.name"
          class="flex w-full cursor-pointer items-center gap-2 border-b border-border px-4 py-2.5 text-left last:border-0"
          :class="selected === n.name ? 'bg-accent-soft' : 'hover:bg-surface-2'"
          @click="selected = n.name"
        >
          <div class="min-w-0 flex-1">
            <div class="mono truncate font-medium" :class="selected === n.name ? 'text-accent' : ''">{{ n.name }}</div>
            <div class="text-[11px] text-faint">{{ num(n.users) }} players · {{ ago(n.lastSeen) }}</div>
          </div>
          <span class="tabular text-[13px] font-medium">{{ num(n.count) }}</span>
        </button>
      </div>
    </div>

    <div v-if="selected" class="min-w-0 space-y-3">
      <div class="card">
        <div class="card-header flex-wrap">
          <span class="card-title mono">{{ selected }}</span>
          <div class="flex flex-wrap items-center gap-2">
            <Select v-model="measure" :options="measures" label="Measure" />
            <div class="flex items-center gap-1.5 text-xs text-muted">
              <Filter class="size-3.5" />
              <Select v-model="groupBy" :options="groupOptions" label="Group by" />
            </div>
          </div>
        </div>
        <div class="p-3">
          <TimeChart :data="seriesData.data.value?.series" :type="groupBy ? 'bar' : 'area'" :stacked="!!groupBy && measure !== 'avg'" :series="chartSeries" />
        </div>
        <table v-if="groupBy && seriesData.data.value?.breakdown.length" class="table border-t border-border">
          <thead><tr><th>{{ groupBy }}</th><th class="text-right">{{ measures.find((m) => m.value === measure)?.label }}</th></tr></thead>
          <tbody>
            <tr v-for="b in seriesData.data.value.breakdown" :key="b.value">
              <td class="mono">{{ b.value }}</td>
              <td class="tabular text-right">{{ num(b.total) }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <div class="card">
        <div class="card-header"><span class="card-title">Latest</span></div>
        <div class="overflow-x-auto">
          <table class="table">
            <thead><tr><th>When</th><th>Player</th><th class="text-right">Value</th><th>Properties</th></tr></thead>
            <tbody>
              <tr v-for="e in recent.data.value" :key="e.id">
                <td class="whitespace-nowrap text-muted">{{ ago(e.ts) }}</td>
                <td>
                  <RouterLink v-if="e.userId" :to="{ name: 'player', params: { pid, userId: e.userId } }" class="mono text-accent hover:underline">{{ e.userId }}</RouterLink>
                  <span v-else class="text-faint">—</span>
                </td>
                <td class="tabular text-right">{{ e.value === null ? '' : num(e.value) }}</td>
                <td class="mono max-w-md truncate text-muted">{{ Object.keys(e.props).length ? JSON.stringify(e.props) : '' }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>
  </div>

  <div class="card mt-3">
    <div class="card-header">
      <span class="card-title">Funnel</span>
      <span class="text-xs text-muted">Players completing each step in order</span>
    </div>
    <div class="space-y-3 p-4">
      <ErrorBanner :message="funnelError" />
      <div class="flex flex-wrap items-center gap-2">
        <template v-for="(_, i) in steps" :key="i">
          <span v-if="i > 0" class="text-faint">→</span>
          <div class="flex items-center">
            <Select v-model="steps[i]!" :options="stepOptions" placeholder="Pick an event" mono class="max-w-56" :label="`Step ${i + 1}`" />
            <button v-if="steps.length > 2" class="btn btn-ghost size-8 px-0" aria-label="Remove step" @click="steps.splice(i, 1)">
              <X class="size-3.5" />
            </button>
          </div>
        </template>
        <button v-if="steps.length < 10" class="btn btn-sm" @click="steps.push('')"><Plus class="size-3.5" /> Step</button>
      </div>
      <div class="flex flex-wrap items-center gap-2 text-[13px] text-muted">
        within
        <Select v-model="windowHours" :options="windowOptions" label="Window" />
        of the first step
        <button class="btn btn-primary btn-sm ml-2" :disabled="steps.filter(Boolean).length < 2" @click="runFunnel">Calculate</button>
      </div>

      <div v-if="funnel" class="space-y-2 pt-2">
        <div v-for="(s, i) in funnel.steps" :key="i" class="flex items-center gap-3">
          <div class="mono w-40 shrink-0 truncate text-[13px]">{{ i + 1 }}. {{ s }}</div>
          <div class="relative h-8 flex-1 overflow-hidden rounded-md bg-surface-2">
            <div
              :key="funnelRun"
              class="funnel-bar absolute inset-0 origin-left rounded-md bg-accent"
              :style="{ transform: `scaleX(${funnel.counts[i]! / Math.max(1, funnel.counts[0]!)})`, opacity: 1 - i * 0.08, animationDelay: `${i * 50}ms` }"
            />
          </div>
          <div class="w-36 shrink-0 text-right text-[13px]">
            <span class="tabular font-medium">{{ num(funnel.counts[i]) }}</span>
            <span class="text-faint"> · {{ pct(funnel.counts[i]! / Math.max(1, funnel.counts[0]!), 0) }}</span>
            <div v-if="i > 0" class="text-[11px] text-faint">
              {{ pct(funnel.counts[i]! / Math.max(1, funnel.counts[i - 1]!), 0) }} from previous
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* Bars grow from the left when a funnel is calculated: shows each step's share. Rare action. */
.funnel-bar {
  animation: grow 500ms var(--ease-out) backwards;
}
@keyframes grow {
  from {
    transform: scaleX(0.02);
  }
}
@media (prefers-reduced-motion: reduce) {
  .funnel-bar {
    animation: none;
  }
}
</style>
