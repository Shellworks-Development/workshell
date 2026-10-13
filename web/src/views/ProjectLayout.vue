<script setup lang="ts">
import { FolderX } from 'lucide-vue-next'
import { ref, toRef, watch } from 'vue'
import { RouterLink, RouterView, useRoute } from 'vue-router'
import AppShell from '@/components/AppShell.vue'
import EmptyState from '@/components/EmptyState.vue'
import { api, type Project } from '@/lib/api'
import { provideProject } from '@/lib/project'

const props = defineProps<{ pid: number }>()
const route = useRoute()
const project = ref<Project | null>(null)
const notFound = ref(false)

async function reload() {
  try {
    project.value = await api.get<Project>(`/projects/${props.pid}`)
    notFound.value = false
  } catch {
    notFound.value = true
  }
}

watch(
  () => props.pid,
  () => {
    project.value = null
    reload()
  },
  { immediate: true },
)

provideProject({ pid: toRef(props, 'pid'), project, reload })
</script>

<template>
  <AppShell :pid="pid" :project-name="project?.name" :crumb="route.name === 'overview' ? undefined : String(route.meta.title ?? '')">
    <div v-if="notFound" class="card">
      <EmptyState title="Project not found" :icon="FolderX">
        It may have been deleted.
        <div class="mt-4"><RouterLink to="/" class="btn">Back to projects</RouterLink></div>
      </EmptyState>
    </div>
    <RouterView v-else-if="project" v-slot="{ Component, route: r }">
      <div :key="r.path" class="page-enter">
        <component :is="Component" />
      </div>
    </RouterView>
  </AppShell>
</template>
