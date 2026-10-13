<script setup lang="ts">
import { Search, ShieldAlert, Users } from 'lucide-vue-next'
import { ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import EmptyState from '@/components/EmptyState.vue'
import ErrorBanner from '@/components/ErrorBanner.vue'
import PageHeader from '@/components/PageHeader.vue'
import RelTime from '@/components/RelTime.vue'
import Select from '@/components/Select.vue'
import Skeleton from '@/components/Skeleton.vue'
import { api } from '@/lib/api'
import { duration, num } from '@/lib/format'
import { useProject } from '@/lib/project'
import { useAsync } from '@/lib/useAsync'

const { pid } = useProject()
const router = useRouter()
const search = ref('')
const q = ref('')
const sort = ref('recent')
let t: ReturnType<typeof setTimeout> | undefined
watch(search, (v) => {
  clearTimeout(t)
  t = setTimeout(() => (q.value = v.trim()), 300)
})
interface PlayerRow {
  userId: number
  firstSeen: number
  lastSeen: number
  sessionCount: number
  playtimeMs: number
  online: boolean
  detections: number
}
const { data, error } = useAsync(
  () => api.get<{ players: PlayerRow[]; hashedIds: boolean }>(`/projects/${pid.value}/players`, { q: q.value, raw: q.value ? 1 : undefined, sort: sort.value, limit: 200 }),
  [pid, q, sort],
)
const sortOptions = [
  { value: 'recent', label: 'Recently played' },
  { value: 'new', label: 'Newest' },
  { value: 'playtime', label: 'Most playtime' },
  { value: 'sessions', label: 'Most sessions' },
]
</script>

<template>
  <PageHeader title="Players" subtitle="Everyone who has played, with their sessions and timeline." />
  <ErrorBanner :message="error" />
  <div class="mb-3 flex flex-wrap items-center gap-2">
    <div class="relative w-full sm:w-72">
      <Search class="pointer-events-none absolute top-2 left-2.5 size-4 text-faint" />
      <input v-model="search" class="input pl-8" placeholder="Roblox user id" inputmode="numeric" />
    </div>
    <span v-if="data?.hashedIds" class="text-xs text-muted">Player ids are stored as HMAC hashes; search with the Roblox id.</span>
    <Select v-model="sort" :options="sortOptions" label="Sort" class="ml-auto" />
  </div>
  <div class="card overflow-x-auto">
    <div v-if="!data" class="space-y-3 p-4"><Skeleton v-for="i in 5" :key="i" h="1.2rem" /></div>
    <EmptyState v-else-if="data.players.length === 0" :title="q ? 'No player with that id' : 'No players yet'" :icon="Users" />
    <table v-else class="table">
      <thead><tr><th>Player</th><th>Last played</th><th>First played</th><th class="text-right">Sessions</th><th class="text-right">Playtime</th><th class="text-right">Detections</th></tr></thead>
      <tbody>
        <tr v-for="p in data.players" :key="p.userId" class="clickable" @click="router.push({ name: 'player', params: { pid, userId: p.userId } })">
          <td><span class="flex items-center gap-2"><span class="dot" :class="p.online ? 'bg-success' : 'bg-faint/40'" /><span class="mono">{{ p.userId }}</span></span></td>
          <td class="text-muted"><RelTime :ts="p.lastSeen" /></td>
          <td class="text-muted"><RelTime :ts="p.firstSeen" /></td>
          <td class="tabular text-right">{{ num(p.sessionCount) }}</td>
          <td class="tabular text-right">{{ duration(p.playtimeMs) }}</td>
          <td class="tabular text-right">
            <span v-if="p.detections" class="inline-flex items-center gap-1 text-warning"><ShieldAlert class="size-3.5" />{{ p.detections }}</span>
            <span v-else class="text-faint">0</span>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
