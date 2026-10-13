<script setup lang="ts">
import Logo from './Logo.vue'
defineProps<{ title: string; subtitle?: string }>()
</script>

<template>
  <div class="grid min-h-dvh place-items-center px-4 py-10">
    <div class="stagger w-full max-w-sm">
      <div class="mb-6 flex items-center gap-2.5">
        <Logo class="size-8" />
        <span class="text-lg font-semibold tracking-tight">Workshell</span>
      </div>
      <div class="card p-6">
        <h1 class="text-lg font-semibold tracking-tight">{{ title }}</h1>
        <p v-if="subtitle" class="mt-1 text-[13px] text-muted">{{ subtitle }}</p>
        <div class="mt-5"><slot /></div>
      </div>
    </div>
  </div>
</template>
