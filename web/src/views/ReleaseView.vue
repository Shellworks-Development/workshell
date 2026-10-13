<script setup lang="ts">
import { ArrowLeft, Bug, ExternalLink, FileCode2, GitBranch, GitCommitHorizontal } from 'lucide-vue-next'
import { ref } from 'vue'
import { RouterLink } from 'vue-router'
import Badge from '@/components/Badge.vue'
import EmptyState from '@/components/EmptyState.vue'
import ErrorBanner from '@/components/ErrorBanner.vue'
import IssueTitle from '@/components/IssueTitle.vue'
import RangePicker from '@/components/RangePicker.vue'
import RelTime from '@/components/RelTime.vue'
import Skeleton from '@/components/Skeleton.vue'
import StatCard from '@/components/StatCard.vue'
import TimeChart from '@/components/TimeChart.vue'
import { api, type Issue, type Release, type Timeseries } from '@/lib/api'
import { duration, num, pct } from '@/lib/format'
import { useProject } from '@/lib/project'
import { useAsync } from '@/lib/useAsync'

const props = defineProps<{ version: string }>()
const { pid } = useProject()
const range = ref('7d')
const { data, error } = useAsync(
  () => api.get<{ release: Release; series: Timeseries; newIssues: Issue[] }>(`/projects/${pid.value}/releases/${encodeURIComponent(props.version)}`, { range: range.value }),
  [pid, () => props.version, range],
)
</script>

<template>
  <RouterLink :to="{ name: 'releases', params: { pid } }" class="mb-4 inline-flex items-center gap-1 text-[13px] text-muted hover:text-fg"><ArrowLeft class="size-3.5" /> Releases</RouterLink>
  <ErrorBanner :message="error" />
  <div v-if="!data && !error" class="space-y-3"><Skeleton w="40%" h="1.6rem" /><Skeleton h="14rem" /></div>

  <template v-if="data">
    <div class="mb-6 border-b border-border pb-5">
      <div class="flex flex-wrap items-center gap-2">
        <h1 class="mono text-2xl font-semibold tracking-tight">{{ data.release.version }}</h1>
        <Badge v-if="data.release.liveServers" tone="success" dot>Live on {{ data.release.liveServers }} servers</Badge>
        <Badge v-if="data.release.hasSourcemap"><FileCode2 class="size-3" /> Sourcemap</Badge>
      </div>
      <div v-if="data.release.commitSha" class="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-[13px] text-muted">
        <span class="text-fg">{{ data.release.message }}</span>
        <span>{{ data.release.author }}</span>
        <span class="flex items-center gap-1"><GitBranch class="size-3.5" />{{ data.release.branch }}</span>
        <a v-if="data.release.commitUrl" :href="data.release.commitUrl" target="_blank" rel="noopener noreferrer" class="mono flex items-center gap-1 hover:text-fg">
          <GitCommitHorizontal class="size-3.5" />{{ data.release.commitSha.slice(0, 7) }}<ExternalLink class="size-3" />
        </a>
        <span v-else class="mono flex items-center gap-1"><GitCommitHorizontal class="size-3.5" />{{ data.release.commitSha.slice(0, 7) }}</span>
      </div>
      <p class="mt-1 text-xs text-faint">
        First seen <RelTime :ts="data.release.firstSeen ?? data.release.createdAt" /><template v-if="data.release.placeVersion"> · place version {{ data.release.placeVersion }}</template>
      </p>
    </div>

    <div class="grid grid-cols-2 gap-3 md:grid-cols-4">
      <StatCard label="Error-free sessions" :value="data.release.errorFreeRate < 0 ? '—' : pct(data.release.errorFreeRate)" :tone="data.release.errorFreeRate >= 0 && data.release.errorFreeRate < 0.95 ? 'danger' : undefined" />
      <StatCard label="Sessions" :value="num(data.release.sessions)" :sub="`${duration(data.release.avgSessionMs)} avg`" />
      <StatCard label="New issues" :value="num(data.release.newIssues)" :tone="data.release.newIssues ? 'danger' : undefined" />
      <StatCard label="Errors" :value="num(data.release.errors)" />
    </div>

    <div class="card mt-4">
      <div class="card-header"><span class="card-title">Sessions & errors</span><RangePicker v-model="range" :options="['24h', '7d', '30d']" /></div>
      <div class="p-3">
        <TimeChart :data="data.series" type="line" :series="[{ key: 'sessions', label: 'Sessions' }, { key: 'errors', label: 'Errors', color: 'var(--danger)', right: true }]" />
      </div>
    </div>

    <section class="mt-6">
      <h2 class="section-title">Issues introduced in this release</h2>
      <div class="card">
        <EmptyState v-if="data.newIssues.length === 0" title="No new issues" :icon="Bug">Nothing first appeared in this release.</EmptyState>
        <RouterLink
          v-for="i in data.newIssues"
          :key="i.id"
          :to="{ name: 'issue', params: { pid, iid: i.id } }"
          class="flex items-center gap-4 border-b border-border px-4 py-3 last:border-0 hover:bg-surface-2"
        >
          <IssueTitle :issue="i" class="flex-1" />
          <span class="tabular text-[13px] font-medium">{{ num(i.eventCount) }}</span>
        </RouterLink>
      </div>
    </section>
  </template>
</template>
