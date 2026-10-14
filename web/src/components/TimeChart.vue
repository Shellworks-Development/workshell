<script setup lang="ts">
import { BarChart, LineChart } from 'echarts/charts'
import { GridComponent, LegendComponent, MarkLineComponent, MarkPointComponent, TooltipComponent } from 'echarts/components'
import { use } from 'echarts/core'
import { CanvasRenderer } from 'echarts/renderers'
import { MapPin, MoreHorizontal, Presentation, X } from 'lucide-vue-next'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import VChart from 'vue-echarts'
import { useRoute } from 'vue-router'
import { addChartNote, chartNotes, removeChartNote } from '@/lib/annotations'
import type { Timeseries } from '@/lib/api'
import { axisColors, palette } from '@/lib/chartTheme'
import { dateTime, num } from '@/lib/format'
import { toast } from '@/lib/overlay'
import { useMaybeProject } from '@/lib/project'
import { auth, dark } from '@/lib/state'
import { chartSnapshot, here, sendToWhiteboard, type ChartNote } from '@/lib/whiteboard'
import Popover from './Popover.vue'

use([LineChart, BarChart, GridComponent, TooltipComponent, LegendComponent, MarkPointComponent, MarkLineComponent, CanvasRenderer])

export interface SeriesSpec {
  /** Series name in the Timeseries payload. */
  key: string
  label?: string
  color?: string
  /** Optional transform of each value. */
  map?: (v: number) => number
  /** Plot against a second y-axis on the right (for series with a different scale). */
  right?: boolean
  /** Formatter for this series' values (tooltip and its axis). */
  format?: (v: number) => string
}

const props = withDefaults(
  defineProps<{
    data: Timeseries | null | undefined
    series: SeriesSpec[]
    type?: 'line' | 'area' | 'bar'
    stacked?: boolean
    height?: number
    format?: (v: number) => string
    legend?: boolean
    daily?: boolean
    /** Name used for notes and whiteboard snapshots (defaults to the card title). */
    title?: string
    /** Show the chart menu (notes, Send to whiteboard). */
    actions?: boolean
    /** Notes managed by the parent (whiteboard charts); otherwise they're loaded per chart. */
    notes?: ChartNote[]
  }>(),
  { type: 'area', stacked: false, height: 220, legend: undefined, daily: false, actions: true, notes: undefined, title: undefined },
)
const emit = defineEmits<{ addNote: [note: Omit<ChartNote, 'id'>]; removeNote: [id: ChartNote['id']] }>()
/** True while waiting for a click on the chart to pin a note. */
const picking = defineModel<boolean>('picking', { default: false })

const fmt = computed(() => props.format ?? num)
const route = useRoute()
const project = useMaybeProject()

// ---- Where the chart sits: its card header hosts the menu ---------------------
const root = ref<HTMLElement | null>(null)
const chart = ref<InstanceType<typeof VChart> | null>(null)
const headerEl = ref<HTMLElement | null>(null)
const cardTitle = ref('')
const cardSubtitle = ref('')
onMounted(() => {
  const card = root.value?.closest('.card')
  const header = card?.querySelector<HTMLElement>(':scope > .card-header')
  if (props.actions && header) headerEl.value = header
  cardTitle.value = header?.querySelector('.card-title')?.textContent?.trim() ?? ''
  cardSubtitle.value = header?.querySelector('.card-title + span')?.textContent?.trim() ?? ''
})
const chartTitle = computed(() => props.title ?? (cardTitle.value || props.series.map((s) => s.label ?? s.key).join(' & ')))

// ---- Notes ----------------------------------------------------------------------
// Dashboard charts store notes per chart key: the page, its params (a server's
// chart is different from another server's), and the chart's title.
const noteKey = computed(() => {
  if (props.notes !== undefined || !props.actions || !project || !chartTitle.value) return null
  const params = Object.entries(route.params)
    .filter(([k]) => k !== 'pid')
    .map(([k, v]) => `${k}=${String(v)}`)
    .join('&')
  return `${String(route.name)}${params ? `?${params}` : ''}:${chartTitle.value}`.slice(0, 200)
})
const loadedNotes = computed<ChartNote[]>(() => (noteKey.value && project ? chartNotes(project.pid.value, noteKey.value) : []))
const allNotes = computed(() => [...(props.notes ?? loadedNotes.value)].sort((a, b) => a.ts - b.ts))

/** Index of the bucket a timestamp falls in, or -1 if off the chart. */
function indexOf(ts: number): number {
  const d = props.data
  if (!d?.points.length) return -1
  const i = Math.floor((ts - d.points[0]!) / d.bucket)
  return i >= 0 && i < d.points.length ? i : -1
}
const visibleNotes = computed(() => allNotes.value.map((n, i) => ({ n, num: i + 1, idx: indexOf(n.ts) })))

// ---- Chart option ---------------------------------------------------------------

// ECharts paints to a canvas, which can't resolve CSS variables: look them up.
function resolveColor(c: string): string {
  const m = /^var\((--[\w-]+)\)$/.exec(c.trim())
  if (!m) return c
  void dark.value // re-resolve when the theme changes
  return getComputedStyle(document.documentElement).getPropertyValue(m[1]!).trim() || c
}

function label(ts: number, withDate: boolean) {
  const d = new Date(ts)
  if (props.daily) return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric', timeZone: 'UTC' })
  const time = d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
  return withDate ? `${d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })} ${time}` : time
}

function seriesValues(key: string, map?: (v: number) => number) {
  const values = props.data?.series.find((x) => x.name === key)?.values ?? []
  return map ? values.map(map) : values
}

const option = computed(() => {
  const ts = props.data
  const points = ts?.points ?? []
  const spanDays = points.length > 1 ? (points[points.length - 1]! - points[0]!) / 86_400_000 : 0
  const colors = axisColors.value
  const showLegend = props.legend ?? props.series.length > 1
  const pin = resolveColor('var(--accent)')
  const pinText = resolveColor('var(--accent-fg)')

  return {
    animation: false,
    color: palette.value,
    grid: { left: 4, right: 12, top: showLegend ? 32 : allNotes.value.length ? 26 : 12, bottom: 4, containLabel: true },
    legend: showLegend
      ? { top: 0, left: 0, icon: 'roundRect', itemWidth: 10, itemHeight: 10, textStyle: { color: colors.text, fontSize: 11 } }
      : undefined,
    tooltip: {
      trigger: 'axis',
      backgroundColor: colors.tooltipBg,
      borderColor: colors.tooltipBorder,
      textStyle: { color: colors.tooltipText, fontSize: 12 },
      axisPointer: { type: props.type === 'bar' ? 'shadow' : 'line', lineStyle: { color: colors.tooltipBorder } },
      valueFormatter: (v: number) => fmt.value(v),
    },
    xAxis: {
      type: 'category',
      data: points.map((p) => label(p, spanDays >= 1 && !props.daily)),
      boundaryGap: props.type === 'bar',
      axisLine: { lineStyle: { color: colors.line } },
      axisTick: { show: false },
      axisLabel: { color: colors.text, fontSize: 11, hideOverlap: true },
    },
    yAxis: [false, true]
      .filter((right) => !right || props.series.some((s) => s.right))
      .map((right) => {
        const f = props.series.find((s) => !!s.right === right)?.format ?? fmt.value
        return {
          type: 'value',
          position: right ? 'right' : 'left',
          splitLine: { show: !right, lineStyle: { color: colors.line } },
          axisLabel: { color: colors.text, fontSize: 11, formatter: (v: number) => f(v) },
        }
      }),
    series: props.series.map((s, i) => {
      const values = seriesValues(s.key, s.map)
      const color = resolveColor(s.color ?? palette.value[i % palette.value.length]!)
      // Notes: a pin at the noted value on this series, and a faint line at its time.
      const mine = visibleNotes.value.filter((v) => v.idx >= 0 && (v.n.series === s.key || (i === 0 && !props.series.some((x) => x.key === v.n.series))))
      return {
        name: s.label ?? s.key,
        type: props.type === 'bar' ? 'bar' : 'line',
        data: values,
        stack: props.stacked ? 'total' : undefined,
        yAxisIndex: s.right ? 1 : 0,
        tooltip: s.format ? { valueFormatter: s.format } : undefined,
        showSymbol: false,
        smooth: false,
        barMaxWidth: 18,
        itemStyle: { color, borderRadius: props.type === 'bar' && !props.stacked ? [3, 3, 0, 0] : 0 },
        lineStyle: { width: 1.75, color },
        areaStyle: props.type === 'area' ? { color, opacity: props.series.length > 1 ? 0.08 : 0.12 } : undefined,
        emphasis: { focus: 'series' },
        markPoint: mine.length
          ? {
              silent: false,
              symbol: 'pin',
              symbolSize: 30,
              itemStyle: { color: pin },
              label: { color: pinText, fontSize: 10, fontWeight: 600, formatter: (p: { value: number }) => String(p.value) },
              tooltip: { show: true, formatter: (p: { data: { note: string } }) => p.data.note },
              data: mine.map((v) => ({ coord: [v.idx, v.n.value ?? values[v.idx] ?? 0], value: v.num, note: noteTooltip(v.n) })),
            }
          : undefined,
        markLine: mine.length
          ? {
              silent: true,
              symbol: 'none',
              label: { show: false },
              lineStyle: { color: pin, type: 'dashed', width: 1, opacity: 0.5 },
              data: mine.map((v) => ({ xAxis: v.idx })),
            }
          : undefined,
      }
    }),
  }
})

function noteTooltip(n: ChartNote) {
  // ECharts tooltips are HTML: escape the user's text.
  const esc = (s: string) => s.replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`)
  return `<div style="max-width:16rem;white-space:normal"><b>${esc(n.text)}</b><br><span style="opacity:.7">${esc(n.author?.name ?? '')} · ${esc(dateTime(n.ts))}</span></div>`
}

// ---- Pinning a note: click a point, type, save ------------------------------------
const draft = ref<{ ts: number; value: number | null; series: string; left: number; top: number } | null>(null)
const draftText = ref('')
const draftInput = ref<HTMLTextAreaElement | null>(null)

function onZrClick(e: { offsetX: number; offsetY: number }) {
  if (!picking.value || !chart.value || !props.data?.points.length) return
  const c = chart.value as unknown as {
    convertFromPixel: (f: object, p: number[]) => number[]
    convertToPixel: (f: object, p: number[]) => number[]
  }
  const at = c.convertFromPixel({ gridIndex: 0 }, [e.offsetX, e.offsetY])
  if (!at || !Number.isFinite(at[0])) return
  const idx = Math.max(0, Math.min(props.data.points.length - 1, Math.round(at[0]!)))
  // Snap to whichever series' point is closest to where the user clicked.
  let best: { key: string; value: number; d: number } | null = null
  for (const s of props.series) {
    const v = seriesValues(s.key, s.map)[idx]
    if (v === undefined) continue
    const px = c.convertToPixel({ xAxisIndex: 0, yAxisIndex: s.right ? 1 : 0 }, [idx, v])
    const d = Math.abs((px?.[1] ?? 0) - e.offsetY)
    if (!best || d < best.d) best = { key: s.key, value: v, d }
  }
  const width = root.value?.clientWidth ?? 300
  draft.value = {
    ts: props.data.points[idx]!,
    value: best?.value ?? null,
    series: best?.key ?? props.series[0]?.key ?? '',
    left: Math.max(8, Math.min(e.offsetX - 120, width - 248)),
    top: Math.min(e.offsetY + 12, props.height - 20),
  }
  draftText.value = ''
  picking.value = false
  nextTick(() => draftInput.value?.focus())
}

async function saveDraft() {
  const d = draft.value
  const text = draftText.value.trim()
  if (!d || !text) return
  const note = { ts: d.ts, value: d.value, series: d.series, text }
  if (props.notes !== undefined) {
    emit('addNote', { ...note, author: auth.user ? { name: auth.user.name } : null, createdAt: Date.now() })
  } else if (noteKey.value && project) {
    try {
      await addChartNote(project.pid.value, noteKey.value, note)
    } catch (e) {
      toast.error('Could not save note', (e as Error).message)
      return
    }
  }
  draft.value = null
}

async function removeNote(n: ChartNote) {
  if (props.notes !== undefined) return emit('removeNote', n.id)
  if (!noteKey.value || !project) return
  try {
    await removeChartNote(project.pid.value, noteKey.value, Number(n.id))
  } catch (e) {
    toast.error('Could not delete note', (e as Error).message)
  }
}

function onKey(e: KeyboardEvent) {
  if (e.key !== 'Escape') return
  if (picking.value) picking.value = false
  else if (draft.value) draft.value = null
}
watch(picking, (p) => {
  if (p) draft.value = null
})
onMounted(() => document.addEventListener('keydown', onKey))
onBeforeUnmount(() => document.removeEventListener('keydown', onKey))

// ---- Menu -------------------------------------------------------------------------
const menuOpen = ref(false)
function startNote() {
  menuOpen.value = false
  picking.value = true
}
function send() {
  menuOpen.value = false
  if (!project || !props.data) return
  sendToWhiteboard(project.pid.value, {
    type: 'chart',
    title: chartTitle.value,
    subtitle: cardSubtitle.value || undefined,
    link: here(),
    capturedAt: Date.now(),
    chart: chartSnapshot(props.data, props.series, { type: props.type, stacked: props.stacked, daily: props.daily, format: props.format }),
    notes: allNotes.value.map((n) => ({ id: String(n.id), ts: n.ts, value: n.value, series: n.series, text: n.text, author: n.author ? { name: n.author.name } : null, createdAt: n.createdAt })),
  })
}
const canDelete = (n: ChartNote) => props.notes !== undefined || auth.user?.isAdmin || (n as { author?: { id?: number } }).author?.id === auth.user?.id
</script>

<template>
  <div ref="root" class="relative" :class="picking && 'cursor-crosshair'">
    <Teleport v-if="headerEl && project" :to="headerEl">
      <div class="-my-1 -mr-2 ml-auto shrink-0">
        <Popover v-model:open="menuOpen" placement="bottom-end" width="15rem">
          <template #trigger="{ toggle }">
            <button class="btn btn-ghost btn-icon btn-sm text-muted" aria-label="Chart actions" :aria-expanded="menuOpen" @click="toggle">
              <MoreHorizontal class="size-4" />
            </button>
          </template>
          <div class="p-1">
            <button class="menu-item" :disabled="!data" @click="startNote"><MapPin class="size-4 text-muted" /> Pin a note</button>
            <button class="menu-item" :disabled="!data" @click="send"><Presentation class="size-4 text-muted" /> Send to whiteboard</button>
          </div>
        </Popover>
      </div>
    </Teleport>

    <VChart ref="chart" :option="option" :style="{ height: `${height}px` }" autoresize @zr:click="onZrClick" />

    <div
      v-if="picking"
      class="pointer-events-none absolute inset-x-0 top-0 flex justify-center"
      role="status"
    >
      <span class="rounded-full border border-border bg-surface px-2.5 py-1 text-xs text-muted shadow-(--shadow-pop)">Click a point to pin a note · Esc to cancel</span>
    </div>

    <form
      v-if="draft"
      class="pop-in absolute z-10 w-60 rounded-lg border border-border bg-surface p-2 shadow-(--shadow-pop)"
      :style="{ left: `${draft.left}px`, top: `${draft.top}px` }"
      @submit.prevent="saveDraft"
      @pointerdown.stop
    >
      <div class="mb-1.5 text-[11px] text-faint">{{ dateTime(draft.ts) }}<template v-if="draft.value !== null"> · {{ fmt(draft.value) }}</template></div>
      <textarea
        ref="draftInput"
        v-model="draftText"
        class="input min-h-16 resize-none"
        maxlength="500"
        placeholder="What happened here?"
        @keydown.enter.exact.prevent="saveDraft"
      />
      <div class="mt-1.5 flex justify-end gap-1.5">
        <button type="button" class="btn btn-sm" @click="draft = null">Cancel</button>
        <button type="submit" class="btn btn-sm btn-primary" :disabled="!draftText.trim()">Pin note</button>
      </div>
    </form>

    <ul v-if="allNotes.length" class="mt-2 space-y-1 border-t border-border pt-2 text-xs">
      <li v-for="v in visibleNotes" :key="v.n.id" class="group flex items-start gap-2">
        <span class="mt-px grid size-4 shrink-0 place-items-center rounded-full bg-accent text-[10px] font-semibold text-accent-fg">{{ v.num }}</span>
        <div class="min-w-0 flex-1">
          <span class="break-words text-fg">{{ v.n.text }}</span>
          <span class="text-faint"> · {{ dateTime(v.n.ts) }}<template v-if="v.n.author"> · {{ v.n.author.name }}</template><template v-if="v.idx < 0"> · outside this range</template></span>
        </div>
        <button
          v-if="canDelete(v.n)"
          class="-my-1 grid size-6 shrink-0 place-items-center rounded text-faint hover:bg-surface-2 hover:text-fg"
          aria-label="Delete note"
          @click="removeNote(v.n)"
        >
          <X class="size-3.5" />
        </button>
      </li>
    </ul>
  </div>
</template>
