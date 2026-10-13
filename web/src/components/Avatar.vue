<script setup lang="ts">
import { computed } from 'vue'
import { hue, initials } from '@/lib/format'

const props = withDefaults(defineProps<{ name: string; src?: string; size?: number; square?: boolean }>(), { size: 24 })
const style = computed(() => {
  const h = hue(props.name)
  return {
    width: `${props.size}px`,
    height: `${props.size}px`,
    fontSize: `${Math.max(9, Math.round(props.size * 0.4))}px`,
    background: `linear-gradient(135deg, hsl(${h} 70% 62%), hsl(${(h + 40) % 360} 75% 48%))`,
  }
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
  <span
    v-else
    class="inline-grid shrink-0 place-items-center font-medium text-white select-none"
    :class="square ? 'rounded-md' : 'rounded-full'"
    :style="style"
    :title="name"
    aria-hidden="true"
  >
    {{ initials(name) }}
  </span>
</template>
