<script setup lang="ts">
import {
  Activity,
  Bug,
  ExternalLink,
  GitCommitHorizontal,
  Hash,
  LineChart,
  MapPin,
  Network,
  ScrollText,
  Server,
  ShieldAlert,
  Timer,
  User,
  Waves,
} from 'lucide-vue-next'
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import RelTime from '@/components/RelTime.vue'
import TimeChart, { type SeriesSpec } from '@/components/TimeChart.vue'
import type { BoardItem } from '@/lib/api'
import { unit } from '@/lib/format'
import { uid, type ChartNote, type EmbedData } from '@/lib/whiteboard'

const props = defineProps<{ item: BoardItem; active: boolean }>()
const emit = defineEmits<{ patch: [data: Record<string, unknown>] }>()
const picking = defineModel<boolean>('picking', { default: false })

const d = computed(() => props.item.data as unknown as EmbedData)
const icons: Record<string, unknown> = {
  chart: LineChart,
  issue: Bug,
  session: Waves,
  log: ScrollText,
  player: User,
  server: Server,
  release: GitCommitHorizontal,
  detection: ShieldAlert,
  span: Timer,
  trace: Network,
  stat: Hash,
}
const toneBorder: Record<string, string> = {
  danger: 'var(--danger)',
  warning: 'var(--warning)',
  success: 'var(--success)',
  info: 'var(--info)',
  muted: 'var(--border-strong)',
}
// Only in-app paths are linked (the server enforces this too).
const link = computed(() => {
  const l = d.value.link
  return typeof l === 'string' && l.startsWith('/') && !l.startsWith('//') ? l : null
})

// Rebuild chart series from the snapshot (formatters were stored as units or text).
const chartSeries = computed<SeriesSpec[]>(() =>
  (d.value.chart?.series ?? []).map((s) => {
    let format: ((v: number) => string) | undefined
    if (s.unit) format = unit(s.unit)
    else if (s.text) {
      const values = d.value.chart?.data.series.find((x) => x.name === s.key)?.values ?? []
      const byValue = new Map(values.map((v, i) => [v, s.text![i]!]))
      format = (v: number) => byValue.get(v) ?? String(v)
    }
    return { key: s.key, label: s.label, color: s.color, right: s.right, format }
  }),
)
const notes = computed(() => d.value.notes ?? [])
const chartHeight = computed(() => Math.max(60, props.item.h - 44 - (notes.value.length ? 14 + Math.min(notes.value.length, 3) * 20 : 0) - 14))

function addNote(n: Omit<ChartNote, 'id'>) {
  emit('patch', { notes: [...notes.value, { ...n, id: uid() }] })
}
function removeNote(id: ChartNote['id']) {
  emit('patch', { notes: notes.value.filter((n) => n.id !== id) })
}
</script>

<template>
  <div
    class="flex size-full flex-col overflow-hidden rounded-xl border border-border bg-surface shadow-(--shadow-sm)"
    :style="d.tone ? { borderLeft: `3px solid ${toneBorder[d.tone]}` } : undefined"
  >
    <div class="flex h-10 shrink-0 items-center gap-2 border-b border-border px-3">
      <component :is="icons[d.type] ?? Activity" class="size-4 shrink-0 text-muted" />
      <div class="min-w-0 flex-1">
        <div class="truncate text-[13px] leading-tight font-medium">{{ d.title }}</div>
        <div v-if="d.subtitle || d.type !== 'span'" class="truncate text-[10.5px] leading-tight text-faint">
          <template v-if="d.subtitle">{{ d.subtitle }} · </template>
          <template v-if="d.type !== 'span'">captured <RelTime :ts="d.capturedAt" /></template>
        </div>
      </div>
      <button
        v-if="active && d.type === 'chart'"
        data-interactive
        class="btn btn-ghost btn-sm shrink-0 px-2"
        :class="picking && 'bg-accent-soft text-accent'"
        :aria-pressed="picking"
        title="Pin a note to a point on this chart"
        @click="picking = !picking"
      >
        <MapPin class="size-3.5" /> <span class="max-sm:hidden">Note</span>
      </button>
      <RouterLink
        v-if="link"
        data-interactive
        :to="link"
        class="grid size-8 shrink-0 place-items-center rounded-md text-faint hover:bg-surface-2 hover:text-fg"
        title="Open where this came from"
        aria-label="Open original"
      >
        <ExternalLink class="size-3.5" />
      </RouterLink>
    </div>

    <!-- Content only takes pointer input once the card is selected, so dragging
         an unselected card from anywhere moves it. -->
    <div class="min-h-0 flex-1 overflow-hidden" :class="active ? 'pointer-events-auto' : 'pointer-events-none'" :data-interactive="active || undefined">
      <div v-if="d.chart" class="px-2 pt-2">
        <TimeChart
          v-model:picking="picking"
          :data="d.chart.data"
          :series="chartSeries"
          :type="d.chart.type"
          :stacked="d.chart.stacked"
          :daily="d.chart.daily"
          :height="chartHeight"
          :actions="false"
          :notes="notes"
          :title="d.title"
          @add-note="addNote"
          @remove-note="removeNote"
        />
      </div>

      <div v-if="d.bar" class="px-3 pt-2.5">
        <div class="relative h-1.5 rounded-full bg-surface-3">
          <div
            class="absolute inset-y-0 rounded-full"
            :class="d.bar.status === 'error' ? 'bg-danger' : 'bg-accent'"
            :style="{ left: `${d.bar.offset * 100}%`, width: `max(3px, ${d.bar.width * 100}%)` }"
          />
        </div>
      </div>

      <dl v-if="d.fields?.length" class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 px-3 py-2 text-xs">
        <template v-for="[k, v] in d.fields" :key="k">
          <dt class="text-faint">{{ k }}</dt>
          <dd class="truncate text-fg tabular-nums">{{ v }}</dd>
        </template>
      </dl>
      <pre v-if="d.code" class="mx-3 mb-3 overflow-hidden rounded-md bg-surface-2 p-2 font-mono text-[11px] leading-snug whitespace-pre-wrap text-muted">{{ d.code }}</pre>
    </div>
  </div>
</template>
