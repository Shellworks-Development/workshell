<script setup lang="ts">
import { computed } from 'vue'

/*
 * User avatar: the uploaded picture, or a GitHub-style identicon — a
 * horizontally symmetric 5×5 pattern in one color, derived from the name,
 * so the same person always gets the same picture.
 */
const props = withDefaults(defineProps<{ name: string; src?: string; size?: number; square?: boolean }>(), { size: 24 })

// FNV-1a, then a xorshift stream for the pattern bits.
function hashBytes(seed: string): number[] {
  let h = 0x811c9dc5
  for (let i = 0; i < seed.length; i++) {
    h ^= seed.charCodeAt(i)
    h = Math.imul(h, 0x01000193) >>> 0
  }
  const out: number[] = []
  let x = h || 1
  for (let i = 0; i < 20; i++) {
    x ^= x << 13
    x ^= x >>> 17
    x ^= x << 5
    x >>>= 0
    out.push(x & 0xff)
  }
  return out
}

const identicon = computed(() => {
  const b = hashBytes(props.name.trim().toLowerCase() || '?')
  // Muted, GitHub-like colors: any hue, moderate saturation and lightness.
  const hue = Math.round((b[0]! / 255) * 360)
  const sat = 45 + (b[1]! % 20)
  const light = 52 + (b[2]! % 12)
  const cells: { x: number; y: number }[] = []
  for (let y = 0; y < 5; y++) {
    for (let x = 0; x < 3; x++) {
      if (b[3 + y * 3 + x]! % 2 === 0) {
        cells.push({ x, y })
        if (x < 2) cells.push({ x: 4 - x, y })
      }
    }
  }
  return { color: `hsl(${hue} ${sat}% ${light}%)`, cells }
})
</script>

<template>
  <img
    v-if="src"
    :src="src"
    :alt="name"
    class="shrink-0 object-cover"
    :class="square ? 'rounded-md' : 'rounded-full'"
    :style="{ width: `${size}px`, height: `${size}px` }"
  />
  <svg
    v-else
    :viewBox="square ? '-0.6 -0.6 6.2 6.2' : '-1 -1 7 7'"
    shape-rendering="crispEdges"
    class="shrink-0 bg-[#f0f0f0] ring-1 ring-border ring-inset dark:bg-[#e8e8e8]"
    :class="square ? 'rounded-md' : 'rounded-full'"
    :style="{ width: `${size}px`, height: `${size}px` }"
    role="img"
    :aria-label="name"
  >
    <title>{{ name }}</title>
    <rect v-for="(c, i) in identicon.cells" :key="i" :x="c.x" :y="c.y" width="1" height="1" :fill="identicon.color" />
  </svg>
</template>
