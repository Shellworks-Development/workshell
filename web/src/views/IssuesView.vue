<script setup lang="ts">
import { Bug, CheckCircle2, EyeOff, MessageSquare, RotateCcw, Search, Trash2, X } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import Avatar from '@/components/Avatar.vue'
import EmptyState from '@/components/EmptyState.vue'
import ErrorBanner from '@/components/ErrorBanner.vue'
import IssueTitle from '@/components/IssueTitle.vue'
import PageHeader from '@/components/PageHeader.vue'
import RangePicker from '@/components/RangePicker.vue'
import RelTime from '@/components/RelTime.vue'
import Segmented from '@/components/Segmented.vue'
import Select from '@/components/Select.vue'
import Skeleton from '@/components/Skeleton.vue'
import Sparkline from '@/components/Sparkline.vue'
import { api, type BoardItem, type Issue, type IssueStatus } from '@/lib/api'
import { num } from '@/lib/format'
import { confetti, originOf } from '@/lib/confetti'
import { confirmDialog, toast } from '@/lib/overlay'
import { useProject } from '@/lib/project'
import { loadProjects } from '@/lib/state'
import { useAsync } from '@/lib/useAsync'
import SendButton from '@/components/whiteboard/SendButton.vue'
import { issueSnap } from '@/lib/snapshots'
import { embedItem } from '@/lib/whiteboard'

const { pid } = useProject()
const router = useRouter()
const route = useRoute()

const status = ref<IssueStatus>('unresolved')
const side = ref('')
const sort = ref('last_seen')
const assignee = ref('')
const range = ref('7d')
const release = ref(typeof route.query.release === 'string' ? route.query.release : '')
const search = ref('')
const query = ref('')
const limit = ref(100)
const selected = ref<Set<number>>(new Set())
let searchTimer: ReturnType<typeof setTimeout> | undefined
watch(search, (v) => {
  clearTimeout(searchTimer)
  searchTimer = setTimeout(() => (query.value = v), 250)
})

const { data, error, reload } = useAsync(
  () =>
    api.get<{ issues: Issue[]; counts: Record<IssueStatus | 'mine', number> }>(`/projects/${pid.value}/issues`, {
      status: status.value,
      side: side.value,
      sort: sort.value,
      range: range.value,
      q: query.value,
      assignee: assignee.value,
      release: release.value,
      limit: limit.value,
    }),
  [pid, status, side, sort, range, query, assignee, release, limit],
  { refreshMs: 30_000 },
)
watch([status, side, query, range, assignee, release], () => {
  selected.value.clear()
  limit.value = 100
})

const allSelected = computed(() => !!data.value?.issues.length && data.value.issues.every((i) => selected.value.has(i.id)))
function toggleAll() {
  if (allSelected.value) selected.value.clear()
  else data.value?.issues.forEach((i) => selected.value.add(i.id))
}
function toggle(id: number) {
  if (selected.value.has(id)) selected.value.delete(id)
  else selected.value.add(id)
}

const busy = ref(false)
// Selected issues go to a board as a grid of cards.
function sendSelected(): BoardItem[] {
  const list = (data.value?.issues ?? []).filter((i) => selected.value.has(i.id))
  return list.map((i, n) => {
    const it = embedItem(issueSnap(i, null, pid.value))
    it.x = (n % 3) * (it.w + 32)
    it.y = Math.floor(n / 3) * (it.h + 32)
    return it
  })
}

async function bulk(body: { status?: IssueStatus; delete?: boolean }, e?: Event) {
  const origin = originOf(e)
  const n = selected.value.size
  const noun = `${n} issue${n === 1 ? '' : 's'}`
  if (
    body.delete &&
    !(await confirmDialog({
      title: `Delete ${noun}?`,
      message: `All of ${n === 1 ? 'its' : 'their'} events are deleted too. If the error happens again, it comes back as a new issue.`,
      confirmLabel: 'Delete',
      danger: true,
    }))
  )
    return
  busy.value = true
  try {
    await api.post(`/projects/${pid.value}/issues/bulk`, { ids: [...selected.value], ...body })
    if (body.status === 'resolved') confetti(origin, Math.min(40 + n * 10, 140))
    selected.value.clear()
    loadProjects().catch(() => {})
    await reload()
    toast(body.delete ? `Deleted ${noun}` : `Marked ${noun} as ${body.status}`)
  } catch (e) {
    toast.error('Couldn’t update issues', (e as Error).message)
  } finally {
    busy.value = false
  }
}

const tabs = computed(() =>
  (['unresolved', 'resolved', 'ignored'] as const).map((value) => ({
    value,
    label: value[0]!.toUpperCase() + value.slice(1),
    count: num(data.value?.counts[value] ?? 0),
  })),
)
const sideOptions = [
  { value: '', label: 'Server + client' },
  { value: 'server', label: 'Server' },
  { value: 'client', label: 'Client' },
]
const sortOptions = [
  { value: 'last_seen', label: 'Last seen' },
  { value: 'first_seen', label: 'First seen' },
  { value: 'events', label: 'Most events' },
  { value: 'users', label: 'Most players' },
]
const assigneeOptions = [
  { value: '', label: 'Anyone' },
  { value: 'me', label: 'Assigned to me' },
  { value: 'none', label: 'Unassigned' },
]
</script>

<template>
  <PageHeader title="Issues" subtitle="Errors grouped by cause, across server and client scripts.">
    <RangePicker v-model="range" :options="['24h', '7d', '30d', '90d']" />
  </PageHeader>
  <ErrorBanner :message="error" />

  <div class="mb-3 flex flex-wrap items-center gap-2">
    <Segmented v-model="status" :options="tabs" size="md" />
    <button
      class="btn h-8"
      :class="assignee === 'me' ? 'border-fg/30 bg-surface-3' : ''"
      :aria-pressed="assignee === 'me'"
      @click="assignee = assignee === 'me' ? '' : 'me'"
    >
      Assigned to me <span class="tabular text-faint">{{ data?.counts.mine ?? 0 }}</span>
    </button>
    <span v-if="release" class="chip h-8 gap-1.5 px-2">
      release {{ release }}
      <button aria-label="Clear release filter" class="text-faint hover:text-fg" @click="release = ''; router.replace({ query: {} })"><X class="size-3" /></button>
    </span>
    <div class="relative ml-auto w-full sm:w-64">
      <Search class="pointer-events-none absolute top-2 left-2.5 size-4 text-faint" />
      <input v-model="search" class="input pl-8" placeholder="Search message or script" />
    </div>
    <Select v-model="assignee" :options="assigneeOptions" label="Assignee" />
    <Select v-model="side" :options="sideOptions" label="Side" />
    <Select v-model="sort" :options="sortOptions" label="Sort by" />
  </div>

  <div class="card">
    <div class="flex h-10 items-center gap-2 border-b border-border px-4 text-xs text-muted">
      <input type="checkbox" class="size-3.5 accent-(--fg)" :checked="allSelected" aria-label="Select all" @change="toggleAll" />
      <template v-if="selected.size > 0">
        <span class="rise-in mr-1">{{ selected.size }} selected</span>
        <button v-if="status !== 'resolved'" class="btn btn-sm" :disabled="busy" @click="bulk({ status: 'resolved' }, $event)"><CheckCircle2 class="size-3.5" /> Resolve</button>
        <button v-if="status !== 'ignored'" class="btn btn-sm" :disabled="busy" @click="bulk({ status: 'ignored' })"><EyeOff class="size-3.5" /> Ignore</button>
        <button v-if="status !== 'unresolved'" class="btn btn-sm" :disabled="busy" @click="bulk({ status: 'unresolved' })"><RotateCcw class="size-3.5" /> Reopen</button>
        <SendButton small label="Whiteboard" :build="sendSelected" />
        <button class="btn btn-sm text-danger" :disabled="busy" @click="bulk({ delete: true })"><Trash2 class="size-3.5" /> Delete</button>
      </template>
      <template v-else>
        <span class="flex-1">Issue</span>
        <span class="hidden w-28 text-center md:block">Last 24h</span>
        <span class="w-16 text-right">Events</span>
        <span class="hidden w-16 text-right sm:block">Players</span>
        <span class="hidden w-14 text-right sm:block">Owner</span>
      </template>
    </div>

    <div v-if="!data" class="divide-y divide-border">
      <div v-for="i in 6" :key="i" class="flex items-center gap-4 px-4 py-3.5">
        <div class="flex-1 space-y-2"><Skeleton w="45%" h="0.9rem" /><Skeleton w="30%" h="0.7rem" /></div>
        <Skeleton w="6rem" h="1.25rem" class="hidden md:block" />
        <Skeleton w="2.5rem" h="0.9rem" />
      </div>
    </div>
    <EmptyState v-else-if="data.issues.length === 0" :title="query ? 'No matching issues' : `No ${status} issues`" :icon="Bug">
      {{ status === 'unresolved' ? 'Errors from your game show up here, grouped by cause.' : 'Nothing here.' }}
    </EmptyState>

    <div
      v-for="i in data?.issues"
      :key="i.id"
      class="flex cursor-pointer items-center gap-2 border-b border-border px-4 py-3 last:border-0 hover:bg-surface-2"
      @click="router.push({ name: 'issue', params: { pid, iid: i.id } })"
    >
      <input type="checkbox" class="size-3.5 shrink-0 accent-(--fg)" :checked="selected.has(i.id)" aria-label="Select issue" @click.stop @change="toggle(i.id)" />
      <div class="min-w-0 flex-1 pl-1">
        <IssueTitle :issue="i" />
        <div class="mt-1 flex items-center gap-2 text-[11.5px] text-faint">
          <RelTime :ts="i.lastSeen" />
          <span>·</span>
          <span>first seen <RelTime :ts="i.firstSeen" /></span>
          <template v-if="i.lastRelease"><span>·</span><span class="font-mono">{{ i.lastRelease }}</span></template>
          <template v-if="i.commentCount"><span>·</span><span class="flex items-center gap-0.5"><MessageSquare class="size-3" />{{ i.commentCount }}</span></template>
        </div>
      </div>
      <div class="hidden w-28 justify-center md:flex"><Sparkline :values="i.spark ?? []" color="var(--danger)" /></div>
      <div class="tabular w-16 text-right text-[13px] font-medium">{{ num(i.rangeEvents) }}</div>
      <div class="tabular hidden w-16 text-right text-[13px] text-muted sm:block">{{ num(i.userCount) }}</div>
      <div class="hidden w-14 justify-end sm:flex">
        <Avatar v-if="i.assignee" :name="i.assignee.name" :src="i.assignee.avatar" :size="22" />
      </div>
    </div>
    <div v-if="data && data.issues.length >= limit" class="border-t border-border p-2 text-center">
      <button class="btn btn-sm" :disabled="limit >= 200" @click="limit = Math.min(limit + 100, 200)">
        {{ limit >= 200 ? 'Showing the first 200 — refine your search' : 'Load more' }}
      </button>
    </div>
  </div>
</template>
