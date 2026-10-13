<script setup lang="ts">
defineProps<{ label: string; value: string; sub?: string; tone?: 'danger' | 'success' | 'warning' | 'accent'; live?: boolean }>()
</script>

<template>
  <div class="card px-4 py-3.5">
    <div class="flex items-center gap-1.5 text-[13px] text-muted">
      <span v-if="live" class="relative flex size-2">
        <span class="absolute inline-flex size-full animate-ping rounded-full bg-success opacity-60" />
        <span class="relative inline-flex size-2 rounded-full bg-success" />
      </span>
      {{ label }}
    </div>
    <div
      class="tabular mt-1 text-[22px] font-semibold tracking-tight"
      :class="{ 'text-danger': tone === 'danger', 'text-success': tone === 'success', 'text-warning': tone === 'warning', 'text-accent': tone === 'accent' }"
    >
      {{ value }}
    </div>
    <div v-if="sub" class="mt-0.5 truncate text-xs text-faint">{{ sub }}</div>
  </div>
</template>
