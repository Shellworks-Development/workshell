<script setup lang="ts">
import { Search } from 'lucide-vue-next'
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import ErrorBanner from '@/components/ErrorBanner.vue'
import PageHeader from '@/components/PageHeader.vue'
import RangePicker from '@/components/RangePicker.vue'
import Skeleton from '@/components/Skeleton.vue'
import StatCard from '@/components/StatCard.vue'
import TimeChart from '@/components/TimeChart.vue'
import { api, type Timeseries } from '@/lib/api'
import { day, duration, num, pct, robux } from '@/lib/format'
import { useProject } from '@/lib/project'
import { useAsync } from '@/lib/useAsync'

const { pid } = useProject()
const router = useRouter()
const range = ref('30d')

interface PlayerStats {
  series: Timeseries
  summary: {
    uniquePlayers: number
    newPlayers: number
    sessions: number
    avgSessionMs: number
    playtimeMs: number
    revenue: number
    payers: number
    totalPlayers: number
  }
}
interface Retention {
  offsets: number[]
  cohorts: { day: number; size: number; retained: (number | null)[] }[]
}
interface BreakdownRow {
  value: string
  players: number
  sessions: number
  avgSessionMs: number
}

const players = useAsync(() => api.get<PlayerStats>(`/projects/${pid.value}/analytics/players`, { range: range.value }), [pid, range])
const retention = useAsync(() => api.get<Retention>(`/projects/${pid.value}/analytics/retention`, { range: '14d' }), [pid])
const platforms = useAsync(() => api.get<BreakdownRow[]>(`/projects/${pid.value}/analytics/breakdown`, { by: 'platform', range: range.value }), [pid, range])
const countries = useAsync(() => api.get<BreakdownRow[]>(`/projects/${pid.value}/analytics/breakdown`, { by: 'country', range: range.value }), [pid, range])

const lookup = ref('')
function findPlayer() {
  const id = lookup.value.trim()
  if (/^\d+$/.test(id)) router.push({ name: 'player', params: { pid: pid.value, userId: id } })
}

function share(rows: BreakdownRow[] | null, r: BreakdownRow) {
  const total = (rows ?? []).reduce((a, b) => a + b.players, 0)
  return total ? r.players / total : 0
}

function cellStyle(v: number | null) {
  if (v === null) return {}
  return { background: `color-mix(in srgb, var(--accent) ${Math.round(Math.min(1, v * 2.2) * 85)}%, transparent)`, color: v > 0.25 ? '#fff' : undefined }
}
</script>

<template>
  <PageHeader title="Analytics" subtitle="Player activity, engagement, retention and revenue. Days are UTC.">
    <form class="relative" @submit.prevent="findPlayer">
      <Search class="pointer-events-none absolute top-2 left-2.5 size-4 text-faint" />
      <input v-model="lookup" class="input w-44 pl-8" placeholder="Look up user id" inputmode="numeric" />
    </form>
    <RangePicker v-model="range" :options="['7d', '30d', '90d']" />
  </PageHeader>
  <ErrorBanner :message="players.error.value" />
  <template v-if="!players.data.value && !players.error.value">
    <div class="grid grid-cols-2 gap-3 md:grid-cols-4">
      <div v-for="i in 4" :key="i" class="card space-y-3 px-4 py-4"><Skeleton w="40%" h="0.7rem" /><Skeleton w="55%" h="1.5rem" /></div>
    </div>
    <div class="mt-3 grid gap-3 lg:grid-cols-2">
      <div v-for="i in 4" :key="i" class="card p-4"><Skeleton h="15rem" /></div>
    </div>
  </template>

  <template v-if="players.data.value">
    <div class="grid grid-cols-2 gap-3 md:grid-cols-4">
      <StatCard label="Active players" :value="num(players.data.value.summary.uniquePlayers)" :sub="`${num(players.data.value.summary.totalPlayers)} all time`" />
      <StatCard
        label="New players"
        :value="num(players.data.value.summary.newPlayers)"
        :sub="`${pct(players.data.value.summary.newPlayers / Math.max(1, players.data.value.summary.uniquePlayers), 0)} of active`"
      />
      <StatCard
        label="Avg. session"
        :value="duration(players.data.value.summary.avgSessionMs)"
        :sub="`${num(players.data.value.summary.sessions)} sessions · ${num(players.data.value.summary.playtimeMs / 3_600_000)} h played`"
      />
      <StatCard
        label="Revenue"
        :value="robux(players.data.value.summary.revenue)"
        :sub="`${num(players.data.value.summary.payers)} payers · ${pct(players.data.value.summary.payers / Math.max(1, players.data.value.summary.uniquePlayers))} conversion`"
      />
    </div>

    <div class="mt-3 grid gap-3 lg:grid-cols-2">
      <div class="card">
        <div class="card-header"><span class="card-title">Daily active players</span></div>
        <div class="p-3">
          <TimeChart
            :data="players.data.value.series"
            type="bar"
            stacked
            daily
            :series="[{ key: 'returning', label: 'Returning' }, { key: 'new', label: 'New' }]"
          />
        </div>
      </div>
      <div class="card">
        <div class="card-header"><span class="card-title">Peak concurrent players</span></div>
        <div class="p-3"><TimeChart :data="players.data.value.series" daily :series="[{ key: 'peakCcu', label: 'Peak CCU' }]" /></div>
      </div>
      <div class="card">
        <div class="card-header"><span class="card-title">Engagement</span></div>
        <div class="p-3">
          <TimeChart
            :data="players.data.value.series"
            type="line"
            daily
            :series="[
              { key: 'sessions', label: 'Sessions' },
              { key: 'avgSessionMin', label: 'Avg. session', right: true, format: (v: number) => duration(v * 60_000) },
            ]"
          />
        </div>
      </div>
      <div class="card">
        <div class="card-header"><span class="card-title">Revenue (Robux)</span></div>
        <div class="p-3">
          <TimeChart :data="players.data.value.series" type="bar" daily :series="[{ key: 'revenue', label: 'Robux', color: 'var(--success)' }]" />
        </div>
      </div>
    </div>
  </template>

  <div class="card mt-3">
    <div class="card-header">
      <span class="card-title">Retention</span>
      <span class="text-xs text-muted">Share of each day's new players who came back N days later</span>
    </div>
    <div class="overflow-x-auto">
      <table v-if="retention.data.value?.cohorts.length" class="table">
        <thead>
          <tr>
            <th>Cohort</th>
            <th class="text-right">Players</th>
            <th v-for="o in retention.data.value.offsets" :key="o" class="text-center">D{{ o }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="c in retention.data.value.cohorts" :key="c.day">
            <td class="whitespace-nowrap">{{ day(c.day) }}</td>
            <td class="tabular text-right">{{ num(c.size) }}</td>
            <td v-for="(v, i) in c.retained" :key="i" class="p-0.5! text-center">
              <div v-if="v !== null" class="tabular rounded px-1 py-1 text-xs" :style="cellStyle(v)">{{ pct(v, 0) }}</div>
            </td>
          </tr>
        </tbody>
      </table>
      <div v-else class="px-4 py-8 text-center text-[13px] text-muted">No new players in the last 14 days.</div>
    </div>
  </div>

  <div class="mt-3 grid gap-3 lg:grid-cols-2">
    <div v-for="b in [{ title: 'Platforms', rows: platforms.data.value }, { title: 'Countries', rows: countries.data.value }]" :key="b.title" class="card">
      <div class="card-header"><span class="card-title">{{ b.title }}</span></div>
      <div v-if="!b.rows?.length" class="px-4 py-8 text-center text-[13px] text-muted">No sessions in this range.</div>
      <table v-else class="table">
        <thead>
          <tr><th></th><th class="text-right">Players</th><th class="text-right">Sessions</th><th class="text-right">Avg. session</th></tr>
        </thead>
        <tbody>
          <tr v-for="r in b.rows.slice(0, 10)" :key="r.value">
            <td class="w-1/2">
              <div class="relative h-6 overflow-hidden rounded-md bg-surface-2">
                <div class="absolute inset-y-0 left-0 bg-accent/15" :style="{ width: `${share(b.rows, r) * 100}%` }" />
                <div class="relative flex h-full items-center justify-between px-2 text-xs">
                  <span>{{ r.value }}</span><span class="tabular text-muted">{{ pct(share(b.rows, r), 0) }}</span>
                </div>
              </div>
            </td>
            <td class="tabular text-right">{{ num(r.players) }}</td>
            <td class="tabular text-right">{{ num(r.sessions) }}</td>
            <td class="tabular text-right">{{ duration(r.avgSessionMs) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
