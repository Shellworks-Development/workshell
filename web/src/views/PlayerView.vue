<script setup lang="ts">
import { ArrowLeft, ExternalLink, ScrollText, Trash2 } from 'lucide-vue-next'
import { RouterLink, useRouter } from 'vue-router'
import Badge from '@/components/Badge.vue'
import ErrorBanner from '@/components/ErrorBanner.vue'
import RelTime from '@/components/RelTime.vue'
import Skeleton from '@/components/Skeleton.vue'
import StatCard from '@/components/StatCard.vue'
import Timeline from '@/components/Timeline.vue'
import { api, type Session, type TimelineItem } from '@/lib/api'
import { duration, num, robux, shortId } from '@/lib/format'
import { confirmDialog, toast } from '@/lib/overlay'
import { useProject } from '@/lib/project'
import { useAsync } from '@/lib/useAsync'
import SendButton from '@/components/whiteboard/SendButton.vue'
import { playerSnap } from '@/lib/snapshots'

const props = defineProps<{ userId: string }>()
const { pid } = useProject()
const router = useRouter()
interface Detail {
  player: { userId: number; firstSeen: number; lastSeen: number; sessionCount: number; playtimeMs: number; spent: number; detections: number; online: boolean }
  sessions: Session[]
  issues: { id: number; title: string; culprit: string; status: string; lastSeen: number }[]
  timeline: TimelineItem[]
  hashedIds: boolean
}
const { data, error } = useAsync(
  async () => {
    try {
      return await api.get<Detail>(`/projects/${pid.value}/players/${props.userId}`)
    } catch (e) {
      // Pseudonymized projects: the URL may hold a Roblox id rather than the stored hash.
      return await api.get<Detail>(`/projects/${pid.value}/players/${props.userId}`, { raw: 1 }).catch(() => Promise.reject(e))
    }
  },
  [pid, () => props.userId],
)

async function erase() {
  const id = String(data.value!.player.userId)
  const ok = await confirmDialog({
    title: `Erase player ${id}?`,
    message:
      'Deletes this player’s sessions, logs, events, errors, traces, detections and whiteboard snapshots from this project. Issue totals keep their counts. This can’t be undone.',
    confirmLabel: 'Erase player data',
    danger: true,
    typeToConfirm: id,
  })
  if (!ok) return
  try {
    // Opened by Roblox id in a pseudonymous project: erase both the hashed and any raw rows.
    const raw = id !== props.userId
    const res = await api.del<{ rows: number }>(`/projects/${pid.value}/players/${raw ? props.userId : id}${raw ? '?raw=1' : ''}`)
    toast(`Erased player ${id}`, `${num(res.rows)} records deleted.`)
    router.replace({ name: 'players', params: { pid: pid.value } })
  } catch (e) {
    toast.error('Couldn’t erase player', (e as Error).message)
  }
}
</script>

<template>
  <RouterLink :to="{ name: 'players', params: { pid } }" class="mb-4 inline-flex items-center gap-1 text-[13px] text-muted hover:text-fg"><ArrowLeft class="size-3.5" /> Players</RouterLink>
  <ErrorBanner :message="error === 'not found' ? `No player with id ${userId} has played this experience.` : error" />
  <div v-if="!data && !error" class="space-y-3"><Skeleton w="40%" h="1.6rem" /><Skeleton h="16rem" /></div>

  <template v-if="data">
    <div class="mb-6 flex flex-wrap items-end justify-between gap-3">
      <div>
        <div class="flex items-center gap-2">
          <h1 class="mono text-2xl font-semibold tracking-tight">{{ data.player.userId }}</h1>
          <Badge :tone="data.player.online ? 'success' : 'neutral'" dot>{{ data.player.online ? 'Playing now' : 'Offline' }}</Badge>
          <Badge v-if="data.hashedIds">pseudonymous</Badge>
        </div>
        <p class="mt-1 text-[13px] text-muted">First played <RelTime :ts="data.player.firstSeen" /> · last played <RelTime :ts="data.player.lastSeen" /></p>
      </div>
      <div class="flex flex-wrap gap-2">
        <SendButton small :build="() => playerSnap(data!.player, data!.timeline, pid)" />
        <RouterLink :to="{ name: 'logs', params: { pid }, query: { userId: String(data.player.userId) } }" class="btn btn-sm"><ScrollText class="size-3.5" /> Logs</RouterLink>
        <a v-if="!data.hashedIds" :href="`https://www.roblox.com/users/${data.player.userId}/profile`" target="_blank" rel="noopener noreferrer" class="btn btn-sm">
          <ExternalLink class="size-3.5" /> Roblox profile
        </a>
        <button class="btn btn-sm text-danger" @click="erase"><Trash2 class="size-3.5" /> Erase player data</button>
      </div>
    </div>

    <div class="grid grid-cols-2 gap-3 md:grid-cols-4">
      <StatCard label="Sessions" :value="num(data.player.sessionCount)" />
      <StatCard label="Playtime" :value="duration(data.player.playtimeMs)" />
      <StatCard label="Spent" :value="robux(data.player.spent)" />
      <StatCard label="Exploit detections" :value="num(data.player.detections)" :tone="data.player.detections ? 'warning' : undefined" />
    </div>

    <div class="mt-5 grid gap-5 lg:grid-cols-[1fr_22rem]">
      <div class="card min-w-0">
        <div class="card-header"><span class="card-title">Recent activity</span><span class="text-xs text-muted">last 7 days of play</span></div>
        <div class="p-4"><Timeline :items="[...data.timeline].reverse()" :pid="pid" /></div>
      </div>
      <div class="space-y-5">
        <div class="card">
          <div class="card-header"><span class="card-title">Sessions</span></div>
          <button
            v-for="s in data.sessions.slice(0, 15)"
            :key="s.key"
            class="flex w-full cursor-pointer items-center gap-3 border-b border-border px-4 py-2.5 text-left text-[13px] last:border-0 hover:bg-surface-2"
            @click="router.push({ name: 'session', params: { pid, sid: s.key } })"
          >
            <span class="dot" :class="s.endedAt ? 'bg-faint/40' : 'bg-success'" />
            <div class="min-w-0 flex-1">
              <div><RelTime :ts="s.startedAt" /> · {{ s.endedAt ? duration(s.endedAt - s.startedAt) : 'in progress' }}</div>
              <div class="font-mono text-[11.5px] text-faint">{{ shortId(s.key) }} · {{ s.platform || '—' }} · {{ s.release }}</div>
            </div>
            <span v-if="s.errorCount" class="tabular text-xs text-danger">{{ s.errorCount }} err</span>
          </button>
        </div>
        <div class="card">
          <div class="card-header"><span class="card-title">Errors encountered</span></div>
          <div v-if="data.issues.length === 0" class="px-4 py-6 text-center text-[13px] text-muted">None recorded.</div>
          <RouterLink
            v-for="i in data.issues"
            :key="i.id"
            :to="{ name: 'issue', params: { pid, iid: i.id } }"
            class="block border-b border-border px-4 py-2.5 last:border-0 hover:bg-surface-2"
          >
            <div class="truncate text-[13px] font-medium">{{ i.title }}</div>
            <div class="truncate font-mono text-[11.5px] text-muted">{{ i.culprit }}</div>
          </RouterLink>
        </div>
      </div>
    </div>
  </template>
</template>
