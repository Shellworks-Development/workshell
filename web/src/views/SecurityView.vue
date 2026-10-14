<script setup lang="ts">
import { ShieldAlert, ShieldCheck } from 'lucide-vue-next'
import { computed, ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import Badge from '@/components/Badge.vue'
import EmptyState from '@/components/EmptyState.vue'
import ErrorBanner from '@/components/ErrorBanner.vue'
import PageHeader from '@/components/PageHeader.vue'
import RangePicker from '@/components/RangePicker.vue'
import RelTime from '@/components/RelTime.vue'
import Skeleton from '@/components/Skeleton.vue'
import StatCard from '@/components/StatCard.vue'
import TimeChart from '@/components/TimeChart.vue'
import { api, type Detection, type Timeseries } from '@/lib/api'
import { severityColors } from '@/lib/chartTheme'
import { num } from '@/lib/format'
import { useProject } from '@/lib/project'
import { useAsync } from '@/lib/useAsync'
import SendButton from '@/components/whiteboard/SendButton.vue'
import { detectionSnap } from '@/lib/snapshots'

const { pid } = useProject()
const router = useRouter()
const range = ref('7d')
interface Sec {
  totals: { detections: number; players: number; high: number; automatic: number; actioned: number }
  series: Timeseries
  kinds: { kind: string; count: number; players: number; high: number; lastSeen: number }[]
  offenders: { userId: number; count: number; high: number; kinds: string[]; actions: string[]; lastSeen: number }[]
  recent: Detection[]
  hashedIds: boolean
}
const { data, error } = useAsync(() => api.get<Sec>(`/projects/${pid.value}/security`, { range: range.value }), [pid, range], { refreshMs: 30_000 })
const sevTone = (s: string) => (s === 'high' ? 'danger' : s === 'medium' ? 'warning' : 'neutral')
const series = computed(() => (['low', 'medium', 'high'] as const).map((k) => ({ key: k, label: k, color: severityColors.value[k] })))
</script>

<template>
  <PageHeader title="Security" subtitle="Exploit and anti-cheat detections from Workshell.flag(), protected remotes, and automatic flood detection.">
    <RangePicker v-model="range" :options="['24h', '7d', '30d', '90d']" />
  </PageHeader>
  <ErrorBanner :message="error" />
  <div v-if="!data && !error" class="space-y-3"><Skeleton h="6rem" /><Skeleton h="14rem" /></div>

  <template v-if="data">
    <div class="grid grid-cols-2 gap-3 md:grid-cols-4">
      <StatCard label="Detections" :value="num(data.totals.detections)" />
      <StatCard label="Players flagged" :value="num(data.totals.players)" />
      <StatCard label="High severity" :value="num(data.totals.high)" :tone="data.totals.high ? 'danger' : undefined" />
      <StatCard label="Actioned" :value="num(data.totals.actioned)" :sub="`${num(data.totals.automatic)} detected by Workshell`" />
    </div>

    <div v-if="data.totals.detections === 0" class="card mt-4">
      <EmptyState title="No detections in this range" :icon="ShieldCheck">
        Report detections with <code class="mono">Workshell.flag(player, "speed", { speed = 90 }, { severity = "high", action = "kicked" })</code>,
        or wrap remotes with <code class="mono">Workshell.protect(remote, handler, { rate = 5 })</code> to flag spam and invalid arguments automatically.
      </EmptyState>
    </div>

    <template v-else>
      <div class="card mt-4">
        <div class="card-header"><span class="card-title">Detections by severity</span></div>
        <div class="p-3"><TimeChart :data="data.series" type="bar" stacked :series="series" /></div>
      </div>

      <div class="mt-4 grid gap-4 lg:grid-cols-2">
        <div class="card overflow-x-auto">
          <div class="card-header"><span class="card-title">Detection types</span></div>
          <table class="table">
            <thead><tr><th>Kind</th><th class="text-right">Count</th><th class="text-right">Players</th><th class="text-right">Last</th></tr></thead>
            <tbody>
              <tr v-for="k in data.kinds" :key="k.kind">
                <td><span class="mono">{{ k.kind }}</span> <Badge v-if="k.high" tone="danger">{{ k.high }} high</Badge></td>
                <td class="tabular text-right">{{ num(k.count) }}</td>
                <td class="tabular text-right">{{ num(k.players) }}</td>
                <td class="text-right text-muted"><RelTime :ts="k.lastSeen" /></td>
              </tr>
            </tbody>
          </table>
        </div>
        <div class="card overflow-x-auto">
          <div class="card-header"><span class="card-title">Most flagged players</span></div>
          <table class="table">
            <thead><tr><th>Player</th><th>Detections</th><th>Actions</th><th class="text-right">Last</th></tr></thead>
            <tbody>
              <tr v-for="o in data.offenders" :key="o.userId" class="clickable" @click="router.push({ name: 'player', params: { pid, userId: o.userId } })">
                <td class="mono">{{ o.userId }}</td>
                <td>
                  <span class="tabular">{{ o.count }}</span>
                  <span v-if="o.high" class="text-danger"> ({{ o.high }} high)</span>
                  <div class="truncate text-xs text-faint">{{ o.kinds.join(', ') }}</div>
                </td>
                <td class="text-muted">{{ o.actions.join(', ') || '—' }}</td>
                <td class="text-right text-muted"><RelTime :ts="o.lastSeen" /></td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      <section class="mt-6">
        <h2 class="section-title">Recent detections</h2>
        <div class="card overflow-x-auto">
          <table class="table">
            <thead><tr><th>When</th><th>Kind</th><th>Severity</th><th>Player</th><th>Action</th><th>Details</th><th class="relative w-0"><span class="sr-only">Send to whiteboard</span></th></tr></thead>
            <tbody>
              <tr v-for="d in data.recent" :key="d.id">
                <td class="whitespace-nowrap text-muted"><RelTime :ts="d.ts" /></td>
                <td>
                  <span class="mono">{{ d.kind }}</span>
                  <ShieldAlert v-if="d.source === 'workshell'" class="ml-1 inline size-3.5 text-faint" aria-label="Detected by Workshell" />
                </td>
                <td><Badge :tone="sevTone(d.severity)">{{ d.severity }}</Badge></td>
                <td>
                  <RouterLink v-if="d.userId" :to="d.sessionKey ? { name: 'session', params: { pid, sid: d.sessionKey } } : { name: 'player', params: { pid, userId: d.userId } }" class="mono link">
                    {{ d.userId }}
                  </RouterLink>
                </td>
                <td class="text-muted">{{ d.action || '—' }}</td>
                <td class="mono max-w-sm truncate text-muted">{{ Object.keys(d.details).length ? JSON.stringify(d.details) : '' }}</td>
                <td class="py-1 text-right"><SendButton small icon-only class="btn-ghost text-muted" :build="() => detectionSnap(d, pid)" /></td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </template>
  </template>
</template>
