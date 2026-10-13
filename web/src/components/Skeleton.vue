<script setup lang="ts">
defineProps<{ h?: string; w?: string }>()
</script>

<template>
  <div class="skeleton" :style="{ height: h ?? '1rem', width: w ?? '100%' }" aria-hidden="true" />
</template>
