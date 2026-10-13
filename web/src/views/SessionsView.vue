<script setup lang="ts">
import { Search, Waves } from 'lucide-vue-next'
import { onBeforeUnmount, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import Badge from '@/components/Badge.vue'
import EmptyState from '@/components/EmptyState.vue'
import ErrorBanner from '@/components/ErrorBanner.vue'
import PageHeader from '@/components/PageHeader.vue'
import RangePicker from '@/components/RangePicker.vue'
import RelTime from '@/components/RelTime.vue'
import Segmented from '@/components/Segmented.vue'
import Skeleton from '@/components/Skeleton.vue'
import { api, type Session } from '@/lib/api'
import { duration, num, shortId } from '@/lib/format'
import { useProject } from '@/lib/project'
import { useAsync } from '@/lib/useAsync'

const { pid } = useProject()
const router = useRouter()
const state = ref<'live' | 'recent'>('live')
const range = ref('24h')
const errorsOnly = ref(false)
const search = ref('')
const q = ref('')
let t: ReturnType<typeof setTimeout> | undefined
watch(search, (v) => {
  clearTimeout(t)
  t = setTimeout(() => (q.value = v.trim()), 300)
})

const { data, error } = useAsync(
  () => {
    const isUser = /^\d+$/.test(q.value)
    return api.get<{ sessions: Session[]; hashedIds: boolean }>(`/projects/${pid.value}/sessions`, {
      state: state.value,
      range: range.value,
      errors: errorsOnly.value ? 1 : undefined,
      userId: isUser ? q.value : undefined,
      raw: isUser ? 1 : undefined,
      q: !isUser ? q.value : undefined,
      limit: 200,
    })
  },
  [pid, state, range, errorsOnly, q],
  { refreshMs: 15_000 },
)
const now = ref(Date.now())
const tick = setInterval(() => (now.value = Date.now()), 15_000)
onBeforeUnmount(() => clearInterval(tick))
</script>

<template>
  <PageHeader title="Sessions" subtitle="Every play session, from join to leave, with a full timeline.">
    <RangePicker v-if="state === 'recent'" v-model="range" :options="['1h', '24h', '7d', '30d']" />
  </PageHeader>
  <ErrorBanner :message="error" />

  <div class="mb-3 flex flex-wrap items-center gap-2">
    <Segmented v-model="state" :options="[{ value: 'live', label: 'Live' }, { value: 'recent', label: 'Recent' }]" size="md" />
    <button class="btn h-8" :class="errorsOnly ? 'border-fg/30 bg-surface-3' : ''" :aria-pressed="errorsOnly" @click="errorsOnly = !errorsOnly">With errors</button>
    <div class="relative ml-auto w-full sm:w-72">
      <Search class="pointer-events-none absolute top-2 left-2.5 size-4 text-faint" />
      <input v-model="search" class="input pl-8" placeholder="Roblox user id or session id" />
    </div>
  </div>

  <div class="card overflow-x-auto">
    <div v-if="!data" class="space-y-3 p-4"><Skeleton v-for="i in 5" :key="i" h="1.2rem" /></div>
    <EmptyState v-else-if="data.sessions.length === 0" :title="state === 'live' ? 'Nobody is playing right now' : 'No sessions found'" :icon="Waves">
      {{ state === 'live' ? 'Live sessions appear here as players join.' : 'Try a wider range or a different search.' }}
    </EmptyState>
    <table v-else class="table">
      <thead>
        <tr><th>Player</th><th>Started</th><th class="text-right">Duration</th><th>Platform</th><th>Release</th><th class="text-right">Errors</th><th>Session</th></tr>
      </thead>
      <tbody>
        <tr v-for="s in data.sessions" :key="s.key" class="clickable" @click="router.push({ name: 'session', params: { pid, sid: s.key } })">
          <td>
            <div class="flex items-center gap-2">
              <span class="dot" :class="s.endedAt ? 'bg-faint/40' : 'bg-success'" />
              <span class="mono">{{ s.userId }}</span>
              <Badge v-if="s.isNew" tone="accent">new</Badge>
            </div>
          </td>
          <td class="text-muted"><RelTime :ts="s.startedAt" /></td>
          <td class="tabular text-right">{{ duration((s.endedAt ?? now) - s.startedAt) }}</td>
          <td class="text-muted">{{ s.platform || '—' }} <span class="text-faint">{{ s.country }}</span></td>
          <td class="mono text-muted">{{ s.release || '—' }}</td>
          <td class="tabular text-right" :class="s.errorCount ? 'text-danger' : 'text-faint'">{{ num(s.errorCount) }}</td>
          <td class="mono text-faint">{{ shortId(s.key) }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
