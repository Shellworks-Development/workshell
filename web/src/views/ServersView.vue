<script setup lang="ts">
import { Server } from 'lucide-vue-next'
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import Badge from '@/components/Badge.vue'
import EmptyState from '@/components/EmptyState.vue'
import ErrorBanner from '@/components/ErrorBanner.vue'
import PageHeader from '@/components/PageHeader.vue'
import RangePicker from '@/components/RangePicker.vue'
import Segmented from '@/components/Segmented.vue'
import StatCard from '@/components/StatCard.vue'
import TimeChart from '@/components/TimeChart.vue'
import { api, seriesOf, type GameServer, type Timeseries } from '@/lib/api'
import { ago, duration, num, shortId, unit } from '@/lib/format'
import { useProject } from '@/lib/project'
import { useAsync } from '@/lib/useAsync'

const { pid } = useProject()
const router = useRouter()
const range = ref('24h')
const state = ref<'live' | 'recent'>('live')

const servers = useAsync(() => api.get<GameServer[]>(`/projects/${pid.value}/servers`, { state: state.value }), [pid, state], {
  refreshMs: 15_000,
})
const stats = useAsync(
  () => api.get<{ health: Timeseries; players: Timeseries }>(`/projects/${pid.value}/servers/stats`, { range: range.value }),
  [pid, range],
  { refreshMs: 60_000 },
)

const live = computed(() => (servers.data.value ?? []).filter((s) => s.live))
const avg = (f: (s: GameServer) => number) => (live.value.length ? live.value.reduce((a, s) => a + f(s), 0) / live.value.length : 0)

// Players come from one series and server counts from another; chart them together.
const playersAndServers = computed<Timeseries | null>(() => {
  const d = stats.data.value
  if (!d) return null
  return { ...d.players, series: [...d.players.series, { name: 'servers', values: seriesOf(d.health, 'servers') }] }
})

function fpsTone(fps: number) {
  return fps >= 55 ? 'text-success' : fps >= 40 ? 'text-warning' : 'text-danger'
}
</script>

<template>
  <PageHeader title="Servers" subtitle="Live game servers and their health, reported every 30 seconds.">
    <RangePicker v-model="range" :options="['1h', '6h', '24h', '7d']" />
  </PageHeader>
  <ErrorBanner :message="servers.error.value || stats.error.value" />

  <div class="grid grid-cols-2 gap-3 md:grid-cols-4">
    <StatCard label="Live servers" :value="num(live.length)" live />
    <StatCard label="Players" :value="num(live.reduce((a, s) => a + s.players, 0))" />
    <StatCard label="Avg. server FPS" :value="num(avg((s) => s.fps))" :sub="`${num(avg((s) => s.heartbeatMs))} ms heartbeat`" />
    <StatCard label="Avg. memory" :value="`${num(avg((s) => s.memoryMb))} MB`" :sub="`${num(avg((s) => s.pingMs))} ms avg ping`" />
  </div>

  <div class="mt-3 grid gap-3 lg:grid-cols-2">
    <div class="card">
      <div class="card-header"><span class="card-title">Players &amp; servers</span></div>
      <div class="p-3">
        <TimeChart
          :data="playersAndServers"
          type="line"
          :height="190"
          :series="[{ key: 'players', label: 'Players', format: unit('players') }, { key: 'servers', label: 'Servers', right: true, format: unit('servers') }]"
        />
      </div>
    </div>
    <div class="card">
      <div class="card-header"><span class="card-title">Server frame rate</span><span class="text-xs text-muted">avg across servers</span></div>
      <div class="p-3"><TimeChart :data="stats.data.value?.health" type="line" :height="190" :series="[{ key: 'fps', label: 'Server FPS', format: unit('fps') }]" /></div>
    </div>
    <div class="card">
      <div class="card-header"><span class="card-title">Heartbeat time</span><span class="text-xs text-muted">time per server frame</span></div>
      <div class="p-3"><TimeChart :data="stats.data.value?.health" type="line" :height="190" :series="[{ key: 'heartbeatMs', label: 'Heartbeat', format: unit('ms') }]" /></div>
    </div>
    <div class="card">
      <div class="card-header"><span class="card-title">Player ping</span><span class="text-xs text-muted">round trip</span></div>
      <div class="p-3"><TimeChart :data="stats.data.value?.health" type="line" :height="190" :series="[{ key: 'pingMs', label: 'Ping', format: unit('ms') }]" /></div>
    </div>
  </div>

  <div class="card mt-3">
    <div class="card-header">
      <span class="card-title">{{ state === 'live' ? 'Live servers' : 'Servers in the last 24h' }}</span>
      <Segmented
        v-model="state"
        :options="[
          { value: 'live', label: 'Live' },
          { value: 'recent', label: 'Recent' },
        ]"
      />
    </div>
    <EmptyState v-if="servers.data.value && servers.data.value.length === 0" title="No servers" :icon="Server">
      {{ state === 'live' ? 'No game servers are reporting right now.' : 'No servers reported in the last 24 hours.' }}
    </EmptyState>
    <div v-else class="overflow-x-auto">
      <table class="table">
        <thead>
          <tr>
            <th>Server</th>
            <th>Release</th>
            <th class="text-right">Players</th>
            <th class="text-right">FPS</th>
            <th class="text-right">Memory</th>
            <th class="text-right">Ping</th>
            <th class="text-right">Uptime</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="s in servers.data.value"
            :key="s.jobId"
            class="clickable"
            @click="router.push({ name: 'server', params: { pid, jobId: s.jobId } })"
          >
            <td>
              <div class="flex items-center gap-2">
                <span class="size-2 shrink-0 rounded-full" :class="s.live ? 'bg-success' : 'bg-surface-3'" />
                <span class="mono">{{ shortId(s.jobId) }}</span>
                <Badge v-if="s.serverType !== 'public'">{{ s.serverType }}</Badge>
              </div>
            </td>
            <td class="mono">{{ s.release || s.placeVersion }}</td>
            <td class="tabular text-right">{{ s.players }}<span class="text-faint">/{{ s.maxPlayers }}</span></td>
            <td class="tabular text-right" :class="s.live ? fpsTone(s.fps) : 'text-faint'">{{ num(s.fps) }}</td>
            <td class="tabular text-right">{{ num(s.memoryMb) }} MB</td>
            <td class="tabular text-right">{{ num(s.pingMs) }} ms</td>
            <td class="tabular text-right text-muted">
              {{ s.live ? duration(Date.now() - s.startedAt) : `closed ${ago(s.closedAt ?? s.lastSeen)}` }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
