<script setup lang="ts">
import { BellOff, Hash, Plus, Send, Trash2, Webhook } from 'lucide-vue-next'
import { computed, reactive, ref } from 'vue'
import Badge from '@/components/Badge.vue'
import EmptyState from '@/components/EmptyState.vue'
import ErrorBanner from '@/components/ErrorBanner.vue'
import Modal from '@/components/Modal.vue'
import PageHeader from '@/components/PageHeader.vue'
import RelTime from '@/components/RelTime.vue'
import Select from '@/components/Select.vue'
import { api, type AlertChannel, type AlertRule } from '@/lib/api'
import { confirmDialog, toast } from '@/lib/overlay'
import { useProject } from '@/lib/project'
import { useAsync } from '@/lib/useAsync'

const { pid } = useProject()
interface Alerts {
  channels: AlertChannel[]
  rules: AlertRule[]
  history: { id: number; kind: string; title: string; detail: string; link: string; delivered: boolean; error: string; createdAt: number; ruleName: string }[]
  kinds: { kind: string; label: string; threshold: boolean }[]
}
const { data, error, reload } = useAsync(() => api.get<Alerts>(`/projects/${pid.value}/alerts`), [pid])

const channelName = (id: number | null) => data.value?.channels.find((c) => c.id === id)?.name
const kindLabel = (k: string) => data.value?.kinds.find((x) => x.kind === k)?.label ?? k
const units: Record<string, string> = { error_spike: 'errors', detection: 'detections', ccu_below: 'players', fps_below: 'FPS' }

function describe(r: AlertRule) {
  switch (r.kind) {
    case 'error_spike':
      return `≥ ${r.threshold} errors in ${r.windowMinutes} min`
    case 'detection':
      return r.threshold <= 1 ? 'Any high-severity detection' : `≥ ${r.threshold} detections in ${r.windowMinutes} min`
    case 'ccu_below':
      return `Fewer than ${r.threshold} players`
    case 'fps_below':
      return `Server FPS below ${r.threshold}`
    default:
      return kindLabel(r.kind)
  }
}

async function toggle(r: AlertRule) {
  await api.patch(`/projects/${pid.value}/alerts/rules/${r.id}`, { enabled: !r.enabled })
  await reload()
}

// ---- Rule editor ------------------------------------------------------------------
const editing = ref(false)
const ruleError = ref<string | null>(null)
const rule = reactive({ id: 0, name: '', kind: 'error_spike', threshold: 100, windowMinutes: 5, cooldownMinutes: 30, channelId: 0 })
const isThreshold = computed(() => data.value?.kinds.find((k) => k.kind === rule.kind)?.threshold ?? false)
function edit(r?: AlertRule) {
  ruleError.value = null
  Object.assign(rule, r ? { ...r, channelId: r.channelId ?? 0 } : { id: 0, name: '', kind: 'error_spike', threshold: 100, windowMinutes: 5, cooldownMinutes: 30, channelId: data.value?.channels[0]?.id ?? 0 })
  editing.value = true
}
async function saveRule() {
  ruleError.value = null
  try {
    const body = { name: rule.name, kind: rule.kind, threshold: Number(rule.threshold), windowMinutes: Number(rule.windowMinutes), cooldownMinutes: Number(rule.cooldownMinutes), channelId: rule.channelId }
    if (rule.id) await api.patch(`/projects/${pid.value}/alerts/rules/${rule.id}`, body)
    else await api.post(`/projects/${pid.value}/alerts/rules`, body)
    editing.value = false
    toast('Alert rule saved')
    await reload()
  } catch (e) {
    ruleError.value = (e as Error).message
  }
}
async function deleteRule(r: AlertRule) {
  if (!(await confirmDialog({ title: `Delete “${r.name}”?`, confirmLabel: 'Delete', danger: true }))) return
  await api.del(`/projects/${pid.value}/alerts/rules/${r.id}`)
  editing.value = false
  await reload()
}
const kindOptions = computed(() => (data.value?.kinds ?? []).map((k) => ({ value: k.kind, label: k.label })))
const channelOptions = computed(() => [{ value: 0, label: 'No channel (history only)' }, ...(data.value?.channels ?? []).map((c) => ({ value: c.id, label: c.name }))])

// ---- Channels -----------------------------------------------------------------------
const addingChannel = ref(false)
const channel = reactive({ name: '', url: '' })
const channelError = ref<string | null>(null)
async function addChannel() {
  channelError.value = null
  try {
    await api.post(`/projects/${pid.value}/alerts/channels`, channel)
    Object.assign(channel, { name: '', url: '' })
    addingChannel.value = false
    toast('Channel added')
    await reload()
  } catch (e) {
    channelError.value = (e as Error).message
  }
}
async function testChannel(c: AlertChannel) {
  try {
    await api.post(`/projects/${pid.value}/alerts/channels/${c.id}/test`)
    toast('Test alert sent', `Check ${c.name}.`)
  } catch (e) {
    toast.error('Webhook failed', (e as Error).message)
  }
}
async function deleteChannel(c: AlertChannel) {
  if (!(await confirmDialog({ title: `Delete ${c.name}?`, message: 'Rules using it will only record alerts in history.', confirmLabel: 'Delete', danger: true }))) return
  await api.del(`/projects/${pid.value}/alerts/channels/${c.id}`)
  await reload()
}
</script>

<template>
  <PageHeader title="Alerts" subtitle="Rules decide when to alert; channels decide where alerts go.">
    <button class="btn btn-primary" @click="edit()"><Plus class="size-4" /> New rule</button>
  </PageHeader>
  <ErrorBanner :message="error" />

  <template v-if="data">
    <section class="mb-8">
      <h2 class="section-title">Rules</h2>
      <div class="card">
        <EmptyState v-if="data.rules.length === 0" title="No alert rules" :icon="BellOff" />
        <div v-for="r in data.rules" :key="r.id" class="flex items-center gap-3 border-b border-border px-4 py-3 last:border-0">
          <button
            role="switch"
            :aria-checked="r.enabled"
            :aria-label="`${r.enabled ? 'Disable' : 'Enable'} ${r.name}`"
            class="relative h-5 w-9 shrink-0 cursor-pointer rounded-full transition-colors duration-200"
            :class="r.enabled ? 'bg-fg' : 'bg-surface-3'"
            @click="toggle(r)"
          >
            <span class="absolute top-0.5 left-0.5 size-4 rounded-full bg-bg shadow transition-transform duration-200 ease-(--ease-out)" :class="r.enabled ? 'translate-x-4' : ''" />
          </button>
          <button class="min-w-0 flex-1 cursor-pointer text-left" @click="edit(r)">
            <div class="truncate text-[13.5px] font-medium" :class="r.enabled ? '' : 'text-muted'">{{ r.name }}</div>
            <div class="truncate text-xs text-muted">
              {{ describe(r) }} → {{ channelName(r.channelId) ?? 'history only' }}
              <template v-if="r.lastFiredAt"> · last fired <RelTime :ts="r.lastFiredAt" /></template>
            </div>
          </button>
          <button class="btn btn-sm" @click="edit(r)">Edit</button>
        </div>
      </div>
    </section>

    <section class="mb-8">
      <div class="mb-2.5 flex items-center justify-between">
        <h2 class="text-sm font-medium">Channels</h2>
        <button class="btn btn-sm" @click="addingChannel = true"><Plus class="size-3.5" /> Add channel</button>
      </div>
      <div class="card">
        <div v-if="data.channels.length === 0" class="px-4 py-8 text-center text-[13px] text-muted">
          Add a Discord webhook (or any URL that accepts JSON) to get alerts delivered.
        </div>
        <div v-for="c in data.channels" :key="c.id" class="flex items-center gap-3 border-b border-border px-4 py-3 last:border-0">
          <span class="grid size-8 place-items-center rounded-md bg-surface-2 text-muted">
            <Hash v-if="c.kind === 'discord'" class="size-4" />
            <Webhook v-else class="size-4" />
          </span>
          <div class="min-w-0 flex-1">
            <div class="text-[13.5px] font-medium">{{ c.name }} <Badge>{{ c.kind }}</Badge></div>
            <div class="mono truncate text-muted">{{ c.url }}</div>
          </div>
          <button class="btn btn-sm" @click="testChannel(c)"><Send class="size-3.5" /> Test</button>
          <button class="btn btn-ghost btn-icon text-danger" aria-label="Delete channel" @click="deleteChannel(c)"><Trash2 class="size-4" /></button>
        </div>
      </div>
      <p class="hint">Webhook URLs are encrypted at rest. Alert text is sanitized so player-controlled error messages can't ping @everyone.</p>
    </section>

    <section>
      <h2 class="section-title">History</h2>
      <div class="card">
        <div v-if="data.history.length === 0" class="px-4 py-8 text-center text-[13px] text-muted">No alerts have fired yet.</div>
        <component
          :is="h.link ? 'a' : 'div'"
          v-for="h in data.history"
          :key="h.id"
          :href="h.link || undefined"
          class="flex items-start gap-3 border-b border-border px-4 py-2.5 text-[13px] last:border-0"
          :class="h.link ? 'hover:bg-surface-2' : ''"
        >
          <span class="dot mt-1.5" :class="h.error ? 'bg-danger' : h.delivered ? 'bg-success' : 'bg-faint/50'" />
          <div class="min-w-0 flex-1">
            <div class="truncate font-medium">{{ h.title }}</div>
            <div class="truncate text-xs text-muted">{{ h.ruleName || h.kind }}<template v-if="h.error"> · <span class="text-danger">{{ h.error }}</span></template></div>
          </div>
          <span class="shrink-0 text-xs text-faint"><RelTime :ts="h.createdAt" /></span>
        </component>
      </div>
    </section>
  </template>

  <Modal :open="editing" :title="rule.id ? 'Edit rule' : 'New alert rule'" width="30rem" @close="editing = false">
    <form id="rule" class="space-y-4" @submit.prevent="saveRule">
      <ErrorBanner :message="ruleError" />
      <div>
        <label class="label">When</label>
        <Select v-model="rule.kind" :options="kindOptions" label="Rule type" class="w-full" />
      </div>
      <div v-if="isThreshold" class="grid grid-cols-2 gap-3">
        <div>
          <label class="label" for="threshold">Threshold ({{ units[rule.kind] }})</label>
          <input id="threshold" v-model.number="rule.threshold" type="number" min="0" step="any" class="input" />
        </div>
        <div v-if="rule.kind === 'error_spike' || rule.kind === 'detection'">
          <label class="label" for="window">Window (minutes)</label>
          <input id="window" v-model.number="rule.windowMinutes" type="number" min="1" max="1440" class="input" />
        </div>
      </div>
      <div>
        <label class="label">Send to</label>
        <Select v-model="rule.channelId" :options="channelOptions" label="Channel" class="w-full" />
      </div>
      <div class="grid grid-cols-2 gap-3">
        <div><label class="label" for="rname">Name</label><input id="rname" v-model="rule.name" class="input" :placeholder="kindLabel(rule.kind)" /></div>
        <div><label class="label" for="cooldown">Cooldown (minutes)</label><input id="cooldown" v-model.number="rule.cooldownMinutes" type="number" min="0" class="input" /></div>
      </div>
    </form>
    <template #footer>
      <button v-if="rule.id" class="btn mr-auto text-danger" @click="deleteRule(rule as unknown as AlertRule)">Delete</button>
      <button class="btn" @click="editing = false">Cancel</button>
      <button class="btn btn-primary" type="submit" form="rule">Save rule</button>
    </template>
  </Modal>

  <Modal :open="addingChannel" title="Add channel" description="Discord webhooks get rich embeds. Any other URL receives a JSON POST with a Slack-compatible text field." @close="addingChannel = false">
    <form id="channel" class="space-y-3" @submit.prevent="addChannel">
      <ErrorBanner :message="channelError" />
      <div><label class="label" for="cname">Name</label><input id="cname" v-model="channel.name" class="input" placeholder="#game-alerts" required data-autofocus /></div>
      <div><label class="label" for="curl">Webhook URL</label><input id="curl" v-model="channel.url" class="input mono" placeholder="https://discord.com/api/webhooks/…" required /></div>
    </form>
    <template #footer>
      <button class="btn" @click="addingChannel = false">Cancel</button>
      <button class="btn btn-primary" type="submit" form="channel">Add channel</button>
    </template>
  </Modal>
</template>
