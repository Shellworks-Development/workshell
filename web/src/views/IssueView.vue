<script setup lang="ts">
import {
  ArrowLeft,
  CheckCircle2,
  ChevronLeft,
  ChevronRight,
  CircleDot,
  ExternalLink,
  EyeOff,
  GitCommitHorizontal,
  GitPullRequestArrow,
  MoreHorizontal,
  Pencil,
  Presentation,
  RotateCcw,
  ShieldAlert,
  Sparkles,
  Trash2,
  TriangleAlert,
  UserRoundPlus,
} from 'lucide-vue-next'
import { computed, nextTick, ref, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import Avatar from '@/components/Avatar.vue'
import Badge from '@/components/Badge.vue'
import ErrorBanner from '@/components/ErrorBanner.vue'
import KeyValues from '@/components/KeyValues.vue'
import Popover from '@/components/Popover.vue'
import RangePicker from '@/components/RangePicker.vue'
import RelTime from '@/components/RelTime.vue'
import RichText from '@/components/RichText.vue'
import Skeleton from '@/components/Skeleton.vue'
import StackTrace from '@/components/StackTrace.vue'
import TimeChart from '@/components/TimeChart.vue'
import { api, type Activity, type ErrorEvent, type Issue, type IssueStatus, type Release, type Timeseries, type User } from '@/lib/api'
import { num, shortId, time } from '@/lib/format'
import { confirmDialog, toast } from '@/lib/overlay'
import { useProject } from '@/lib/project'
import { confetti, originOf } from '@/lib/confetti'
import { auth, loadProjects } from '@/lib/state'
import { useAsync } from '@/lib/useAsync'
import { issueSnap } from '@/lib/snapshots'
import { sendToWhiteboard } from '@/lib/whiteboard'

const props = defineProps<{ iid: number }>()
const { pid, project } = useProject()
const router = useRouter()
const range = ref('7d')

interface IssueDetail {
  issue: Issue
  histogram: Timeseries
  breakdown: Record<string, { value: string; count: number }[]>
  latest: ErrorEvent | null
  samples: number
  firstRelease: Release | null
}

const { data, error, reload } = useAsync(
  () => api.get<IssueDetail>(`/projects/${pid.value}/issues/${props.iid}`, { range: range.value }),
  [pid, () => props.iid, range],
)
const issue = computed(() => data.value?.issue)

// ---- Events browser -------------------------------------------------------------
const events = ref<ErrorEvent[]>([])
const index = ref(0)
const moreAvailable = ref(true)
async function loadEvents(before?: number) {
  const page = await api.get<ErrorEvent[]>(`/projects/${pid.value}/issues/${props.iid}/events`, { before, limit: 25 })
  events.value = before ? [...events.value, ...page] : page
  moreAvailable.value = page.length === 25
}
const event = computed(() => events.value[index.value] ?? data.value?.latest ?? null)
async function older() {
  if (index.value + 1 >= events.value.length && moreAvailable.value) await loadEvents(events.value[events.value.length - 1]?.id)
  if (index.value + 1 < events.value.length) index.value++
}

// ---- Activity -----------------------------------------------------------------
const activity = ref<Activity[]>([])
interface GhComment {
  author: string
  avatar: string
  body: string
  url: string
  createdAt: number
}
const github = ref<{ state: string; url: string; comments: GhComment[] } | null>(null)
const githubError = ref<string | null>(null)

async function loadActivity() {
  activity.value = await api.get<Activity[]>(`/projects/${pid.value}/issues/${props.iid}/activity`)
}
async function loadGithub() {
  github.value = null
  githubError.value = null
  if (!issue.value?.githubNumber) return
  try {
    github.value = await api.get(`/projects/${pid.value}/issues/${props.iid}/github`)
  } catch (e) {
    githubError.value = (e as Error).message
  }
}
watch(
  () => props.iid,
  () => {
    index.value = 0
    loadEvents().catch(() => {})
    loadActivity().catch(() => {})
  },
  { immediate: true },
)
watch(() => issue.value?.githubNumber, () => loadGithub())

type ThreadItem = { kind: 'activity'; at: number; a: Activity } | { kind: 'github'; at: number; c: GhComment }
const thread = computed<ThreadItem[]>(() => {
  const items: ThreadItem[] = activity.value.map((a) => ({ kind: 'activity', at: a.createdAt, a }))
  for (const c of github.value?.comments ?? []) {
    // Skip comments Workshell itself mirrored to GitHub.
    if (c.body.includes('commented in Workshell:')) continue
    items.push({ kind: 'github', at: c.createdAt, c })
  }
  return items.sort((x, y) => x.at - y.at)
})

// ---- Team & assignment --------------------------------------------------------
const team = useAsync(() => api.get<User[]>('/users'))
const assignOpen = ref(false)
async function assign(id: number) {
  assignOpen.value = false
  await api.patch(`/projects/${pid.value}/issues/${props.iid}`, { assigneeId: id })
  await Promise.all([reload(), loadActivity()])
  toast(id ? 'Assigned' : 'Unassigned')
}

const busy = ref(false)
const statusToast: Record<IssueStatus, string> = { resolved: 'Issue resolved', ignored: 'Issue ignored', unresolved: 'Issue reopened' }
async function setStatus(status: IssueStatus, e?: Event) {
  const origin = originOf(e)
  busy.value = true
  try {
    await api.patch(`/projects/${pid.value}/issues/${props.iid}`, { status })
    if (status === 'resolved') confetti(origin)
    // Refresh the open-issue badge in the sidebar / tab bar right away.
    loadProjects().catch(() => {})
    await Promise.all([reload(), loadActivity()])
    toast(statusToast[status], status === 'resolved' ? 'You’ll be alerted if it happens again.' : undefined)
  } catch (e) {
    toast.error('Couldn’t update the issue', (e as Error).message)
  } finally {
    busy.value = false
  }
}
const moreOpen = ref(false)
async function remove() {
  moreOpen.value = false
  const ok = await confirmDialog({
    title: 'Delete this issue?',
    message: 'All of its events and comments are deleted too. If the error happens again, it comes back as a new issue.',
    confirmLabel: 'Delete issue',
    danger: true,
  })
  if (!ok) return
  await api.del(`/projects/${pid.value}/issues/${props.iid}`)
  toast('Issue deleted')
  router.replace({ name: 'issues', params: { pid: pid.value } })
}

async function createGithubIssue() {
  busy.value = true
  try {
    const res = await api.post<{ number: number; url: string }>(`/projects/${pid.value}/issues/${props.iid}/github`)
    await Promise.all([reload(), loadActivity()])
    toast(`Created GitHub issue #${res.number}`)
  } catch (e) {
    toast.error('Couldn’t create the GitHub issue', (e as Error).message)
  } finally {
    busy.value = false
  }
}

// ---- Comments -------------------------------------------------------------------
const draft = ref('')
const postToGithub = ref(false)
const posting = ref(false)
const composer = ref<HTMLTextAreaElement | null>(null)
async function comment() {
  if (!draft.value.trim()) return
  posting.value = true
  try {
    await api.post(`/projects/${pid.value}/issues/${props.iid}/comments`, { body: draft.value, postToGithub: postToGithub.value })
    draft.value = ''
    await Promise.all([loadActivity(), reload()])
    if (postToGithub.value) loadGithub()
  } catch (e) {
    toast.error('Couldn’t post your comment', (e as Error).message)
  } finally {
    posting.value = false
  }
}
function onComposerKey(e: KeyboardEvent) {
  if (mention.value && mentionMatches.value.length) {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault()
      const n = mentionMatches.value.length
      mentionIndex.value = (mentionIndex.value + (e.key === 'ArrowDown' ? 1 : n - 1)) % n
      return
    }
    if (e.key === 'Enter' || e.key === 'Tab') {
      e.preventDefault()
      pickMention(mentionMatches.value[mentionIndex.value]!)
      return
    }
    if (e.key === 'Escape') {
      mention.value = null
      return
    }
  }
  if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) comment()
}

// @mention suggestions: the word being typed at the caret.
const mention = ref<{ start: number; query: string } | null>(null)
const mentionIndex = ref(0)
const mentionMatches = computed(() => {
  if (!mention.value) return []
  const q = mention.value.query.toLowerCase()
  return (team.data.value ?? []).filter((u) => u.handle.startsWith(q) || u.name.toLowerCase().includes(q)).slice(0, 5)
})
function onComposerInput() {
  const el = composer.value
  if (!el) return
  const before = el.value.slice(0, el.selectionStart)
  const m = /(?:^|\s)@([a-z0-9_-]*)$/i.exec(before)
  mention.value = m ? { start: before.length - m[1]!.length - 1, query: m[1]! } : null
  mentionIndex.value = 0
}
async function pickMention(u: User) {
  const el = composer.value
  if (!el || !mention.value) return
  const end = el.selectionStart
  draft.value = draft.value.slice(0, mention.value.start) + '@' + u.handle + ' ' + draft.value.slice(end)
  const caret = mention.value.start + u.handle.length + 2
  mention.value = null
  await nextTick()
  el.focus()
  el.setSelectionRange(caret, caret)
}

const editing = ref<number | null>(null)
const editDraft = ref('')
async function saveEdit(a: Activity) {
  await api.patch(`/projects/${pid.value}/issues/${props.iid}/comments/${a.id}`, { body: editDraft.value })
  editing.value = null
  await loadActivity()
}
async function deleteComment(a: Activity) {
  const ok = await confirmDialog({ title: 'Delete this comment?', confirmLabel: 'Delete', danger: true })
  if (!ok) return
  await api.del(`/projects/${pid.value}/issues/${props.iid}/comments/${a.id}`)
  await Promise.all([loadActivity(), reload()])
}

const mentionPath = (handle: string) => {
  const u = team.data.value?.find((x) => x.handle === handle)
  return u ? `/team/${u.id}` : '/team'
}

const breakdownLabels: Record<string, string> = { release: 'Release', environment: 'Environment', server: 'Server' }
const total = (list: { count: number }[]) => list.reduce((a, b) => a + b.count, 0) || 1
const levelTone = (l?: string) => (l === 'error' ? 'danger' : l === 'warn' ? 'warning' : 'neutral')
</script>

<template>
  <RouterLink :to="{ name: 'issues', params: { pid } }" class="mb-4 inline-flex items-center gap-1 text-[13px] text-muted hover:text-fg">
    <ArrowLeft class="size-3.5" /> Issues
  </RouterLink>
  <ErrorBanner :message="error" />

  <div v-if="!data && !error" class="space-y-3"><Skeleton w="60%" h="1.6rem" /><Skeleton w="35%" /><Skeleton h="12rem" /></div>

  <template v-if="data && issue">
    <!-- Header -->
    <div class="mb-6 flex flex-wrap items-start justify-between gap-4 border-b border-border pb-5">
      <div class="min-w-0">
        <h1 class="text-2xl font-semibold tracking-tight break-words">{{ issue.title }} <span class="font-normal text-faint">#{{ issue.id }}</span></h1>
        <div class="mt-2 flex flex-wrap items-center gap-2 text-[13px] text-muted">
          <Badge v-if="issue.status === 'resolved'" tone="success" dot>Resolved</Badge>
          <Badge v-else-if="issue.status === 'ignored'" dot>Ignored</Badge>
          <Badge v-else tone="danger" dot>Unresolved</Badge>
          <Badge v-if="issue.regressedAt && issue.status === 'unresolved'" tone="warning">Regressed <RelTime :ts="issue.regressedAt" /></Badge>
          <Badge :tone="issue.side === 'client' ? 'info' : 'neutral'">{{ issue.side }}</Badge>
          <span class="mono break-all">{{ issue.culprit }}</span>
        </div>
      </div>
      <div class="flex items-center gap-2">
        <button v-if="issue.status !== 'resolved'" class="btn btn-primary" :disabled="busy" @click="setStatus('resolved', $event)"><CheckCircle2 class="size-4" /> Resolve</button>
        <button v-else class="btn" :disabled="busy" @click="setStatus('unresolved')"><RotateCcw class="size-4" /> Reopen</button>
        <button v-if="issue.status === 'unresolved'" class="btn" :disabled="busy" @click="setStatus('ignored')"><EyeOff class="size-4" /> Ignore</button>
        <Popover v-model:open="moreOpen" placement="bottom-end" width="12rem">
          <template #trigger="{ toggle }">
            <button class="btn btn-icon" aria-label="More actions" @click="toggle"><MoreHorizontal class="size-4" /></button>
          </template>
          <div class="p-1 text-[13px]">
            <button v-if="issue.status === 'ignored'" class="menu-item" @click="moreOpen = false; setStatus('unresolved')">
              <RotateCcw class="size-4 text-muted" /> Stop ignoring
            </button>
            <button class="menu-item" @click="moreOpen = false; sendToWhiteboard(pid, issueSnap(issue, event, pid))">
              <Presentation class="size-4 text-muted" /> Send to whiteboard
            </button>
            <button class="menu-item text-danger" @click="remove"><Trash2 class="size-4" /> Delete issue</button>
          </div>
        </Popover>
      </div>
    </div>

    <div class="grid gap-8 lg:grid-cols-[1fr_17rem]">
      <!-- Main column -->
      <div class="min-w-0 space-y-5">
        <div class="grid grid-cols-2 overflow-hidden rounded-xl border border-border bg-surface sm:grid-cols-4">
          <div class="border-r border-b border-border px-4 py-3 sm:border-b-0">
            <div class="text-xs text-muted">Events</div>
            <div class="tabular mt-0.5 text-lg font-semibold">{{ num(issue.eventCount) }}</div>
          </div>
          <div class="border-b border-border px-4 py-3 sm:border-r sm:border-b-0">
            <div class="text-xs text-muted">Players affected</div>
            <div class="tabular mt-0.5 text-lg font-semibold">{{ num(issue.userCount) }}</div>
          </div>
          <div class="border-r border-border px-4 py-3">
            <div class="text-xs text-muted">First seen</div>
            <div class="mt-0.5 text-sm font-medium"><RelTime :ts="issue.firstSeen" /></div>
          </div>
          <div class="px-4 py-3">
            <div class="text-xs text-muted">Last seen</div>
            <div class="mt-0.5 text-sm font-medium"><RelTime :ts="issue.lastSeen" /></div>
          </div>
        </div>

        <div class="card">
          <div class="card-header">
            <span class="card-title">Occurrences</span>
            <RangePicker v-model="range" :options="['24h', '7d', '30d', '90d']" />
          </div>
          <div class="p-3">
            <TimeChart :data="data.histogram" type="bar" :height="140" :series="[{ key: 'events', label: 'Events', color: 'var(--danger)' }]" />
          </div>
        </div>

        <!-- Event -->
        <div v-if="event" class="card">
          <div class="card-header">
            <div class="flex min-w-0 items-center gap-2">
              <span class="card-title">Event</span>
              <span class="truncate text-xs text-muted"><RelTime :ts="event.ts" absolute-only /><template v-if="event.count > 1"> · ×{{ event.count }}</template></span>
            </div>
            <div class="flex items-center gap-1">
              <button class="btn btn-sm btn-icon size-7" :disabled="index === 0" aria-label="Newer event" @click="index--"><ChevronLeft class="size-3.5" /></button>
              <span class="tabular px-1 text-xs text-muted">{{ index + 1 }}{{ moreAvailable ? '+' : ` / ${events.length}` }}</span>
              <button class="btn btn-sm btn-icon size-7" :disabled="!moreAvailable && index + 1 >= events.length" aria-label="Older event" @click="older">
                <ChevronRight class="size-3.5" />
              </button>
            </div>
          </div>
          <div class="space-y-5 p-4">
            <pre class="mono rounded-lg border border-border bg-surface-2 p-3 whitespace-pre-wrap">{{ event.message }}</pre>
            <section>
              <h3 class="mb-2 text-[13px] font-medium">Stack trace</h3>
              <StackTrace :frames="event.frames" :raw="event.stack" />
            </section>
            <section v-if="event.breadcrumbs?.length">
              <h3 class="mb-2 text-[13px] font-medium">Breadcrumbs</h3>
              <div class="overflow-hidden rounded-lg border border-border">
                <div v-for="(b, i) in [...event.breadcrumbs].reverse()" :key="i" class="flex items-baseline gap-3 border-b border-border px-3 py-1.5 text-[13px] last:border-0">
                  <span class="mono shrink-0 whitespace-nowrap text-faint">{{ time(b.ts) }}</span>
                  <Badge :tone="levelTone(b.level)" class="w-16 shrink-0 justify-center">{{ b.category }}</Badge>
                  <span class="mono min-w-0 flex-1 break-all">{{ b.message }}</span>
                </div>
              </div>
            </section>
            <div class="grid gap-5 sm:grid-cols-3">
              <section>
                <h3 class="mb-2 text-[13px] font-medium">Details</h3>
                <dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5 text-[13px]">
                  <dt class="text-muted">Server</dt>
                  <dd class="min-w-0"><RouterLink :to="{ name: 'server', params: { pid, jobId: event.jobId } }" class="mono link">{{ shortId(event.jobId) }}</RouterLink></dd>
                  <dt class="text-muted">Release</dt>
                  <dd class="min-w-0"><RouterLink :to="{ name: 'release', params: { pid, version: event.release || event.placeVersion } }" class="mono link">{{ event.release || event.placeVersion || '?' }}</RouterLink></dd>
                  <template v-if="event.userId">
                    <dt class="text-muted">Player</dt>
                    <dd><RouterLink :to="{ name: 'player', params: { pid, userId: event.userId } }" class="mono link">{{ event.userId }}</RouterLink></dd>
                  </template>
                  <template v-if="event.sessionKey">
                    <dt class="text-muted">Session</dt>
                    <dd><RouterLink :to="{ name: 'session', params: { pid, sid: event.sessionKey } }" class="mono link">{{ shortId(event.sessionKey) }}</RouterLink></dd>
                  </template>
                  <dt class="text-muted">Env</dt>
                  <dd>{{ event.environment || '—' }}</dd>
                </dl>
              </section>
              <section>
                <h3 class="mb-2 text-[13px] font-medium">Tags</h3>
                <KeyValues :data="event.tags" />
              </section>
              <section>
                <h3 class="mb-2 text-[13px] font-medium">Context</h3>
                <KeyValues :data="event.context" />
              </section>
            </div>
            <p v-if="data && data.samples < issue.eventCount" class="border-t border-border pt-4 text-[13px] text-muted">
              Full details are kept for {{ num(data.samples) }} of {{ num(issue.eventCount) }} events, up to 20 per release. The rest are counted in the totals and charts.
            </p>
          </div>
        </div>

        <!-- Activity thread -->
        <section>
          <h2 class="mb-3 text-base font-semibold">Activity</h2>
          <ol class="relative space-y-4 before:absolute before:top-2 before:bottom-2 before:left-[15px] before:w-px before:bg-border">
            <li v-for="(t, i) in thread" :key="i" class="relative flex gap-3">
              <template v-if="t.kind === 'github'">
                <img :src="t.c.avatar" :alt="t.c.author" class="relative z-10 size-8 shrink-0 rounded-full ring-4 ring-bg" />
                <div class="card min-w-0 flex-1">
                  <div class="flex items-center gap-2 border-b border-border bg-surface-2/60 px-3 py-2 text-[13px]">
                    <span class="font-medium">{{ t.c.author }}</span>
                    <Badge><GitPullRequestArrow class="size-3" /> GitHub</Badge>
                    <a :href="t.c.url" target="_blank" rel="noopener noreferrer" class="text-muted hover:text-fg"><RelTime :ts="t.c.createdAt" /></a>
                  </div>
                  <div class="px-3 py-2.5"><RichText :text="t.c.body" /></div>
                </div>
              </template>
              <template v-else-if="t.a.kind === 'comment'">
                <Avatar :name="t.a.user?.name ?? 'Deleted user'" :src="t.a.user?.avatar" :size="32" class="relative z-10 ring-4 ring-bg" />
                <div class="card min-w-0 flex-1">
                  <div class="flex items-center gap-2 border-b border-border bg-surface-2/60 px-3 py-2 text-[13px]">
                    <RouterLink v-if="t.a.user" :to="`/team/${t.a.user.id}`" class="font-medium hover:underline">{{ t.a.user.name }}</RouterLink>
                    <span v-else class="font-medium">Deleted user</span>
                    <span class="text-muted"><RelTime :ts="t.a.createdAt" /><template v-if="t.a.editedAt"> · edited</template></span>
                    <span class="flex-1" />
                    <template v-if="t.a.user?.id === auth.user?.id">
                      <button class="btn btn-ghost btn-sm btn-icon size-6" aria-label="Edit comment" @click="editing = t.a.id; editDraft = t.a.body"><Pencil class="size-3" /></button>
                    </template>
                    <button v-if="t.a.user?.id === auth.user?.id || auth.user?.isAdmin" class="btn btn-ghost btn-sm btn-icon size-6" aria-label="Delete comment" @click="deleteComment(t.a)">
                      <Trash2 class="size-3" />
                    </button>
                  </div>
                  <div v-if="editing === t.a.id" class="space-y-2 p-3">
                    <textarea v-model="editDraft" class="input min-h-24" />
                    <div class="flex justify-end gap-2">
                      <button class="btn btn-sm" @click="editing = null">Cancel</button>
                      <button class="btn btn-primary btn-sm" :disabled="!editDraft.trim()" @click="saveEdit(t.a)">Save</button>
                    </div>
                  </div>
                  <div v-else class="px-3 py-2.5"><RichText :text="t.a.body" :mention-path="mentionPath" /></div>
                </div>
              </template>
              <template v-else>
                <span class="relative z-10 grid size-8 shrink-0 place-items-center rounded-full bg-surface-2 text-muted ring-4 ring-bg">
                  <Sparkles v-if="t.a.kind === 'first_seen'" class="size-3.5" />
                  <TriangleAlert v-else-if="t.a.kind === 'regression'" class="size-3.5 text-warning" />
                  <ShieldAlert v-else-if="t.a.kind === 'quarantine'" class="size-3.5 text-warning" />
                  <UserRoundPlus v-else-if="t.a.kind === 'assign'" class="size-3.5" />
                  <GitPullRequestArrow v-else-if="t.a.kind === 'github'" class="size-3.5" />
                  <CheckCircle2 v-else-if="t.a.data.status === 'resolved'" class="size-3.5 text-success" />
                  <CircleDot v-else class="size-3.5" />
                </span>
                <div class="flex min-h-8 flex-wrap items-center gap-x-1.5 text-[13px] text-muted">
                  <template v-if="t.a.kind === 'first_seen'">
                    First seen in
                    <RouterLink :to="{ name: 'release', params: { pid, version: t.a.data.release } }" class="mono link text-fg">{{ t.a.data.release || 'unknown release' }}</RouterLink>
                  </template>
                  <template v-else-if="t.a.kind === 'quarantine'">
                    <span class="text-warning">Automatically ignored</span> — only reported by
                    <RouterLink :to="{ name: 'player', params: { pid, userId: t.a.data.userId } }" class="mono link text-fg">player {{ t.a.data.userId }}</RouterLink>,
                    who was flagged for <span class="mono text-fg">{{ t.a.data.reason }}</span>
                  </template>
                  <template v-else-if="t.a.kind === 'regression'">
                    <span class="text-warning">Regressed</span> in
                    <RouterLink :to="{ name: 'release', params: { pid, version: t.a.data.release } }" class="mono link text-fg">{{ t.a.data.release }}</RouterLink>
                  </template>
                  <template v-else>
                    <span class="font-medium text-fg">{{ t.a.user?.name ?? 'Someone' }}</span>
                    <template v-if="t.a.kind === 'status'">
                      {{ t.a.data.status === 'resolved' ? 'resolved this' : t.a.data.status === 'ignored' ? 'ignored this' : 'reopened this' }}
                    </template>
                    <template v-else-if="t.a.kind === 'assign'">
                      <template v-if="t.a.data.assigneeId">assigned <span class="font-medium text-fg">{{ t.a.data.assigneeName }}</span></template>
                      <template v-else>removed the assignee</template>
                    </template>
                    <template v-else-if="t.a.kind === 'github'">
                      linked <a :href="t.a.data.url" target="_blank" rel="noopener noreferrer" class="link text-fg">GitHub #{{ t.a.data.number }}</a>
                    </template>
                  </template>
                  <span class="text-faint">· <RelTime :ts="t.a.createdAt" /></span>
                </div>
              </template>
            </li>
          </ol>

          <!-- Composer -->
          <div class="relative mt-5 flex gap-3">
            <Avatar :name="auth.user?.name ?? '?'" :src="auth.user?.avatar" :size="32" />
            <div class="card min-w-0 flex-1 overflow-visible">
              <textarea
                ref="composer"
                v-model="draft"
                class="block min-h-24 w-full resize-y rounded-t-xl bg-transparent px-3 py-2.5 text-[13.5px] outline-none placeholder:text-faint"
                placeholder="Leave a comment — @mention a teammate, `code`, **bold**"
                aria-label="Comment"
                @input="onComposerInput"
                @keydown="onComposerKey"
              />
              <div v-if="mention && mentionMatches.length" class="absolute top-full left-11 z-10 mt-1 w-60 rounded-lg border border-border bg-surface p-1 shadow-(--shadow-pop)">
                <button
                  v-for="(u, i) in mentionMatches"
                  :key="u.id"
                  class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-[13px]"
                  :class="i === mentionIndex ? 'bg-surface-2' : ''"
                  @mousedown.prevent="pickMention(u)"
                >
                  <Avatar :name="u.name" :src="u.avatar" :size="18" /> <span class="truncate">{{ u.name }}</span> <span class="text-faint">@{{ u.handle }}</span>
                </button>
              </div>
              <div class="flex items-center justify-between gap-3 border-t border-border px-3 py-2">
                <label v-if="issue.githubNumber" class="flex items-center gap-1.5 text-xs text-muted">
                  <input v-model="postToGithub" type="checkbox" class="accent-(--fg)" /> Also post to GitHub
                </label>
                <span v-else class="text-xs text-faint">⌘/Ctrl + Enter to send</span>
                <button class="btn btn-primary btn-sm" :disabled="posting || !draft.trim()" @click="comment">Comment</button>
              </div>
            </div>
          </div>
        </section>
      </div>

      <!-- Sidebar -->
      <aside class="space-y-5 text-[13px] lg:sticky lg:top-20 lg:self-start">
        <section>
          <div class="mb-2 flex items-center justify-between">
            <h3 class="font-medium text-muted">Assignee</h3>
            <Popover v-model:open="assignOpen" placement="bottom-end" width="15rem">
              <template #trigger="{ toggle }">
                <button class="btn btn-ghost btn-sm h-6 px-2 text-xs" @click="toggle">Edit</button>
              </template>
              <div class="p-1">
                <button
                  v-for="u in team.data.value ?? []"
                  :key="u.id"
                  class="menu-item"
                  @click="assign(u.id)"
                >
                  <Avatar :name="u.name" :src="u.avatar" :size="20" />
                  <span class="min-w-0 flex-1 truncate">{{ u.name }}</span>
                  <span v-if="u.id === auth.user?.id" class="text-xs text-faint">you</span>
                </button>
                <div class="my-1 border-t border-border" />
                <button class="menu-item text-muted" @click="assign(0)">Unassign</button>
              </div>
            </Popover>
          </div>
          <RouterLink v-if="issue.assignee" :to="`/team/${issue.assignee.id}`" class="flex items-center gap-2 hover:underline">
            <Avatar :name="issue.assignee.name" :src="issue.assignee.avatar" :size="22" /> {{ issue.assignee.name }}
          </RouterLink>
          <div v-else class="text-muted">
            No one —
            <button class="link" @click="assign(auth.user!.id)">assign yourself</button>
          </div>
        </section>

        <section class="border-t border-border pt-5">
          <h3 class="mb-2 font-medium text-muted">GitHub</h3>
          <template v-if="issue.githubUrl">
            <a :href="issue.githubUrl" target="_blank" rel="noopener noreferrer" class="flex items-center gap-2 hover:underline">
              <GitPullRequestArrow class="size-4" :class="github?.state === 'closed' ? 'text-accent' : 'text-success'" />
              #{{ issue.githubNumber }}
              <Badge v-if="github" :tone="github.state === 'closed' ? 'accent' : 'success'">{{ github.state }}</Badge>
              <ExternalLink class="size-3 text-faint" />
            </a>
            <p v-if="githubError" class="hint text-warning">{{ githubError }}</p>
          </template>
          <template v-else-if="project?.githubRepo && project.hasGithubToken">
            <button class="btn btn-sm w-full" :disabled="busy" @click="createGithubIssue"><GitPullRequestArrow class="size-3.5" /> Create GitHub issue</button>
            <p class="hint">In {{ project.githubRepo }}, with the stack trace and source links.</p>
          </template>
          <p v-else class="text-muted">
            <RouterLink :to="{ name: 'settings', params: { pid }, query: { tab: 'integrations' } }" class="link">Connect a repository</RouterLink>
            to open GitHub issues from here.
          </p>
        </section>

        <section v-if="data.firstRelease" class="border-t border-border pt-5">
          <h3 class="mb-2 font-medium text-muted">Introduced in</h3>
          <RouterLink :to="{ name: 'release', params: { pid, version: data.firstRelease.version } }" class="mono flex items-center gap-1.5 hover:underline">
            <GitCommitHorizontal class="size-4 text-faint" /> {{ data.firstRelease.version }}
          </RouterLink>
          <p v-if="data.firstRelease.message" class="mt-1 line-clamp-2 text-muted">{{ data.firstRelease.message }}</p>
          <a v-if="data.firstRelease.commitUrl" :href="data.firstRelease.commitUrl" target="_blank" rel="noopener noreferrer" class="mt-1 inline-flex items-center gap-1 text-xs text-muted hover:text-fg">
            View commit <ExternalLink class="size-3" />
          </a>
        </section>

        <section v-for="(list, key) in data.breakdown" :key="key" class="border-t border-border pt-5">
          <h3 class="mb-2 font-medium text-muted">{{ breakdownLabels[key] ?? key }}</h3>
          <div v-if="list.length === 0" class="text-faint">—</div>
          <div v-for="t in list" :key="t.value" class="mb-1.5">
            <div class="mb-0.5 flex justify-between gap-2 text-xs">
              <span class="mono truncate">{{ key === 'server' ? shortId(t.value) : t.value || '(none)' }}</span>
              <span class="tabular text-muted">{{ Math.round((t.count / total(list)) * 100) }}%</span>
            </div>
            <div class="h-1 overflow-hidden rounded-full bg-surface-3">
              <div class="h-full origin-left rounded-full bg-fg/70" :style="{ transform: `scaleX(${t.count / total(list)})` }" />
            </div>
          </div>
        </section>
      </aside>
    </div>
  </template>
</template>
