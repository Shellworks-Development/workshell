<script setup lang="ts">
import { MoreHorizontal, Pencil, Plus, Presentation, Trash2 } from 'lucide-vue-next'
import { ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import Avatar from '@/components/Avatar.vue'
import EmptyState from '@/components/EmptyState.vue'
import ErrorBanner from '@/components/ErrorBanner.vue'
import Modal from '@/components/Modal.vue'
import PageHeader from '@/components/PageHeader.vue'
import Popover from '@/components/Popover.vue'
import RelTime from '@/components/RelTime.vue'
import { api, type Whiteboard } from '@/lib/api'
import { num } from '@/lib/format'
import { confirmDialog, toast } from '@/lib/overlay'
import { useProject } from '@/lib/project'
import { dark } from '@/lib/state'
import { useAsync } from '@/lib/useAsync'
import { swatchOf, swatchVar, tint } from '@/lib/whiteboard'

const { pid } = useProject()
const router = useRouter()
const { data, error, reload } = useAsync(() => api.get<Whiteboard[]>(`/projects/${pid.value}/whiteboards`), [pid], { refreshMs: 30_000 })

// ---- Thumbnails: each board's items drawn as outlines ---------------------------
function viewBox(b: Whiteboard) {
  const p = b.preview ?? []
  if (!p.length) return '0 0 160 90'
  const x1 = Math.min(...p.map((i) => i.x))
  const y1 = Math.min(...p.map((i) => i.y))
  const x2 = Math.max(...p.map((i) => i.x + i.w))
  const y2 = Math.max(...p.map((i) => i.y + i.h))
  // Keep a 16:9 frame around the content, padded.
  let w = x2 - x1
  let h = y2 - y1
  const pad = Math.max(w, h) * 0.08 + 20
  w += pad * 2
  h += pad * 2
  const cx = (x1 + x2) / 2
  const cy = (y1 + y2) / 2
  if (w / h > 16 / 9) h = (w * 9) / 16
  else w = (h * 16) / 9
  return `${cx - w / 2} ${cy - h / 2} ${w} ${h}`
}
const fill = (i: { kind: string; color?: string }) =>
  i.kind === 'sticky' ? `color-mix(in oklch, ${swatchVar[swatchOf(i.color)]} ${dark.value ? 78 : 35}%, white)` : i.kind === 'shape' ? tint(i.color, 22) : i.kind === 'frame' ? tint(i.color ?? 'gray', 6) : i.kind === 'text' ? 'var(--border-strong)' : 'var(--surface)'
const stroke = (i: { kind: string; color?: string }) =>
  i.kind === 'shape' ? swatchVar[swatchOf(i.color)] : i.kind === 'embed' || i.kind === 'frame' ? 'var(--border-strong)' : 'none'

// ---- Create / rename / delete ----------------------------------------------------
const creating = ref(false)
const title = ref('')
const busy = ref(false)
const formError = ref('')
const renaming = ref<Whiteboard | null>(null)

async function create() {
  busy.value = true
  formError.value = ''
  try {
    const b = await api.post<Whiteboard>(`/projects/${pid.value}/whiteboards`, { title: title.value })
    creating.value = false
    router.push({ name: 'whiteboard', params: { pid: pid.value, bid: b.id } })
  } catch (e) {
    formError.value = (e as Error).message
  } finally {
    busy.value = false
  }
}
function openCreate() {
  title.value = ''
  formError.value = ''
  creating.value = true
}
function openRename(b: Whiteboard) {
  menuFor.value = null
  title.value = b.title
  formError.value = ''
  renaming.value = b
}
async function rename() {
  if (!renaming.value) return
  busy.value = true
  try {
    await api.patch(`/projects/${pid.value}/whiteboards/${renaming.value.id}`, { title: title.value })
    renaming.value = null
    reload(true)
  } catch (e) {
    formError.value = (e as Error).message
  } finally {
    busy.value = false
  }
}
async function remove(b: Whiteboard) {
  menuFor.value = null
  const ok = await confirmDialog({
    title: `Delete “${b.title}”?`,
    message: `Its ${num(b.itemCount)} items are deleted for everyone. This can't be undone.`,
    confirmLabel: 'Delete board',
    danger: true,
  })
  if (!ok) return
  try {
    await api.del(`/projects/${pid.value}/whiteboards/${b.id}`)
    toast('Board deleted')
    reload(true)
  } catch (e) {
    toast.error('Could not delete board', (e as Error).message)
  }
}
const menuFor = ref<number | null>(null)
</script>

<template>
  <PageHeader title="Whiteboards" subtitle="Lay out the evidence: charts, issues, sessions and traces on one canvas, with your notes.">
    <button class="btn btn-primary" @click="openCreate"><Plus class="size-4" /> New board</button>
  </PageHeader>
  <ErrorBanner :message="error" />

  <div v-if="!data" class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
    <div v-for="i in 3" :key="i" class="card overflow-hidden">
      <div class="skeleton aspect-video rounded-none" />
      <div class="space-y-2 p-4"><div class="skeleton h-4 w-2/3" /><div class="skeleton h-3 w-1/3" /></div>
    </div>
  </div>
  <div v-else-if="data.length === 0" class="card">
    <EmptyState title="No whiteboards yet" :icon="Presentation">
      Start a board for an investigation, or use <span class="font-medium text-fg">Send to whiteboard</span> on any chart, issue, session or trace.
      <div class="mt-4"><button class="btn btn-primary" @click="openCreate"><Plus class="size-4" /> New board</button></div>
    </EmptyState>
  </div>
  <div v-else class="stagger grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
    <div v-for="b in data" :key="b.id" class="card group relative overflow-hidden transition-[border-color,box-shadow] duration-150 hover:border-border-strong">
      <RouterLink :to="{ name: 'whiteboard', params: { pid, bid: b.id } }" class="block" :aria-label="`Open ${b.title}`">
        <div class="aspect-video border-b border-border bg-bg" aria-hidden="true">
          <svg v-if="b.preview?.length" class="size-full" :viewBox="viewBox(b)" preserveAspectRatio="xMidYMid meet">
            <rect
              v-for="(i, n) in b.preview"
              :key="n"
              :x="i.x"
              :y="i.y"
              :width="i.w"
              :height="i.kind === 'text' ? Math.min(i.h, 18) : i.h"
              :rx="i.kind === 'shape' || i.kind === 'embed' || i.kind === 'frame' ? 12 : 4"
              :fill="fill(i)"
              :stroke="stroke(i)"
              stroke-width="3"
              vector-effect="non-scaling-stroke"
            />
          </svg>
          <div v-else class="grid size-full place-items-center text-xs text-faint">Empty board</div>
        </div>
        <div class="p-4 pr-12">
          <div class="truncate text-sm font-medium">{{ b.title }}</div>
          <div class="mt-1 flex items-center gap-1.5 text-xs text-muted">
            <Avatar v-if="b.updatedBy" :name="b.updatedBy.name" :src="b.updatedBy.avatar" :size="16" />
            <span class="truncate">{{ b.updatedBy?.name ?? 'Someone' }} · <RelTime :ts="b.updatedAt" /> · {{ num(b.itemCount) }} items</span>
          </div>
        </div>
      </RouterLink>
      <div class="absolute right-2 bottom-3">
        <Popover :open="menuFor === b.id" placement="bottom-end" width="12rem" @update:open="(o: boolean) => (menuFor = o ? b.id : null)">
          <template #trigger="{ toggle }">
            <button class="btn btn-ghost btn-icon btn-sm text-muted" :aria-label="`Actions for ${b.title}`" @click="toggle"><MoreHorizontal class="size-4" /></button>
          </template>
          <div class="p-1">
            <button class="menu-item" @click="openRename(b)"><Pencil class="size-4 text-muted" /> Rename</button>
            <button class="menu-item text-danger" @click="remove(b)"><Trash2 class="size-4" /> Delete</button>
          </div>
        </Popover>
      </div>
    </div>
  </div>

  <Modal :open="creating || !!renaming" :title="renaming ? 'Rename board' : 'New whiteboard'" width="26rem" @close="(creating = false), (renaming = null)">
    <form id="board-form" @submit.prevent="renaming ? rename() : create()">
      <ErrorBanner :message="formError" />
      <label class="label" for="board-title">Name</label>
      <input id="board-title" v-model="title" class="input" maxlength="120" placeholder="e.g. Combat lag spike, Oct 14" data-autofocus />
    </form>
    <template #footer>
      <button class="btn" @click="(creating = false), (renaming = null)">Cancel</button>
      <button class="btn btn-primary" type="submit" form="board-form" :disabled="busy">{{ renaming ? 'Save' : 'Create board' }}</button>
    </template>
  </Modal>
</template>
