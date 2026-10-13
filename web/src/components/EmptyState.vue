<script setup lang="ts">
import type { Component } from 'vue'
defineProps<{ title: string; icon?: Component }>()
</script>

<template>
  <div class="flex flex-col items-center justify-center px-6 py-14 text-center">
    <div v-if="icon" class="mb-3 grid size-10 place-items-center rounded-xl bg-surface-2 text-muted">
      <component :is="icon" class="size-5" />
    </div>
    <div class="text-sm font-medium">{{ title }}</div>
    <div class="mt-1 max-w-sm text-[13px] text-muted"><slot /></div>
  </div>
</template>
