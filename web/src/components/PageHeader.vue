<script setup lang="ts">
defineProps<{ title: string; subtitle?: string }>()
</script>

<template>
  <div class="mb-6 flex flex-wrap items-end justify-between gap-x-4 gap-y-3">
    <div class="min-w-0">
      <slot name="before" />
      <h1 class="truncate text-2xl font-semibold tracking-tight">{{ title }}</h1>
      <p v-if="subtitle" class="mt-1 text-sm text-muted">{{ subtitle }}</p>
    </div>
    <div class="flex flex-wrap items-center gap-2"><slot /></div>
  </div>
</template>
