<script setup lang="ts">
import {
  Activity,
  BarChart3,
  Bell,
  Bug,
  Check,
  ChevronsUpDown,
  Download,
  Gauge,
  GitCommitHorizontal,
  LayoutDashboard,
  LayoutGrid,
  LogOut,
  Menu,
  Monitor,
  Moon,
  Plus,
  ScrollText,
  Search,
  Server,
  Settings,
  ShieldAlert,
  Siren,
  Sun,
  UserRound,
  Users,
  Waves,
  X,
  Zap,
} from 'lucide-vue-next'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api } from '@/lib/api'
import { openPalette } from '@/lib/palette'
import { canInstall, install } from '@/lib/pwa'
import { auth, inbox, loadInbox, loadProjects, markRead, prefs, projects, setPrefs } from '@/lib/state'
import Avatar from './Avatar.vue'
import Logo from './Logo.vue'
import Popover from './Popover.vue'
import RelTime from './RelTime.vue'

const props = defineProps<{ pid?: number; projectName?: string; crumb?: string }>()
const route = useRoute()
const router = useRouter()
const navOpen = ref(false)
const switcherOpen = ref(false)
const inboxOpen = ref(false)
const menuOpen = ref(false)

watch(() => route.fullPath, () => (navOpen.value = false))

onMounted(() => {
  if (projects.value.length === 0) loadProjects().catch(() => {})
  loadInbox().catch(() => {})
})
const inboxTimer = setInterval(() => {
  if (document.visibilityState === 'visible') loadInbox().catch(() => {})
}, 60_000)
const projectTimer = setInterval(() => {
  if (document.visibilityState === 'visible' && props.pid) loadProjects().catch(() => {})
}, 30_000)
onBeforeUnmount(() => {
  clearInterval(inboxTimer)
  clearInterval(projectTimer)
})

const current = computed(() => projects.value.find((p) => p.id === props.pid))

type NavItem = { name: string; label: string; icon: unknown; match?: string[]; count?: number }
const projectNav = computed<NavItem[][]>(() => [
  [
    { name: 'overview', label: 'Overview', icon: LayoutDashboard },
    { name: 'issues', label: 'Issues', icon: Bug, match: ['issue'], count: current.value?.openIssues },
    { name: 'logs', label: 'Logs', icon: ScrollText },
    { name: 'sessions', label: 'Sessions', icon: Waves, match: ['session'] },
    { name: 'players', label: 'Players', icon: Users, match: ['player'] },
  ],
  [
    { name: 'performance', label: 'Performance', icon: Gauge },
    { name: 'servers', label: 'Servers', icon: Server, match: ['server'] },
    { name: 'releases', label: 'Releases', icon: GitCommitHorizontal, match: ['release'] },
    { name: 'security', label: 'Security', icon: ShieldAlert },
  ],
  [
    { name: 'analytics', label: 'Analytics', icon: BarChart3 },
    { name: 'events', label: 'Events', icon: Zap },
    { name: 'metrics', label: 'Metrics', icon: Activity },
  ],
  [
    { name: 'alerts', label: 'Alerts', icon: Siren },
    { name: 'settings', label: 'Settings', icon: Settings },
  ],
])
const teamNav: NavItem[][] = [
  [
    { name: 'projects', label: 'Projects', icon: LayoutGrid },
    { name: 'team', label: 'Team', icon: Users, match: ['member'] },
    { name: 'inbox', label: 'Inbox', icon: Bell },
    { name: 'profile', label: 'Profile & preferences', icon: UserRound },
  ],
]
const nav = computed(() => (props.pid ? projectNav.value : teamNav))

function isActive(item: NavItem) {
  return route.name === item.name || (item.match?.includes(String(route.name)) ?? false)
}
function to(item: NavItem) {
  return props.pid ? { name: item.name, params: { pid: props.pid } } : { name: item.name }
}

function switchTo(id: number) {
  switcherOpen.value = false
  const keep = typeof route.name === 'string' && props.pid && !['issue', 'server', 'player', 'session', 'release'].includes(route.name)
  router.push({ name: keep ? (route.name as string) : 'overview', params: { pid: id } })
}

async function logout() {
  await api.post('/auth/logout')
  auth.user = null
  router.replace('/login')
}

function openNote(n: (typeof inbox.notifications)[number]) {
  inboxOpen.value = false
  if (!n.readAt) markRead([n.id]).catch(() => {})
  if (n.issueId) router.push({ name: 'issue', params: { pid: n.projectId, iid: n.issueId } })
}
const noteVerb: Record<string, string> = {
  mention: 'mentioned you on',
  assigned: 'assigned you',
  comment: 'commented on',
  regression: 'Regression in',
}

const themes = [
  { value: 'system', icon: Monitor, label: 'System' },
  { value: 'light', icon: Sun, label: 'Light' },
  { value: 'dark', icon: Moon, label: 'Dark' },
] as const

const tabs = computed(() => [
  { name: 'overview', label: 'Overview', icon: LayoutDashboard },
  { name: 'issues', label: 'Issues', icon: Bug, match: ['issue'], count: current.value?.openIssues },
  { name: 'logs', label: 'Logs', icon: ScrollText },
  { name: 'sessions', label: 'Sessions', icon: Waves, match: ['session'] },
])
</script>

<template>
  <div class="min-h-dvh lg:flex">
    <!-- Mobile header -->
    <div
      class="sticky top-0 z-30 flex h-[calc(3rem+env(safe-area-inset-top))] items-center gap-2 border-b border-border bg-bg/85 px-3 pt-[env(safe-area-inset-top)] backdrop-blur lg:hidden"
    >
      <button class="btn btn-ghost btn-icon" aria-label="Open menu" @click="navOpen = true"><Menu class="size-4" /></button>
      <Logo class="size-6" />
      <span class="truncate text-sm font-medium">{{ projectName ?? 'Workshell' }}</span>
      <button class="btn btn-ghost btn-icon ml-auto" aria-label="Search" @click="openPalette()"><Search class="size-4" /></button>
    </div>

    <Transition name="fade">
      <div v-if="navOpen" class="fixed inset-0 z-40 bg-(--overlay) lg:hidden" @click="navOpen = false" />
    </Transition>

    <aside
      class="fixed inset-y-0 left-0 z-50 flex w-64 flex-col border-r border-border bg-bg pt-[env(safe-area-inset-top)] pb-[env(safe-area-inset-bottom)] pl-[env(safe-area-inset-left)] transition-transform duration-[400ms] ease-(--ease-drawer) motion-reduce:transition-none lg:sticky lg:top-0 lg:h-dvh lg:w-60 lg:translate-x-0 lg:transition-none"
      :class="navOpen ? 'translate-x-0' : '-translate-x-full'"
    >
      <!-- Scope switcher -->
      <div class="flex items-center gap-1 p-2.5">
        <Popover v-model:open="switcherOpen" width="15rem">
          <template #trigger="{ toggle }">
            <button
              class="flex min-w-0 flex-1 cursor-pointer items-center gap-2 rounded-md px-1.5 py-1.5 text-left transition-colors hover:bg-surface-2"
              :aria-expanded="switcherOpen"
              @click="toggle"
            >
              <Logo class="size-[22px] shrink-0" />
              <span class="min-w-0 flex-1 truncate text-[13.5px] font-medium">{{ projectName ?? 'Workshell' }}</span>
              <ChevronsUpDown class="size-3.5 shrink-0 text-faint" />
            </button>
          </template>
          <div class="p-1">
            <div class="px-2 pt-1.5 pb-1 text-xs text-faint">Projects</div>
            <button
              v-for="p in projects"
              :key="p.id"
              class="flex w-full cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-left text-[13px] hover:bg-surface-2"
              @click="switchTo(p.id)"
            >
              <span class="dot" :class="p.liveServers ? 'bg-success' : 'bg-faint/40'" />
              <span class="min-w-0 flex-1 truncate">{{ p.name }}</span>
              <span v-if="p.livePlayers" class="tabular text-xs text-success">{{ p.livePlayers }}</span>
              <Check v-if="p.id === pid" class="size-3.5 text-fg" />
            </button>
            <div class="my-1 border-t border-border" />
            <RouterLink to="/" class="flex items-center gap-2 rounded-md px-2 py-1.5 text-[13px] text-muted hover:bg-surface-2 hover:text-fg" @click="switcherOpen = false">
              <LayoutGrid class="size-4" /> All projects
            </RouterLink>
            <RouterLink to="/?new=1" class="flex items-center gap-2 rounded-md px-2 py-1.5 text-[13px] text-muted hover:bg-surface-2 hover:text-fg" @click="switcherOpen = false">
              <Plus class="size-4" /> New project
            </RouterLink>
          </div>
        </Popover>
        <button class="btn btn-ghost btn-icon shrink-0 lg:hidden" aria-label="Close menu" @click="navOpen = false"><X class="size-4" /></button>
      </div>

      <div class="px-2.5 pb-2">
        <button
          class="flex h-8 w-full cursor-pointer items-center gap-2 rounded-md border border-border bg-surface px-2.5 text-[13px] text-faint transition-colors hover:border-border-strong"
          @click="openPalette()"
        >
          <Search class="size-3.5" />
          <span class="flex-1 text-left">Find…</span>
          <span class="kbd">⌘K</span>
        </button>
      </div>

      <nav class="flex-1 overflow-y-auto overscroll-contain px-2.5 pb-3">
        <template v-for="(group, gi) in nav" :key="gi">
          <div v-if="gi > 0" class="mx-2 my-2 border-t border-border" />
          <RouterLink
            v-for="item in group"
            :key="item.name"
            :to="to(item)"
            class="flex h-8 items-center gap-2.5 rounded-md px-2 text-[13.5px] transition-colors duration-150 pointer-coarse:h-10 pointer-coarse:text-[15px]"
            :class="isActive(item) ? 'bg-surface-3 font-medium text-fg' : 'text-muted hover:bg-surface-2 hover:text-fg'"
          >
            <component :is="item.icon" class="size-4 shrink-0" :stroke-width="isActive(item) ? 2.1 : 1.8" />
            <span class="flex-1 truncate">{{ item.label }}</span>
            <span v-if="item.count" class="tabular rounded-full bg-surface-3 px-1.5 text-[11px] text-muted">{{ item.count }}</span>
          </RouterLink>
        </template>
      </nav>

      <!-- User -->
      <div class="flex items-center gap-1 border-t border-border p-2.5">
        <Popover v-model:open="menuOpen" placement="top-start" width="14rem">
          <template #trigger="{ toggle }">
            <button class="flex min-w-0 flex-1 cursor-pointer items-center gap-2 rounded-md px-1.5 py-1 hover:bg-surface-2" @click="toggle">
              <Avatar :name="auth.user?.name ?? '?'" :src="auth.user?.avatar" :size="24" />
              <span class="min-w-0 flex-1 truncate text-left text-[13px]">{{ auth.user?.name }}</span>
            </button>
          </template>
          <div class="p-1 text-[13px]">
            <div class="px-2 py-1.5">
              <div class="truncate font-medium">{{ auth.user?.name }}</div>
              <div class="truncate text-xs text-muted">@{{ auth.user?.handle }}</div>
            </div>
            <div class="my-1 border-t border-border" />
            <RouterLink to="/profile" class="flex items-center gap-2 rounded-md px-2 py-1.5 hover:bg-surface-2" @click="menuOpen = false">
              <UserRound class="size-4 text-muted" /> Profile & preferences
            </RouterLink>
            <RouterLink to="/team" class="flex items-center gap-2 rounded-md px-2 py-1.5 hover:bg-surface-2" @click="menuOpen = false">
              <Users class="size-4 text-muted" /> Team
            </RouterLink>
            <div class="flex items-center justify-between gap-2 px-2 py-1.5">
              <span class="text-muted">Theme</span>
              <div class="flex rounded-md border border-border p-0.5">
                <button
                  v-for="t in themes"
                  :key="t.value"
                  class="grid size-6 cursor-pointer place-items-center rounded"
                  :class="prefs.theme === t.value ? 'bg-surface-3 text-fg' : 'text-faint hover:text-fg'"
                  :title="t.label"
                  :aria-pressed="prefs.theme === t.value"
                  @click="setPrefs({ theme: t.value })"
                >
                  <component :is="t.icon" class="size-3.5" />
                </button>
              </div>
            </div>
            <RouterLink to="/profile" class="block px-2 pb-1.5 text-xs text-muted hover:text-fg" @click="menuOpen = false">More themes — Catppuccin, Rosé Pine, Nord…</RouterLink>
            <button v-if="canInstall" class="flex w-full cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-left hover:bg-surface-2" @click="menuOpen = false; install()">
              <Download class="size-4 text-muted" /> Install app
            </button>
            <div class="my-1 border-t border-border" />
            <button class="flex w-full cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-left hover:bg-surface-2" @click="logout">
              <LogOut class="size-4 text-muted" /> Sign out
            </button>
          </div>
        </Popover>

        <Popover v-model:open="inboxOpen" placement="top-start" width="22rem">
          <template #trigger="{ toggle }">
            <button class="btn btn-ghost btn-icon relative shrink-0" aria-label="Notifications" @click="toggle">
              <Bell class="size-4" />
              <span v-if="inbox.unread" class="absolute top-1.5 right-1.5 size-2 rounded-full bg-accent ring-2 ring-bg" />
            </button>
          </template>
          <div>
            <div class="flex items-center justify-between border-b border-border px-3 py-2.5">
              <span class="text-[13px] font-medium">Inbox</span>
              <button v-if="inbox.unread" class="text-xs text-muted hover:text-fg" @click="markRead()">Mark all read</button>
            </div>
            <div v-if="inbox.notifications.length === 0" class="px-4 py-10 text-center text-[13px] text-muted">You're all caught up.</div>
            <button
              v-for="n in inbox.notifications.slice(0, 12)"
              :key="n.id"
              class="flex w-full cursor-pointer gap-2.5 border-b border-border px-3 py-2.5 text-left last:border-0 hover:bg-surface-2"
              @click="openNote(n)"
            >
              <Avatar v-if="n.actorName" :name="n.actorName" :src="n.actorAvatar" :size="22" />
              <span v-else class="grid size-[22px] shrink-0 place-items-center rounded-full bg-warning-soft text-warning"><Bug class="size-3" /></span>
              <div class="min-w-0 flex-1 text-[13px]">
                <div>
                  <span v-if="n.actorName" class="font-medium">{{ n.actorName }} </span>
                  <span class="text-muted">{{ noteVerb[n.kind] ?? n.kind }}</span>
                  <span class="font-medium"> {{ n.issueTitle }}</span>
                </div>
                <div v-if="n.kind === 'mention' || n.kind === 'comment'" class="mt-0.5 line-clamp-2 text-xs text-muted">{{ n.body }}</div>
                <div class="mt-0.5 text-[11px] text-faint"><RelTime :ts="n.createdAt" /></div>
              </div>
              <span v-if="!n.readAt" class="mt-1.5 size-2 shrink-0 rounded-full bg-accent" />
            </button>
            <RouterLink to="/inbox" class="block border-t border-border px-3 py-2 text-center text-xs text-muted hover:text-fg" @click="inboxOpen = false">
              View all
            </RouterLink>
          </div>
        </Popover>
      </div>
    </aside>

    <main class="min-w-0 flex-1">
      <!-- Top bar -->
      <div class="sticky top-0 z-20 hidden h-12 items-center gap-2 border-b border-border bg-bg/85 px-6 text-[13px] backdrop-blur lg:flex">
        <RouterLink to="/" class="text-muted hover:text-fg">Projects</RouterLink>
        <template v-if="projectName">
          <span class="text-faint">/</span>
          <RouterLink :to="{ name: 'overview', params: { pid } }" class="truncate text-muted hover:text-fg">{{ projectName }}</RouterLink>
        </template>
        <template v-if="crumb">
          <span class="text-faint">/</span>
          <span class="truncate text-fg">{{ crumb }}</span>
        </template>
        <div class="ml-auto flex items-center gap-3">
          <span v-if="current" class="flex items-center gap-1.5 text-muted">
            <span class="dot" :class="current.liveServers ? 'bg-success' : 'bg-faint/50'" />
            <span class="tabular">{{ current.livePlayers }}</span> playing
          </span>
          <slot name="topbar" />
        </div>
      </div>
      <div
        class="mx-auto max-w-[1240px] px-4 py-6 sm:px-6 lg:px-8 lg:py-8"
        :class="pid ? 'max-lg:pb-[calc(5rem+env(safe-area-inset-bottom))]' : 'max-lg:pb-[calc(1.5rem+env(safe-area-inset-bottom))]'"
      >
        <slot />
      </div>
    </main>

    <!-- Mobile tab bar for the most-used project pages -->
    <nav
      v-if="pid"
      class="fixed inset-x-0 bottom-0 z-30 grid grid-cols-5 border-t border-border bg-bg/90 pb-[env(safe-area-inset-bottom)] backdrop-blur lg:hidden"
      aria-label="Project"
    >
      <RouterLink
        v-for="t in tabs"
        :key="t.name"
        :to="{ name: t.name, params: { pid } }"
        class="relative flex h-14 flex-col items-center justify-center gap-0.5 text-[11px] transition-transform duration-150 ease-(--ease-out) active:scale-95"
        :class="isActive(t) ? 'text-fg' : 'text-faint'"
      >
        <component :is="t.icon" class="size-5" :stroke-width="isActive(t) ? 2.2 : 1.8" />
        {{ t.label }}
        <span v-if="t.count" class="tabular absolute top-1.5 left-[calc(50%+6px)] rounded-full bg-danger px-1 text-[10px] leading-4 text-white">{{ t.count }}</span>
      </RouterLink>
      <button
        class="flex h-14 flex-col items-center justify-center gap-0.5 text-[11px] text-faint transition-transform duration-150 ease-(--ease-out) active:scale-95"
        @click="navOpen = true"
      >
        <Menu class="size-5" /> More
      </button>
    </nav>
  </div>
</template>
