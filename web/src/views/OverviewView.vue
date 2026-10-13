<script setup lang="ts">
import { Bug, ChevronRight, GitCommitHorizontal, Rocket } from 'lucide-vue-next'
import { RouterLink } from 'vue-router'
import EmptyState from '@/components/EmptyState.vue'
import ErrorBanner from '@/components/ErrorBanner.vue'
import IssueTitle from '@/components/IssueTitle.vue'
import LiveFeed from '@/components/LiveFeed.vue'
import PageHeader from '@/components/PageHeader.vue'
import RangePicker from '@/components/RangePicker.vue'
import RelTime from '@/components/RelTime.vue'
import Skeleton from '@/components/Skeleton.vue'
import Sparkline from '@/components/Sparkline.vue'
import TimeChart from '@/components/TimeChart.vue'
import { api, type Issue, type Timeseries } from '@/lib/api'
import { duration, num, robux } from '@/lib/format'
import { useProject } from '@/lib/project'
import { range } from '@/lib/state'
import { useAsync } from '@/lib/useAsync'

interface Overview {
  livePlayers: number
  liveServers: number
  uniquePlayers: number
  newPlayers: number
  sessions: number
  avgSessionMs: number
  errorEvents: number
  newIssues: number
  openIssues: number
  peakPlayers: number
  revenue: number
  detections: number
  latestRelease: string
  players: Timeseries
  errors: Timeseries
  topIssues: Issue[]
  serverVersions: { version: string; servers: number; players: number }[] | null
}

const { pid, project } = useProject()
const { data, error } = useAsync(() => api.get<Overview>(`/projects/${pid.value}/overview`, { range: range.value }), [pid, range], {
  refreshMs: 30_000,
})
</script>

<template>
  <PageHeader :title="project?.name ?? 'Overview'">
    <RangePicker v-model="range" />
  </PageHeader>
  <ErrorBanner :message="error" />

  <template v-if="!data && !error">
    <div class="card mb-4 grid grid-cols-2 divide-x divide-border md:grid-cols-6">
      <div v-for="i in 6" :key="i" class="space-y-2 p-4"><Skeleton w="50%" h="0.7rem" /><Skeleton w="60%" h="1.4rem" /></div>
    </div>
    <div class="grid gap-4 lg:grid-cols-2"><div v-for="i in 2" :key="i" class="card p-4"><Skeleton h="14rem" /></div></div>
  </template>

  <template v-if="data">
    <div v-if="data.uniquePlayers === 0 && data.liveServers === 0 && data.errorEvents === 0" class="card mb-4 border-dashed">
      <EmptyState title="Waiting for your game" :icon="Rocket">
        No telemetry in this range yet. Add the SDK to your game and data shows up here within seconds.
        <div class="mt-4"><RouterLink :to="{ name: 'settings', params: { pid }, query: { tab: 'sdk' } }" class="btn btn-primary">Set up the SDK</RouterLink></div>
      </EmptyState>
    </div>

    <!-- Metric strip -->
    <div class="card mb-4 grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-6">
      <div class="border-b border-border p-4 sm:border-r lg:border-b-0">
        <div class="flex items-center gap-1.5 text-[13px] text-muted">
          <span class="relative flex size-2">
            <span v-if="data.liveServers" class="absolute inline-flex size-full animate-ping rounded-full bg-success opacity-60" />
            <span class="relative inline-flex size-2 rounded-full" :class="data.liveServers ? 'bg-success' : 'bg-faint/50'" />
          </span>
          Playing now
        </div>
        <div class="tabular mt-1 text-[22px] font-semibold tracking-tight">{{ num(data.livePlayers) }}</div>
        <div class="text-xs text-faint">{{ data.liveServers }} servers</div>
      </div>
      <div class="border-b border-border p-4 lg:border-r lg:border-b-0">
        <div class="text-[13px] text-muted">Players</div>
        <div class="tabular mt-1 text-[22px] font-semibold tracking-tight">{{ num(data.uniquePlayers) }}</div>
        <div class="text-xs text-faint">{{ num(data.newPlayers) }} new · peak {{ num(Math.round(data.peakPlayers)) }}</div>
      </div>
      <div class="border-b border-border p-4 sm:border-r lg:border-b-0">
        <div class="text-[13px] text-muted">Avg. session</div>
        <div class="tabular mt-1 text-[22px] font-semibold tracking-tight">{{ duration(data.avgSessionMs) }}</div>
        <div class="text-xs text-faint">{{ num(data.sessions) }} sessions</div>
      </div>
      <div class="border-b border-border p-4 sm:border-b-0 lg:border-r">
        <div class="text-[13px] text-muted">Errors</div>
        <div class="tabular mt-1 text-[22px] font-semibold tracking-tight" :class="data.errorEvents ? 'text-danger' : ''">{{ num(data.errorEvents) }}</div>
        <div class="text-xs text-faint">{{ num(data.newIssues) }} new issues</div>
      </div>
      <div class="p-4 sm:border-r">
        <div class="text-[13px] text-muted">Exploit detections</div>
        <div class="tabular mt-1 text-[22px] font-semibold tracking-tight" :class="data.detections ? 'text-warning' : ''">{{ num(data.detections) }}</div>
        <RouterLink :to="{ name: 'security', params: { pid } }" class="text-xs text-faint hover:text-fg">View security →</RouterLink>
      </div>
      <div class="p-4">
        <div class="text-[13px] text-muted">Revenue</div>
        <div class="tabular mt-1 text-[22px] font-semibold tracking-tight">{{ robux(data.revenue) }}</div>
        <div class="text-xs text-faint">tracked purchases</div>
      </div>
    </div>

    <div class="grid gap-4 lg:grid-cols-2">
      <div class="card">
        <div class="card-header"><span class="card-title">Concurrent players</span></div>
        <div class="p-3"><TimeChart :data="data.players" :series="[{ key: 'players', label: 'Players' }]" /></div>
      </div>
      <div class="card">
        <div class="card-header"><span class="card-title">Errors</span></div>
        <div class="p-3"><TimeChart :data="data.errors" type="bar" :series="[{ key: 'events', label: 'Errors', color: 'var(--danger)' }]" /></div>
      </div>
    </div>

    <div class="mt-4 grid gap-4 lg:grid-cols-3">
      <div class="space-y-4 lg:col-span-2">
        <div class="card">
          <div class="card-header">
            <span class="card-title">Top unresolved issues</span>
            <RouterLink :to="{ name: 'issues', params: { pid } }" class="flex items-center text-[13px] text-muted hover:text-fg">
              {{ num(data.openIssues) }} open <ChevronRight class="size-3.5" />
            </RouterLink>
          </div>
          <EmptyState v-if="data.topIssues.length === 0" title="No unresolved issues" :icon="Bug">Nothing is on fire in this range.</EmptyState>
          <RouterLink
            v-for="i in data.topIssues"
            :key="i.id"
            :to="{ name: 'issue', params: { pid, iid: i.id } }"
            class="flex items-center gap-4 border-b border-border px-4 py-3 last:border-0 hover:bg-surface-2"
          >
            <IssueTitle :issue="i" class="flex-1" />
            <Sparkline :values="i.spark ?? []" color="var(--danger)" class="hidden shrink-0 sm:block" />
            <div class="w-16 shrink-0 text-right">
              <div class="tabular text-[13px] font-medium">{{ num(i.rangeEvents) }}</div>
              <div class="text-[11px] text-faint"><RelTime :ts="i.lastSeen" /></div>
            </div>
          </RouterLink>
        </div>

        <div class="card">
          <div class="card-header">
            <span class="card-title">Live releases</span>
            <RouterLink :to="{ name: 'releases', params: { pid } }" class="flex items-center text-[13px] text-muted hover:text-fg">All releases <ChevronRight class="size-3.5" /></RouterLink>
          </div>
          <div v-if="!data.serverVersions?.length" class="px-4 py-8 text-center text-[13px] text-muted">No live servers.</div>
          <table v-else class="table">
            <thead><tr><th>Release</th><th class="text-right">Servers</th><th class="text-right">Players</th></tr></thead>
            <tbody>
              <tr v-for="v in data.serverVersions" :key="v.version" class="clickable" @click="$router.push({ name: 'release', params: { pid, version: v.version } })">
                <td>
                  <span class="mono flex items-center gap-1.5"><GitCommitHorizontal class="size-3.5 text-faint" />{{ v.version || '?' }}</span>
                </td>
                <td class="tabular text-right">{{ num(v.servers) }}</td>
                <td class="tabular text-right">{{ num(v.players) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
      <LiveFeed :pid="pid" class="lg:sticky lg:top-16 lg:self-start" />
    </div>
  </template>
</template>
