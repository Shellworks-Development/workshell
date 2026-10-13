<script setup lang="ts">
import { computed, useId } from 'vue'

const props = withDefaults(defineProps<{ values: number[]; width?: number; height?: number; color?: string; kind?: 'bars' | 'area' }>(), {
  width: 96,
  height: 24,
  color: 'var(--accent)',
  kind: undefined,
})
const id = useId()
const mode = computed(() => props.kind ?? (props.width > 150 ? 'area' : 'bars'))

const bars = computed(() => {
  const max = Math.max(1, ...props.values)
  const n = props.values.length || 1
  const w = props.width / n
  return props.values.map((v, i) => {
    const h = v > 0 ? Math.max(2, (v / max) * props.height) : 1
    return { x: i * w + 0.5, y: props.height - h, w: Math.max(1, w - 1), h, empty: v === 0 }
  })
})

const area = computed(() => {
  const v = props.values.length ? props.values : [0]
  const max = Math.max(1, ...v)
  const step = props.width / Math.max(v.length - 1, 1)
  const pts = v.map((x, i) => [i * step, props.height - 1 - (x / max) * (props.height - 3)] as const)
  const line = pts.map(([x, y], i) => `${i ? 'L' : 'M'}${x.toFixed(1)},${y.toFixed(1)}`).join('')
  return { line, fill: `${line}L${props.width},${props.height}L0,${props.height}Z` }
})
</script>

<template>
  <svg :viewBox="`0 0 ${width} ${height}`" :width="mode === 'area' ? undefined : width" :height="height" preserveAspectRatio="none" aria-hidden="true">
    <template v-if="mode === 'bars'">
      <rect v-for="(b, i) in bars" :key="i" :x="b.x" :y="b.y" :width="b.w" :height="b.h" rx="1" :fill="b.empty ? 'var(--surface-3)' : color" />
    </template>
    <template v-else>
      <defs>
        <linearGradient :id="id" x1="0" x2="0" y1="0" y2="1">
          <stop offset="0%" :stop-color="color" stop-opacity="0.22" />
          <stop offset="100%" :stop-color="color" stop-opacity="0" />
        </linearGradient>
      </defs>
      <path :d="area.fill" :fill="`url(#${id})`" />
      <path :d="area.line" fill="none" :stroke="color" stroke-width="1.5" vector-effect="non-scaling-stroke" stroke-linejoin="round" />
    </template>
  </svg>
</template>
