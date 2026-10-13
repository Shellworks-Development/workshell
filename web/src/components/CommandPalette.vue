<script setup lang="ts">
import {
  Bug,
  CornerDownLeft,
  GitCommitHorizontal,
  LayoutGrid,
  Search,
  Server,
  User,
  Waves,
  Zap,
  ArrowRight,
} from 'lucide-vue-next'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '@/lib/api'
import { paletteOpen } from '@/lib/palette'
import { projects } from '@/lib/state'

/*
 * ⌘K / Ctrl+K (or F) command palette. Opened many times a day from the
 * keyboard, so it deliberately has no open/close animation.
 */
interface Hit {
  kind: string
  id: string
  title: string
  detail?: string
}
interface Entry {
  group: string
  title: string
  detail?: string
  icon: unknown
  go: () => void
}

const router = useRouter()
const route = useRoute()
const query = ref('')
const hits = ref<Hit[]>([])
const active = ref(0)
const input = ref<HTMLInputElement | null>(null)
const pid = computed(() => (route.params.pid ? Number(route.params.pid) : null))

const pages = [
  ['overview', 'Overview'],
  ['issues', 'Issues'],
  ['logs', 'Logs'],
  ['sessions', 'Sessions'],
  ['players', 'Players'],
  ['performance', 'Performance'],
  ['servers', 'Servers'],
  ['releases', 'Releases'],
  ['security', 'Security'],
  ['analytics', 'Analytics'],
  ['events', 'Events'],
  ['metrics', 'Metrics'],
  ['alerts', 'Alerts'],
  ['settings', 'Settings'],
] as const

const kindIcon: Record<string, unknown> = { issue: Bug, player: User, session: Waves, server: Server, release: GitCommitHorizontal, event: Zap }
const kindLabel: Record<string, string> = { issue: 'Issues', player: 'Players', session: 'Sessions', server: 'Servers', release: 'Releases', event: 'Events' }

function go(to: Parameters<typeof router.push>[0]) {
  paletteOpen.value = false
  router.push(to)
}

const entries = computed<Entry[]>(() => {
  const q = query.value.trim().toLowerCase()
  const out: Entry[] = []
  if (pid.value) {
    for (const [name, label] of pages) {
      if (!q || label.toLowerCase().includes(q)) out.push({ group: 'Go to', title: label, icon: ArrowRight, go: () => go({ name, params: { pid: pid.value } }) })
    }
  }
  for (const p of projects.value) {
    if (!q || p.name.toLowerCase().includes(q))
      out.push({ group: 'Projects', title: p.name, detail: p.livePlayers ? `${p.livePlayers} playing` : undefined, icon: LayoutGrid, go: () => go({ name: 'overview', params: { pid: p.id } }) })
  }
  for (const h of hits.value) {
    const routes: Record<string, () => void> = {
      issue: () => go({ name: 'issue', params: { pid: pid.value, iid: h.id } }),
      player: () => go({ name: 'player', params: { pid: pid.value, userId: h.id } }),
      session: () => go({ name: 'session', params: { pid: pid.value, sid: h.id } }),
      server: () => go({ name: 'server', params: { pid: pid.value, jobId: h.id } }),
      release: () => go({ name: 'release', params: { pid: pid.value, version: h.id } }),
      event: () => go({ name: 'events', params: { pid: pid.value }, query: { name: h.id } }),
    }
    out.push({ group: kindLabel[h.kind] ?? h.kind, title: h.title, detail: h.detail, icon: kindIcon[h.kind] ?? Search, go: routes[h.kind] ?? (() => {}) })
  }
  return out.slice(0, 40)
})

const grouped = computed(() => {
  const groups: { name: string; items: { e: Entry; i: number }[] }[] = []
  entries.value.forEach((e, i) => {
    let g = groups.find((x) => x.name === e.group)
    if (!g) groups.push((g = { name: e.group, items: [] }))
    g.items.push({ e, i })
  })
  return groups
})

let timer: ReturnType<typeof setTimeout> | undefined
let seq = 0
watch(query, (q) => {
  active.value = 0
  clearTimeout(timer)
  if (!pid.value || q.trim().length < 2) {
    hits.value = []
    return
  }
  timer = setTimeout(async () => {
    const id = ++seq
    try {
      const res = await api.get<Hit[]>(`/projects/${pid.value}/search`, { q: q.trim() })
      if (id === seq) hits.value = res
    } catch {
      // ignore
    }
  }, 150)
})

watch(paletteOpen, async (o) => {
  if (o) {
    query.value = ''
    hits.value = []
    active.value = 0
    await nextTick()
    input.value?.focus()
  }
})

function onKey(e: KeyboardEvent) {
  const n = entries.value.length
  if (e.key === 'ArrowDown') {
    e.preventDefault()
    active.value = (active.value + 1) % Math.max(n, 1)
  } else if (e.key === 'ArrowUp') {
    e.preventDefault()
    active.value = (active.value - 1 + n) % Math.max(n, 1)
  } else if (e.key === 'Enter') {
    e.preventDefault()
    entries.value[active.value]?.go()
  } else if (e.key === 'Escape') {
    paletteOpen.value = false
  }
  nextTick(() => document.querySelector(`[data-palette-index="${active.value}"]`)?.scrollIntoView({ block: 'nearest' }))
}

function globalKey(e: KeyboardEvent) {
  const target = e.target as HTMLElement
  const typing = target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName)
  if ((e.key === 'k' && (e.metaKey || e.ctrlKey)) || (e.key === 'f' && !typing && !e.metaKey && !e.ctrlKey && !e.altKey)) {
    e.preventDefault()
    paletteOpen.value = !paletteOpen.value
  }
}
onMounted(() => document.addEventListener('keydown', globalKey))
onBeforeUnmount(() => document.removeEventListener('keydown', globalKey))
</script>

<template>
  <Teleport to="body">
    <div v-if="paletteOpen" class="fixed inset-0 z-[95] bg-(--overlay)" @click="paletteOpen = false" />
    <div v-if="paletteOpen" class="fixed inset-x-0 top-[max(12dvh,calc(env(safe-area-inset-top)+0.75rem))] z-[96] mx-auto w-[min(36rem,calc(100vw-2rem))]" role="dialog" aria-modal="true" aria-label="Find">
      <div class="overflow-hidden rounded-xl border border-border bg-surface shadow-(--shadow-pop)">
        <div class="flex items-center gap-2.5 border-b border-border px-4">
          <Search class="size-4 text-faint" />
          <input
            ref="input"
            v-model="query"
            class="h-12 flex-1 bg-transparent text-[15px] outline-none placeholder:text-faint"
            :placeholder="pid ? 'Search issues, players, sessions, servers, releases…' : 'Search projects…'"
            role="combobox"
            aria-expanded="true"
            @keydown="onKey"
          />
          <span class="kbd">esc</span>
        </div>
        <div class="max-h-[min(26rem,60dvh)] overflow-y-auto overscroll-contain p-1.5">
          <div v-if="entries.length === 0" class="px-3 py-8 text-center text-[13px] text-muted">No results for “{{ query }}”.</div>
          <div v-for="g in grouped" :key="g.name" class="mb-1">
            <div class="px-2.5 pt-2 pb-1 text-xs text-faint">{{ g.name }}</div>
            <button
              v-for="{ e, i } in g.items"
              :key="i"
              :data-palette-index="i"
              class="flex w-full cursor-pointer items-center gap-2.5 rounded-md px-2.5 py-2 text-left text-[13.5px]"
              :class="i === active ? 'bg-surface-2 text-fg' : 'text-muted'"
              @pointermove="active = i"
              @click="e.go()"
            >
              <component :is="e.icon" class="size-4 shrink-0" />
              <span class="min-w-0 truncate" :class="i === active ? 'text-fg' : 'text-fg/90'">{{ e.title }}</span>
              <span v-if="e.detail" class="min-w-0 flex-1 truncate font-mono text-xs text-faint">{{ e.detail }}</span>
              <span v-else class="flex-1" />
              <CornerDownLeft v-if="i === active" class="size-3.5 shrink-0 text-faint" />
            </button>
          </div>
        </div>
      </div>
    </div>
  </Teleport>
</template>
