<script setup lang="ts">
defineProps<{ tone?: 'danger' | 'warning' | 'success' | 'info' | 'accent' | 'neutral'; dot?: boolean }>()
</script>

<template>
  <span
    class="inline-flex h-5 items-center gap-1.5 rounded-full px-2 text-[11.5px] font-medium whitespace-nowrap"
    :class="{
      'bg-danger-soft text-danger': tone === 'danger',
      'bg-warning-soft text-warning': tone === 'warning',
      'bg-success-soft text-success': tone === 'success',
      'bg-info-soft text-info': tone === 'info',
      'bg-accent-soft text-accent': tone === 'accent',
      'bg-surface-2 text-muted ring-1 ring-border ring-inset': !tone || tone === 'neutral',
    }"
  >
    <span v-if="dot" class="size-1.5 rounded-full bg-current" />
    <slot />
  </span>
</template>
