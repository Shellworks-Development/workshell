<script setup lang="ts">
import { ArrowLeft, ExternalLink, ScrollText } from 'lucide-vue-next'
import { ref } from 'vue'
import { RouterLink } from 'vue-router'
import Badge from '@/components/Badge.vue'
import CopyButton from '@/components/CopyButton.vue'
import ErrorBanner from '@/components/ErrorBanner.vue'
import RangePicker from '@/components/RangePicker.vue'
import StatCard from '@/components/StatCard.vue'
import TimeChart from '@/components/TimeChart.vue'
import { api, type GameServer, type Timeseries } from '@/lib/api'
import { ago, dateTime, duration, num, unit } from '@/lib/format'
import { useProject } from '@/lib/project'
import { useAsync } from '@/lib/useAsync'
import SendButton from '@/components/whiteboard/SendButton.vue'
import { serverSnap } from '@/lib/snapshots'

const props = defineProps<{ jobId: string }>()
const { pid } = useProject()
const range = ref('6h')

interface ServerDetail {
  server: GameServer
  health: Timeseries
  players: Timeseries
  errorCount: number
  logCount: number
  sessionCount: number
  issues: { id: number; title: string; culprit: string; count: number; lastSeen: number }[]
}

const { data, error } = useAsync(
  () => api.get<ServerDetail>(`/projects/${pid.value}/servers/${encodeURIComponent(props.jobId)}`, { range: range.value }),
  [pid, () => props.jobId, range],
  { refreshMs: 30_000 },
)

function joinUrl(s: GameServer) {
  return `https://www.roblox.com/games/start?placeId=${s.placeId}&gameInstanceId=${encodeURIComponent(s.jobId)}`
}
</script>

<template>
  <RouterLink :to="{ name: 'servers', params: { pid } }" class="mb-3 inline-flex items-center gap-1 text-[13px] text-muted hover:text-fg">
    <ArrowLeft class="size-3.5" /> Servers
  </RouterLink>
  <ErrorBanner :message="error" />

  <template v-if="data">
    <div class="mb-5 flex flex-wrap items-start justify-between gap-3">
      <div class="min-w-0">
        <div class="mb-1 flex items-center gap-2">
          <Badge :tone="data.server.live ? 'success' : 'neutral'">{{ data.server.live ? 'live' : 'closed' }}</Badge>
          <Badge>{{ data.server.serverType }}</Badge>
          <Badge>{{ data.server.environment }}</Badge>
        </div>
        <h1 class="mono text-lg font-semibold break-all">{{ data.server.jobId }}</h1>
        <p class="mt-0.5 text-[13px] text-muted">
          Place {{ data.server.placeId }} · {{ data.server.release || data.server.placeVersion }} · started {{ dateTime(data.server.startedAt) }}
        </p>
      </div>
      <div class="flex flex-wrap gap-2">
        <CopyButton :text="data.server.jobId" label="Copy job id" />
        <SendButton small :build="() => serverSnap(data!.server, data!, pid)" />
        <RouterLink :to="{ name: 'logs', params: { pid }, query: { jobId: data.server.jobId } }" class="btn btn-sm">
          <ScrollText class="size-3.5" /> Logs
        </RouterLink>
        <a v-if="data.server.live && data.server.serverType === 'public'" :href="joinUrl(data.server)" target="_blank" rel="noopener" class="btn btn-sm">
          <ExternalLink class="size-3.5" /> Join
        </a>
      </div>
    </div>

    <div class="grid grid-cols-2 gap-3 md:grid-cols-4">
      <StatCard label="Players" :value="`${data.server.players}/${data.server.maxPlayers}`" :live="data.server.live" :sub="`${num(data.sessionCount)} sessions`" />
      <StatCard label="Server FPS" :value="num(data.server.fps)" :sub="`${num(data.server.heartbeatMs)} ms heartbeat`" />
      <StatCard label="Memory" :value="`${num(data.server.memoryMb)} MB`" :sub="`${num(data.server.pingMs)} ms avg ping`" />
      <StatCard
        label="Uptime"
        :value="duration((data.server.live ? Date.now() : (data.server.closedAt ?? data.server.lastSeen)) - data.server.startedAt)"
        :sub="`last heartbeat ${ago(data.server.lastSeen)}`"
      />
    </div>

    <div class="mt-3 mb-3 flex justify-end"><RangePicker v-model="range" :options="['1h', '6h', '24h', '7d']" /></div>

    <div class="grid gap-3 lg:grid-cols-2">
      <div class="card">
        <div class="card-header"><span class="card-title">Players</span></div>
        <div class="p-3"><TimeChart :data="data.players" :height="170" :series="[{ key: 'players', label: 'Players', format: unit('players') }]" /></div>
      </div>
      <div class="card">
        <div class="card-header"><span class="card-title">Frame rate</span></div>
        <div class="p-3"><TimeChart :data="data.health" type="line" :height="170" :series="[{ key: 'fps', label: 'FPS', format: unit('fps') }]" /></div>
      </div>
      <div class="card">
        <div class="card-header"><span class="card-title">Heartbeat time</span></div>
        <div class="p-3"><TimeChart :data="data.health" type="line" :height="170" :series="[{ key: 'heartbeatMs', label: 'Heartbeat', format: unit('ms') }]" /></div>
      </div>
      <div class="card">
        <div class="card-header"><span class="card-title">Memory &amp; instances</span></div>
        <div class="p-3">
          <TimeChart
            :data="data.health"
            type="line"
            :height="170"
            :series="[{ key: 'memoryMb', label: 'Memory', format: unit('MB') }, { key: 'instances', label: 'Instances', right: true, format: unit('inst.') }]"
          />
        </div>
      </div>
      <div class="card">
        <div class="card-header"><span class="card-title">Network</span></div>
        <div class="p-3">
          <TimeChart
            :data="data.health"
            type="line"
            :height="170"
            :series="[{ key: 'sendKbps', label: 'Sent', format: unit('KB/s') }, { key: 'recvKbps', label: 'Received', format: unit('KB/s') }]"
          />
        </div>
      </div>
      <div class="card">
        <div class="card-header"><span class="card-title">Player ping</span></div>
        <div class="p-3"><TimeChart :data="data.health" type="line" :height="170" :series="[{ key: 'pingMs', label: 'Ping', format: unit('ms') }]" /></div>
      </div>
    </div>

    <div class="card mt-3">
      <div class="card-header">
        <span class="card-title">Errors on this server</span>
        <span class="tabular text-xs text-muted">{{ num(data.errorCount) }} events</span>
      </div>
      <div v-if="data.issues.length === 0" class="px-4 py-8 text-center text-[13px] text-muted">No errors from this server.</div>
      <RouterLink
        v-for="i in data.issues"
        :key="i.id"
        :to="{ name: 'issue', params: { pid, iid: i.id } }"
        class="flex items-center gap-4 border-b border-border px-4 py-2.5 last:border-0 hover:bg-surface-2"
      >
        <div class="min-w-0 flex-1">
          <div class="truncate font-medium">{{ i.title }}</div>
          <div class="mono truncate text-muted">{{ i.culprit }}</div>
        </div>
        <div class="tabular text-right">
          <div class="font-medium">{{ num(i.count) }}</div>
          <div class="text-[11px] text-faint">{{ ago(i.lastSeen) }}</div>
        </div>
      </RouterLink>
    </div>
  </template>
</template>
