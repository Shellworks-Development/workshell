<script setup lang="ts">
import { Download, Eye, EyeOff, KeyRound, RefreshCw, Trash2 } from 'lucide-vue-next'
import { computed, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import Badge from '@/components/Badge.vue'
import CodeBlock from '@/components/CodeBlock.vue'
import CopyButton from '@/components/CopyButton.vue'
import PageHeader from '@/components/PageHeader.vue'
import RelTime from '@/components/RelTime.vue'
import { api, type Project } from '@/lib/api'
import { confirmDialog, toast } from '@/lib/overlay'
import { useProject } from '@/lib/project'
import { auth, loadProjects } from '@/lib/state'
import { useAsync } from '@/lib/useAsync'

const { pid, project, reload } = useProject()
const route = useRoute()
const router = useRouter()

const tabs = [
  { value: 'general', label: 'General' },
  { value: 'sdk', label: 'SDK setup' },
  { value: 'security', label: 'Security' },
  { value: 'integrations', label: 'Integrations' },
  { value: 'audit', label: 'Audit log' },
  { value: 'danger', label: 'Danger zone' },
]
const tab = computed(() => (typeof route.query.tab === 'string' && tabs.some((t) => t.value === route.query.tab) ? route.query.tab : 'general'))
const welcome = computed(() => route.query.welcome === '1')
function setTab(t: string) {
  router.replace({ query: { tab: t } })
}

const form = reactive({ name: '', logRetentionDays: 14, dataRetentionDays: 90, githubRepo: '', githubToken: '', ingestRate: 2000, clientRate: 60 })
watch(
  project,
  (p) => {
    if (!p) return
    Object.assign(form, { name: p.name, logRetentionDays: p.logRetentionDays, dataRetentionDays: p.dataRetentionDays, githubRepo: p.githubRepo, githubToken: '', ingestRate: p.ingestRate, clientRate: p.clientRate })
  },
  { immediate: true },
)

const saving = ref(false)
async function save(fields: Partial<Project> & { githubToken?: string }, message = 'Settings saved') {
  saving.value = true
  try {
    await api.patch(`/projects/${pid.value}`, fields)
    await reload()
    await loadProjects()
    toast(message)
    return true
  } catch (e) {
    toast.error('Couldn’t save settings', (e as Error).message)
    return false
  } finally {
    saving.value = false
  }
}

const showKey = ref(false)
const showSecret = ref(false)
async function rotateKey() {
  const ok = await confirmDialog({
    title: 'Rotate the ingest key?',
    message: 'Game servers using the current key stop reporting until you update the key in your game.',
    confirmLabel: 'Rotate key',
    danger: true,
  })
  if (!ok) return
  await api.post(`/projects/${pid.value}/rotate-key`)
  await reload()
  showKey.value = true
  toast('New ingest key generated', 'Update the key in your game’s Workshell.init call.')
}
async function rotateSecret() {
  if (project.value?.signingSecret) {
    const ok = await confirmDialog({
      title: 'Generate a new signing secret?',
      message: project.value.requireSignature ? 'Signed requests are required, so servers using the old secret stop reporting until updated.' : 'The old secret stops working.',
      confirmLabel: 'Generate',
      danger: true,
    })
    if (!ok) return
  }
  await api.post(`/projects/${pid.value}/signing-secret`)
  await reload()
  showSecret.value = true
  toast('Signing secret generated')
}
async function toggleSignature() {
  await save({ requireSignature: !project.value?.requireSignature }, project.value?.requireSignature ? 'Unsigned requests allowed' : 'Signed requests now required')
}
async function toggleHashing() {
  const enabling = !project.value?.hashUserIds
  if (enabling) {
    const ok = await confirmDialog({
      title: 'Store player ids as HMAC hashes?',
      message:
        'New data stores a keyed hash instead of each Roblox user id. Analytics, sessions and lookups by Roblox id keep working, but Roblox profile links disappear, and existing data keeps raw ids — mixing the two splits players in analytics.',
      confirmLabel: 'Enable',
    })
    if (!ok) return
  }
  await save({ hashUserIds: enabling }, enabling ? 'Player ids will be hashed' : 'Player ids will be stored as-is')
}

async function remove() {
  const name = project.value?.name ?? ''
  const ok = await confirmDialog({
    title: `Delete ${name}?`,
    message: 'This permanently deletes the project and every error, log, event, session and detection it has collected. This can’t be undone.',
    confirmLabel: 'Delete project',
    danger: true,
    typeToConfirm: name,
  })
  if (!ok) return
  await api.del(`/projects/${pid.value}`)
  await loadProjects()
  toast(`Deleted ${name}`)
  router.replace('/')
}

const audit = useAsync(
  async () => (tab.value === 'audit' ? api.get<{ id: number; action: string; detail: string; ip: string; createdAt: number; userName: string }[]>(`/projects/${pid.value}/audit`) : []),
  [pid, tab],
)

const baseUrl = computed(() => (auth.publicUrl || window.location.origin).replace(/\/+$/, ''))
const isLocal = computed(() => /localhost|127\.0\.0\.1|0\.0\.0\.0/.test(baseUrl.value))
const initSnippet = computed(
  () => `local Workshell = require(game.ReplicatedStorage.Workshell)

Workshell.init({
	url = "${baseUrl.value}",
	key = "${project.value?.ingestKey ?? ''}",${project.value?.signingSecret ? `\n\tsigningSecret = "${project.value.signingSecret}",` : ''}
})`,
)
const secretSnippet = computed(
  () => `-- Keep the key out of your scripts with Roblox's Secrets store
-- (Game Settings → Security → Secrets, name "workshell_key"):
key = game:GetService("HttpService"):GetSecret("workshell_key"),`,
)
const clientSnippet = `-- Enables client errors, device info and client FPS/ping/memory.
require(game.ReplicatedStorage.Workshell)`
const releaseSnippet = computed(
  () => `# In CI, before \`rojo build\`: writes the release module and uploads
# the git commit + Rojo sourcemap so stack traces link to your source.
WORKSHELL_URL=${baseUrl.value} WORKSHELL_KEY=${project.value?.ingestKey ?? 'wsk_…'} \\
  workshell release -project default.project.json -out src/shared/WorkshellRelease.json`,
)
const apiSnippet = `Workshell.track("level_up", { level = 5 }, player)
Workshell.trackPurchase(player, receipt.CurrencySpent, { productId = receipt.ProductId })
Workshell.setUser(player, { level = 12, guild = "Red" })    -- session metadata
Workshell.setContext("round", { map = "Desert", mode = "TDM" }) -- on every error

local data = Workshell.trace("datastore.load", function()
	return store:GetAsync(key)
end, player)                                                  -- performance span

Workshell.flag(player, "speed", { speed = 90 }, { severity = "high", action = "kicked" })
Workshell.protect(remotes.BuyItem, onBuy, { rate = 5, validate = function(player, id)
	return typeof(id) == "string"
end })                                                        -- rate limit + validate`
</script>

<template>
  <PageHeader title="Settings" :subtitle="project?.name" />

  <div class="grid gap-8 md:grid-cols-[11rem_1fr]">
    <nav class="flex gap-1 overflow-x-auto md:flex-col">
      <button
        v-for="t in tabs"
        :key="t.value"
        class="h-8 shrink-0 cursor-pointer rounded-md px-2.5 text-left text-[13.5px] transition-colors"
        :class="tab === t.value ? 'bg-surface-3 font-medium text-fg' : 'text-muted hover:bg-surface-2 hover:text-fg'"
        @click="setTab(t.value)"
      >
        {{ t.label }}
      </button>
    </nav>

    <div class="min-w-0 space-y-6">
      <!-- General -->
      <template v-if="tab === 'general'">
        <form class="card" @submit.prevent="save({ name: form.name, logRetentionDays: form.logRetentionDays, dataRetentionDays: form.dataRetentionDays })">
          <div class="space-y-5 p-5">
            <div>
              <h2 class="text-base font-semibold">General</h2>
              <p class="mt-1 text-[13px] text-muted">Name and how long Workshell keeps data.</p>
            </div>
            <div class="max-w-md"><label class="label" for="name">Project name</label><input id="name" v-model="form.name" class="input" required /></div>
            <div class="grid max-w-xl gap-4 sm:grid-cols-2">
              <div>
                <label class="label" for="logret">Logs, traces & health</label>
                <div class="flex items-center gap-2"><input id="logret" v-model.number="form.logRetentionDays" class="input w-24" type="number" min="1" max="3650" /><span class="text-[13px] text-muted">days</span></div>
              </div>
              <div>
                <label class="label" for="dataret">Errors, events, sessions & detections</label>
                <div class="flex items-center gap-2"><input id="dataret" v-model.number="form.dataRetentionDays" class="input w-24" type="number" min="1" max="3650" /><span class="text-[13px] text-muted">days</span></div>
              </div>
            </div>
          </div>
          <div class="flex justify-end rounded-b-xl border-t border-border bg-surface-2/50 px-5 py-3"><button class="btn btn-primary" :disabled="saving">Save</button></div>
        </form>
      </template>

      <!-- SDK -->
      <template v-else-if="tab === 'sdk'">
        <div class="card" :class="welcome ? 'ring-4 ring-accent/15' : ''">
          <div class="p-5">
            <h2 class="text-base font-semibold">{{ welcome ? 'Project created — connect your game' : 'Install the SDK' }}</h2>
            <p class="mt-1 text-[13px] text-muted">About two minutes. Errors, logs, sessions and server health start flowing immediately.</p>
            <ol class="mt-5 space-y-6 text-[13px]">
              <li class="flex gap-3">
                <span class="grid size-6 shrink-0 place-items-center rounded-full border border-border text-xs font-medium">1</span>
                <div class="min-w-0 flex-1">
                  <div class="font-medium">Add the Workshell module to ReplicatedStorage</div>
                  <p class="mt-1 text-muted">Drag the model into Studio, or sync <code class="mono">sdk/</code> from the Workshell repo with Rojo or Wally.</p>
                  <a href="/api/sdk/Workshell.rbxmx" download class="btn btn-sm mt-2"><Download class="size-3.5" /> Workshell.rbxmx</a>
                </div>
              </li>
              <li class="flex gap-3">
                <span class="grid size-6 shrink-0 place-items-center rounded-full border border-border text-xs font-medium">2</span>
                <div class="min-w-0 flex-1">
                  <div class="font-medium">Enable HTTP requests</div>
                  <p class="mt-1 text-muted">Game Settings → Security → Allow HTTP Requests.</p>
                </div>
              </li>
              <li class="flex gap-3">
                <span class="grid size-6 shrink-0 place-items-center rounded-full border border-border text-xs font-medium">3</span>
                <div class="min-w-0 flex-1">
                  <div class="font-medium">Start it from a server Script</div>
                  <CodeBlock :code="initSnippet" :secrets="[project?.ingestKey ?? '', project?.signingSecret ?? '']" title="ServerScriptService/Workshell.server.luau" class="mt-2" />
                  <p v-if="isLocal" class="mt-2 text-warning">
                    Roblox servers can't reach localhost. Expose Workshell on a public HTTPS URL and set <code class="mono">WORKSHELL_PUBLIC_URL</code>.
                  </p>
                  <details class="mt-2 text-muted">
                    <summary class="cursor-pointer select-none hover:text-fg">Keep the key in Roblox Secrets instead</summary>
                    <CodeBlock :code="secretSnippet" class="mt-2" />
                  </details>
                </div>
              </li>
              <li class="flex gap-3">
                <span class="grid size-6 shrink-0 place-items-center rounded-full border border-border text-xs font-medium text-muted">4</span>
                <div class="min-w-0 flex-1">
                  <div class="font-medium">Optional: the client side</div>
                  <CodeBlock :code="clientSnippet" title="StarterPlayerScripts/Workshell.client.luau" class="mt-2" />
                </div>
              </li>
              <li class="flex gap-3">
                <span class="grid size-6 shrink-0 place-items-center rounded-full border border-border text-xs font-medium text-muted">5</span>
                <div class="min-w-0 flex-1">
                  <div class="font-medium">Optional: releases from Rojo projects</div>
                  <p class="mt-1 text-muted">
                    Tags every error with its git commit (“issue introduced in <span class="mono">a1b2c3d</span>”) and resolves stack frames to your source files.
                  </p>
                  <CodeBlock :code="releaseSnippet" lang="sh" :secrets="[project?.ingestKey ?? '', project?.signingSecret ?? '']" title="CI" class="mt-2" />
                </div>
              </li>
            </ol>
          </div>
        </div>
        <div class="card p-5">
          <h2 class="text-base font-semibold">API at a glance</h2>
          <CodeBlock :code="apiSnippet" class="mt-3" />
        </div>
      </template>

      <!-- Security -->
      <template v-else-if="tab === 'security'">
        <div class="card p-5">
          <h2 class="text-base font-semibold">Ingest key</h2>
          <p class="mt-1 text-[13px] text-muted">Identifies this project. Keep it in a server Script or Roblox Secrets — never where clients can read it.</p>
          <div class="mt-4 flex flex-wrap items-center gap-2">
            <div class="relative min-w-60 flex-1">
              <KeyRound class="pointer-events-none absolute top-2 left-2.5 size-4 text-faint" />
              <input class="input mono pr-9 pl-8" :type="showKey ? 'text' : 'password'" :value="project?.ingestKey" readonly aria-label="Ingest key" />
              <button class="absolute top-1.5 right-2 text-faint hover:text-fg" :aria-label="showKey ? 'Hide key' : 'Show key'" @click="showKey = !showKey">
                <EyeOff v-if="showKey" class="size-4" /><Eye v-else class="size-4" />
              </button>
            </div>
            <CopyButton :text="project?.ingestKey ?? ''" />
            <button class="btn btn-sm" @click="rotateKey"><RefreshCw class="size-3.5" /> Rotate</button>
          </div>
        </div>

        <div class="card p-5">
          <div class="flex flex-wrap items-start justify-between gap-3">
            <div>
              <h2 class="text-base font-semibold">Signed requests</h2>
              <p class="mt-1 max-w-xl text-[13px] text-muted">
                The SDK signs each batch with HMAC-SHA256 and a timestamp. With signatures required, a leaked ingest key alone can't be used to send fake data, and
                captured requests can't be replayed.
              </p>
            </div>
            <Badge :tone="project?.requireSignature ? 'success' : 'neutral'" dot>{{ project?.requireSignature ? 'Required' : 'Optional' }}</Badge>
          </div>
          <div v-if="project?.signingSecret" class="mt-4 flex flex-wrap items-center gap-2">
            <div class="relative min-w-60 flex-1">
              <input class="input mono pr-9" :type="showSecret ? 'text' : 'password'" :value="project.signingSecret" readonly aria-label="Signing secret" />
              <button class="absolute top-1.5 right-2 text-faint hover:text-fg" :aria-label="showSecret ? 'Hide secret' : 'Show secret'" @click="showSecret = !showSecret">
                <EyeOff v-if="showSecret" class="size-4" /><Eye v-else class="size-4" />
              </button>
            </div>
            <CopyButton :text="project.signingSecret" />
            <button class="btn btn-sm" @click="rotateSecret"><RefreshCw class="size-3.5" /> Rotate</button>
          </div>
          <div class="mt-4 flex flex-wrap gap-2">
            <button v-if="!project?.signingSecret" class="btn" @click="rotateSecret"><KeyRound class="size-4" /> Generate signing secret</button>
            <button v-else class="btn" :class="project.requireSignature ? '' : 'btn-primary'" @click="toggleSignature">
              {{ project.requireSignature ? 'Allow unsigned requests' : 'Require signed requests' }}
            </button>
          </div>
          <p v-if="project?.signingSecret" class="hint">Add <code class="mono">signingSecret = "…"</code> to <code class="mono">Workshell.init</code> (the SDK setup snippet includes it).</p>
        </div>

        <div class="card p-5">
          <div class="flex flex-wrap items-start justify-between gap-3">
            <div>
              <h2 class="text-base font-semibold">Pseudonymous player ids</h2>
              <p class="mt-1 max-w-xl text-[13px] text-muted">
                Store an HMAC of each Roblox user id (keyed per project, encrypted at rest) instead of the id itself. Lookups by Roblox id still work.
              </p>
            </div>
            <button class="btn" @click="toggleHashing">{{ project?.hashUserIds ? 'Disable' : 'Enable' }}</button>
          </div>
        </div>

        <form class="card" @submit.prevent="save({ ingestRate: form.ingestRate, clientRate: form.clientRate })">
          <div class="space-y-4 p-5">
            <div>
              <h2 class="text-base font-semibold">Abuse limits</h2>
              <p class="mt-1 max-w-xl text-[13px] text-muted">
                Exploiters can only reach Workshell through your game's clients. Data that came from a client is capped per player; anyone exceeding it is
                dropped and flagged as <span class="mono">client_flood</span> on the Security page.
              </p>
            </div>
            <div class="grid max-w-xl gap-4 sm:grid-cols-2">
              <div>
                <label class="label" for="irate">Project ingest limit</label>
                <div class="flex items-center gap-2"><input id="irate" v-model.number="form.ingestRate" type="number" min="10" class="input w-28" /><span class="text-[13px] text-muted">items/s</span></div>
              </div>
              <div>
                <label class="label" for="crate">Client reports per player</label>
                <div class="flex items-center gap-2"><input id="crate" v-model.number="form.clientRate" type="number" min="1" class="input w-28" /><span class="text-[13px] text-muted">per minute</span></div>
              </div>
            </div>
          </div>
          <div class="flex justify-end rounded-b-xl border-t border-border bg-surface-2/50 px-5 py-3"><button class="btn btn-primary" :disabled="saving">Save</button></div>
        </form>
      </template>

      <!-- Integrations -->
      <template v-else-if="tab === 'integrations'">
        <form class="card" @submit.prevent="save({ githubRepo: form.githubRepo, ...(form.githubToken ? { githubToken: form.githubToken } : {}) }, 'GitHub settings saved')">
          <div class="space-y-4 p-5">
            <div class="flex items-start justify-between gap-3">
              <div>
                <h2 class="text-base font-semibold">GitHub</h2>
                <p class="mt-1 max-w-xl text-[13px] text-muted">
                  Open GitHub issues from Workshell issues, see their comments in the activity thread, and link stack frames and releases to commits.
                </p>
              </div>
              <Badge :tone="project?.githubRepo && project?.hasGithubToken ? 'success' : 'neutral'" dot>{{ project?.githubRepo && project?.hasGithubToken ? 'Connected' : 'Not connected' }}</Badge>
            </div>
            <div class="max-w-md"><label class="label" for="repo">Repository</label><input id="repo" v-model="form.githubRepo" class="input mono" placeholder="owner/game" /></div>
            <div class="max-w-md">
              <label class="label" for="token">Access token</label>
              <input id="token" v-model="form.githubToken" type="password" class="input mono" :placeholder="project?.hasGithubToken ? '•••••••• (saved — leave blank to keep)' : 'github_pat_…'" autocomplete="off" />
              <p class="hint">A fine-grained token with Issues read & write on this repository. Stored encrypted.</p>
            </div>
          </div>
          <div class="flex justify-between rounded-b-xl border-t border-border bg-surface-2/50 px-5 py-3">
            <button v-if="project?.hasGithubToken" type="button" class="btn" @click="save({ githubToken: '' }, 'GitHub token removed')">Remove token</button>
            <span v-else />
            <button class="btn btn-primary" :disabled="saving">Save</button>
          </div>
        </form>
      </template>

      <!-- Audit -->
      <template v-else-if="tab === 'audit'">
        <div class="card">
          <div class="card-header"><span class="card-title">Audit log</span><span class="text-xs text-muted">Key rotations, settings changes, deletions</span></div>
          <div v-if="!audit.data.value?.length" class="px-4 py-8 text-center text-[13px] text-muted">Nothing recorded yet.</div>
          <div v-for="a in audit.data.value" :key="a.id" class="flex items-center gap-3 border-b border-border px-4 py-2.5 text-[13px] last:border-0">
            <span class="mono w-48 shrink-0 truncate">{{ a.action }}</span>
            <span class="min-w-0 flex-1 truncate text-muted">{{ a.detail }}</span>
            <span class="shrink-0 text-muted">{{ a.userName || '—' }}</span>
            <span class="mono hidden shrink-0 text-faint sm:block">{{ a.ip }}</span>
            <span class="shrink-0 text-xs text-faint"><RelTime :ts="a.createdAt" /></span>
          </div>
        </div>
      </template>

      <!-- Danger -->
      <template v-else-if="tab === 'danger'">
        <div class="card border-danger/40">
          <div class="p-5">
            <h2 class="text-base font-semibold">Delete project</h2>
            <p class="mt-1 text-[13px] text-muted">Permanently deletes {{ project?.name }} and everything it has collected.</p>
          </div>
          <div class="flex justify-end rounded-b-xl border-t border-danger/30 bg-danger-soft/40 px-5 py-3">
            <button class="btn btn-danger" @click="remove"><Trash2 class="size-4" /> Delete project</button>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>
