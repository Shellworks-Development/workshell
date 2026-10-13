<script setup lang="ts">
import { GitCommitHorizontal } from 'lucide-vue-next'
import { useRouter } from 'vue-router'
import EmptyState from '@/components/EmptyState.vue'
import ErrorBanner from '@/components/ErrorBanner.vue'
import PageHeader from '@/components/PageHeader.vue'
import RelTime from '@/components/RelTime.vue'
import Skeleton from '@/components/Skeleton.vue'
import { api, type Release } from '@/lib/api'
import { num, pct } from '@/lib/format'
import { useProject } from '@/lib/project'
import { useAsync } from '@/lib/useAsync'

const { pid } = useProject()
const router = useRouter()
const { data, error } = useAsync(() => api.get<Release[]>(`/projects/${pid.value}/releases`), [pid], { refreshMs: 60_000 })
const rateClass = (r: number) => (r < 0 ? 'text-faint' : r >= 0.99 ? 'text-success' : r >= 0.95 ? 'text-warning' : 'text-danger')
</script>

<template>
  <PageHeader title="Releases" subtitle="Every version your servers have run: what it introduced and how healthy it is." />
  <ErrorBanner :message="error" />
  <div class="card overflow-x-auto">
    <div v-if="!data" class="space-y-3 p-4"><Skeleton v-for="i in 4" :key="i" h="1.4rem" /></div>
    <EmptyState v-else-if="data.length === 0" title="No releases yet" :icon="GitCommitHorizontal">
      Releases appear automatically as servers report their place version. Run <code class="mono">workshell release</code> in CI to attach git commits and a
      Rojo sourcemap.
    </EmptyState>
    <table v-else class="table">
      <thead>
        <tr><th>Release</th><th>Commit</th><th>First seen</th><th class="text-right">Live</th><th class="text-right">Error-free sessions</th><th class="text-right">New issues</th><th class="text-right">Errors</th></tr>
      </thead>
      <tbody>
        <tr v-for="r in data" :key="r.version" class="clickable" @click="router.push({ name: 'release', params: { pid, version: r.version } })">
          <td>
            <div class="flex items-center gap-2">
              <span class="dot" :class="r.liveServers ? 'bg-success' : 'bg-faint/40'" />
              <span class="mono font-medium">{{ r.version }}</span>
            </div>
          </td>
          <td class="max-w-72">
            <div v-if="r.commitSha" class="truncate">
              <span class="text-fg">{{ r.message || r.commitSha.slice(0, 7) }}</span>
              <span class="text-faint"> · {{ r.author }}</span>
            </div>
            <span v-else class="text-faint">place v{{ r.placeVersion || r.version }}</span>
          </td>
          <td class="text-muted"><RelTime :ts="r.firstSeen ?? r.createdAt" /></td>
          <td class="tabular text-right">{{ r.liveServers ? `${num(r.livePlayers)} players` : '—' }}</td>
          <td class="tabular text-right" :class="rateClass(r.errorFreeRate)">{{ r.errorFreeRate < 0 ? '—' : pct(r.errorFreeRate) }}</td>
          <td class="tabular text-right" :class="r.newIssues ? 'text-danger' : 'text-faint'">{{ num(r.newIssues) }}</td>
          <td class="tabular text-right text-muted">{{ num(r.errors) }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
