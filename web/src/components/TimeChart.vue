<script setup lang="ts">
import { BarChart, LineChart } from 'echarts/charts'
import { GridComponent, LegendComponent, TooltipComponent } from 'echarts/components'
import { use } from 'echarts/core'
import { CanvasRenderer } from 'echarts/renderers'
import { computed } from 'vue'
import VChart from 'vue-echarts'
import type { Timeseries } from '@/lib/api'
import { axisColors, palette } from '@/lib/chartTheme'
import { num } from '@/lib/format'
import { dark } from '@/lib/state'

use([LineChart, BarChart, GridComponent, TooltipComponent, LegendComponent, CanvasRenderer])

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
  }>(),
  { type: 'area', stacked: false, height: 220, legend: undefined, daily: false },
)

const fmt = computed(() => props.format ?? num)

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

const option = computed(() => {
  const ts = props.data
  const points = ts?.points ?? []
  const spanDays = points.length > 1 ? (points[points.length - 1]! - points[0]!) / 86_400_000 : 0
  const colors = axisColors.value
  const showLegend = props.legend ?? props.series.length > 1

  return {
    animation: false,
    color: palette.value,
    grid: { left: 4, right: 12, top: showLegend ? 32 : 12, bottom: 4, containLabel: true },
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
      const values = ts?.series.find((x) => x.name === s.key)?.values ?? []
      const color = resolveColor(s.color ?? palette.value[i % palette.value.length]!)
      return {
        name: s.label ?? s.key,
        type: props.type === 'bar' ? 'bar' : 'line',
        data: s.map ? values.map(s.map) : values,
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
      }
    }),
  }
})
</script>

<template>
  <VChart :option="option" :style="{ height: `${height}px` }" autoresize />
</template>
