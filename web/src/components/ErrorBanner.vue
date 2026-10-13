<script setup lang="ts">
import { CircleAlert } from 'lucide-vue-next'
defineProps<{ message: string | null }>()
</script>

<template>
  <div v-if="message" class="mb-4 flex items-start gap-2 rounded-lg border border-danger/30 bg-danger-soft px-3 py-2.5 text-[13px] text-danger">
    <CircleAlert class="mt-0.5 size-4 shrink-0" />
    <span>{{ message }}</span>
  </div>
</template>
