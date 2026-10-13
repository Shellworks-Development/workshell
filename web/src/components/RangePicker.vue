<script setup lang="ts">
import { computed } from 'vue'
import Segmented from './Segmented.vue'

const model = defineModel<string>({ required: true })
const props = withDefaults(defineProps<{ options?: string[] }>(), { options: () => ['1h', '24h', '7d', '30d'] })
const opts = computed(() => props.options.map((o) => ({ value: o, label: o })))
</script>

<template>
  <Segmented v-model="model" :options="opts" />
</template>
