<script setup lang="ts">
import { Bug, GitCommitHorizontal, LayoutGrid, List, Plus, Rocket, Search } from 'lucide-vue-next'
import { computed, onMounted, ref } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import Avatar from '@/components/Avatar.vue'
import EmptyState from '@/components/EmptyState.vue'
import ErrorBanner from '@/components/ErrorBanner.vue'
import Modal from '@/components/Modal.vue'
import RelTime from '@/components/RelTime.vue'
import Skeleton from '@/components/Skeleton.vue'
import Sparkline from '@/components/Sparkline.vue'
import { api } from '@/lib/api'
import { num } from '@/lib/format'
import { toast } from '@/lib/overlay'
import { auth, inbox, loadProjects, projects } from '@/lib/state'

const router = useRouter()
const route = useRoute()
const loaded = ref(false)
const error = ref<string | null>(null)
const search = ref('')
const view = ref<'grid' | 'list'>((localStorage.getItem('workshell-projects-view') as 'grid' | 'list') || 'grid')
function setView(v: 'grid' | 'list') {
  view.value = v
  try {
    localStorage.setItem('workshell-projects-view', v)
  } catch {
    // ignore
  }
}

onMounted(async () => {
  try {
    await loadProjects()
  } catch (e) {
    error.value = (e as Error).message
  }
  loaded.value = true
  if (route.query.new === '1') openCreate()
})

const filtered = computed(() => {
  const q = search.value.trim().toLowerCase()
  return q ? projects.value.filter((p) => p.name.toLowerCase().includes(q)) : projects.value
})
const totals = computed(() => ({
  players: projects.value.reduce((a, p) => a + p.livePlayers, 0),
  servers: projects.value.reduce((a, p) => a + p.liveServers, 0),
  issues: projects.value.reduce((a, p) => a + p.openIssues, 0),
}))

// ---- Create -------------------------------------------------------------------
const creating = ref(false)
const name = ref('')
const createError = ref<string | null>(null)
const busy = ref(false)
function openCreate() {
  name.value = ''
  createError.value = null
  creating.value = true
}
async function create() {
  createError.value = null
  busy.value = true
  try {
    const { id } = await api.post<{ id: number }>('/projects', { name: name.value })
    await loadProjects()
    creating.value = false
    toast(`Created ${name.value}`, 'Next: connect your game.')
    router.push(`/p/${id}/settings?tab=sdk&welcome=1`)
  } catch (e) {
    createError.value = (e as Error).message
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="mb-6 flex flex-wrap items-center gap-2">
    <div class="relative min-w-60 flex-1">
      <Search class="pointer-events-none absolute top-2 left-2.5 size-4 text-faint" />
      <input v-model="search" class="input h-9 pl-8" placeholder="Search projects…" aria-label="Search projects" />
    </div>
    <div class="flex rounded-md border border-border bg-surface p-0.5">
      <button class="grid size-7 cursor-pointer place-items-center rounded" :class="view === 'grid' ? 'bg-surface-3' : 'text-faint'" aria-label="Grid view" @click="setView('grid')">
        <LayoutGrid class="size-4" />
      </button>
      <button class="grid size-7 cursor-pointer place-items-center rounded" :class="view === 'list' ? 'bg-surface-3' : 'text-faint'" aria-label="List view" @click="setView('list')">
        <List class="size-4" />
      </button>
    </div>
    <button class="btn btn-primary h-9" @click="openCreate"><Plus class="size-4" /> New project</button>
  </div>

  <ErrorBanner :message="error" />

  <div class="grid gap-6 lg:grid-cols-[17rem_1fr]">
    <!-- Left column: summary & inbox -->
    <div class="space-y-6">
      <section>
        <h2 class="section-title">Right now</h2>
        <div class="card divide-y divide-border">
          <div class="flex items-center justify-between px-4 py-2.5 text-[13px]">
            <span class="flex items-center gap-2 text-muted"><span class="dot bg-success" /> Playing</span>
            <span class="tabular font-medium">{{ num(totals.players) }}</span>
          </div>
          <div class="flex items-center justify-between px-4 py-2.5 text-[13px]">
            <span class="text-muted">Live servers</span>
            <span class="tabular font-medium">{{ num(totals.servers) }}</span>
          </div>
          <div class="flex items-center justify-between px-4 py-2.5 text-[13px]">
            <span class="text-muted">Open issues</span>
            <span class="tabular font-medium">{{ num(totals.issues) }}</span>
          </div>
        </div>
      </section>
      <section>
        <h2 class="section-title">Inbox</h2>
        <div class="card">
          <div v-if="inbox.notifications.length === 0" class="px-4 py-6 text-center text-[13px] text-muted">
            Mentions and assignments for you, {{ auth.user?.name.split(' ')[0] }}, show up here.
          </div>
          <RouterLink
            v-for="n in inbox.notifications.slice(0, 4)"
            :key="n.id"
            :to="{ name: 'issue', params: { pid: n.projectId, iid: n.issueId } }"
            class="flex gap-2 border-b border-border px-3 py-2.5 text-[13px] last:border-0 hover:bg-surface-2"
          >
            <Avatar :name="n.actorName || 'Workshell'" :src="n.actorAvatar" :size="20" />
            <div class="min-w-0">
              <div class="truncate">{{ n.issueTitle }}</div>
              <div class="text-[11px] text-faint">{{ n.actorName || 'Regression' }} · <RelTime :ts="n.createdAt" /></div>
            </div>
          </RouterLink>
          <RouterLink v-if="inbox.notifications.length" to="/inbox" class="block border-t border-border px-3 py-2 text-center text-xs text-muted hover:text-fg">
            Open inbox
          </RouterLink>
        </div>
      </section>
    </div>

    <!-- Projects -->
    <section class="min-w-0">
      <h2 class="section-title">Projects</h2>

      <div v-if="!loaded" class="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
        <div v-for="i in 3" :key="i" class="card space-y-3 p-4"><Skeleton w="50%" /><Skeleton w="30%" h="0.7rem" /><Skeleton h="2rem" /></div>
      </div>

      <div v-else-if="projects.length === 0" class="card">
        <EmptyState title="Create your first project" :icon="Rocket">
          One project per Roblox experience. You'll get an ingest key and a two-line SDK setup.
          <div class="mt-4"><button class="btn btn-primary" @click="openCreate"><Plus class="size-4" /> New project</button></div>
        </EmptyState>
      </div>

      <div v-else-if="filtered.length === 0" class="card px-4 py-10 text-center text-[13px] text-muted">No projects match “{{ search }}”.</div>

      <div v-else-if="view === 'grid'" class="stagger grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
        <RouterLink
          v-for="p in filtered"
          :key="p.id"
          :to="`/p/${p.id}`"
          class="card group flex flex-col p-4 transition-[border-color,transform] duration-150 ease-(--ease-out) hover:border-border-strong active:scale-[0.99]"
        >
          <div class="flex items-start gap-3">
            <div class="min-w-0 flex-1">
              <div class="truncate font-medium">{{ p.name }}</div>
              <div class="flex items-center gap-1.5 text-[13px] text-muted">
                <span class="dot" :class="p.liveServers ? 'bg-success' : 'bg-faint/40'" />
                {{ p.liveServers ? `${num(p.livePlayers)} playing · ${p.liveServers} server${p.liveServers === 1 ? '' : 's'}` : 'Offline' }}
              </div>
            </div>
          </div>
          <Sparkline :values="p.activity" :width="260" :height="34" color="var(--accent)" class="mt-4 w-full" />
          <div class="mt-3 flex items-center gap-3 text-xs text-muted">
            <span class="flex items-center gap-1" :class="p.openIssues ? 'text-danger' : ''"><Bug class="size-3.5" /> {{ num(p.openIssues) }}</span>
            <span v-if="p.latestRelease" class="flex min-w-0 items-center gap-1 font-mono"><GitCommitHorizontal class="size-3.5 shrink-0" /> <span class="truncate">{{ p.latestRelease }}</span></span>
            <span class="ml-auto shrink-0"><RelTime v-if="p.lastEventAt" :ts="p.lastEventAt" /><template v-else>No data yet</template></span>
          </div>
        </RouterLink>
      </div>

      <div v-else class="card overflow-x-auto">
        <table class="table">
          <thead>
            <tr><th>Project</th><th class="text-right">Playing</th><th class="text-right">Open issues</th><th>Release</th><th class="text-right">Last activity</th></tr>
          </thead>
          <tbody>
            <tr v-for="p in filtered" :key="p.id" class="clickable" @click="router.push(`/p/${p.id}`)">
              <td>
                <div class="flex items-center gap-2.5"><span class="dot" :class="p.liveServers ? 'bg-success' : 'bg-faint/40'" /> <span class="font-medium">{{ p.name }}</span></div>
              </td>
              <td class="tabular text-right">{{ num(p.livePlayers) }}</td>
              <td class="tabular text-right" :class="p.openIssues ? 'text-danger' : ''">{{ num(p.openIssues) }}</td>
              <td class="mono text-muted">{{ p.latestRelease || '—' }}</td>
              <td class="text-right text-muted"><RelTime v-if="p.lastEventAt" :ts="p.lastEventAt" /><template v-else>—</template></td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
  </div>

  <Modal :open="creating" title="New project" description="One project per Roblox experience. You'll get an ingest key next." @close="creating = false">
    <form id="create-project" @submit.prevent="create">
      <ErrorBanner :message="createError" />
      <label class="label" for="pname">Project name</label>
      <input id="pname" v-model="name" class="input" placeholder="My Obby" required maxlength="100" data-autofocus />
    </form>
    <template #footer>
      <button class="btn" @click="creating = false">Cancel</button>
      <button class="btn btn-primary" type="submit" form="create-project" :disabled="busy || !name.trim()">Create project</button>
    </template>
  </Modal>
</template>
