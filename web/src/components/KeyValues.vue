<script setup lang="ts">
import { computed } from 'vue'
const props = defineProps<{ data: Record<string, unknown> | null | undefined }>()
const entries = computed(() =>
  Object.entries(props.data ?? {}).map(([k, v]) => [k, typeof v === 'object' && v !== null ? JSON.stringify(v) : String(v)] as const),
)
</script>

<template>
  <dl v-if="entries.length" class="grid grid-cols-[minmax(0,auto)_1fr] gap-x-4 gap-y-1.5 text-[13px]">
    <template v-for="[k, v] in entries" :key="k">
      <dt class="mono truncate text-muted">{{ k }}</dt>
      <dd class="mono min-w-0 break-all">{{ v }}</dd>
    </template>
  </dl>
  <div v-else class="text-[13px] text-faint">None</div>
</template>
