<script setup lang="ts">
import { RouterView, useRoute } from 'vue-router'
import AppShell from '@/components/AppShell.vue'

const route = useRoute()
</script>

<template>
  <AppShell :crumb="route.name === 'projects' ? undefined : String(route.meta.title ?? '')">
    <RouterView v-slot="{ Component, route: r }">
      <div :key="r.path" class="page-enter">
        <component :is="Component" />
      </div>
    </RouterView>
  </AppShell>
</template>
