<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { absolute, ago, dateTime } from '@/lib/format'
import { prefs } from '@/lib/state'

const props = defineProps<{ ts: number | null | undefined; absoluteOnly?: boolean }>()
const now = ref(Date.now())
const timer = setInterval(() => (now.value = Date.now()), 30_000)
onBeforeUnmount(() => clearInterval(timer))

const text = computed(() => {
  if (!props.ts) return 'never'
  return props.absoluteOnly || prefs.timeFormat === 'absolute' ? dateTime(props.ts) : ago(props.ts, now.value)
})
</script>

<template>
  <time :datetime="ts ? new Date(ts).toISOString() : undefined" :title="ts ? absolute(ts) : undefined" class="tabular">{{ text }}</time>
</template>
