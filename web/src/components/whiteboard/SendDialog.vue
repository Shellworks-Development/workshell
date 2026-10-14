<script setup lang="ts">
import { Check, Plus, Presentation } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import ErrorBanner from '@/components/ErrorBanner.vue'
import Modal from '@/components/Modal.vue'
import RelTime from '@/components/RelTime.vue'
import { api, type Whiteboard } from '@/lib/api'
import { toast } from '@/lib/overlay'
import { sendState } from '@/lib/whiteboard'

/*
 * The board picker behind every "Send to whiteboard" button: choose a board
 * (the last one used is preselected) or start a new one, then the snapshot is
 * placed below everything already on it.
 */
const router = useRouter()
const boards = ref<Whiteboard[] | null>(null)
const chosen = ref<number | 'new'>('new')
const newTitle = ref('')
const busy = ref(false)
const error = ref('')

const lastKey = computed(() => `workshell-last-board-${sendState.pid}`)

watch(
  () => sendState.open,
  async (open) => {
    if (!open) return
    error.value = ''
    newTitle.value = ''
    boards.value = null
    try {
      boards.value = await api.get<Whiteboard[]>(`/projects/${sendState.pid}/whiteboards`)
    } catch (e) {
      error.value = (e as Error).message
      boards.value = []
    }
    let last = NaN
    try {
      last = Number(localStorage.getItem(lastKey.value))
    } catch {
      // storage unavailable
    }
    chosen.value = boards.value.find((b) => b.id === last)?.id ?? boards.value[0]?.id ?? 'new'
  },
)

async function send() {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    let board = boards.value?.find((b) => b.id === chosen.value)
    if (chosen.value === 'new') {
      board = await api.post<Whiteboard>(`/projects/${sendState.pid}/whiteboards`, { title: newTitle.value || sendState.label || 'Untitled board' })
    }
    if (!board) return
    await api.post(`/projects/${sendState.pid}/whiteboards/${board.id}/items`, { place: true, upsert: sendState.items })
    try {
      localStorage.setItem(lastKey.value, String(board.id))
    } catch {
      // storage unavailable
    }
    const to = { name: 'whiteboard', params: { pid: sendState.pid, bid: board.id }, query: { focus: sendState.items[0]?.id } }
    sendState.open = false
    toast(`Sent to ${board.title}`, { action: { label: 'Open', run: () => router.push(to) } })
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Modal :open="sendState.open" title="Send to whiteboard" :description="sendState.label ? `“${sendState.label}” is saved as a snapshot with a link back here.` : undefined" width="26rem" @close="sendState.open = false">
    <form id="send-wb" class="space-y-3" @submit.prevent="send">
      <ErrorBanner :message="error" />
      <div v-if="boards === null" class="space-y-1.5">
        <div v-for="i in 3" :key="i" class="skeleton h-11 rounded-lg" />
      </div>
      <div v-else class="max-h-[min(18rem,45vh)] space-y-1 overflow-y-auto overscroll-contain" role="radiogroup" aria-label="Board">
        <button
          v-for="b in boards"
          :key="b.id"
          type="button"
          role="radio"
          :aria-checked="chosen === b.id"
          class="menu-item min-h-11 rounded-lg border"
          :class="chosen === b.id ? 'border-border-strong bg-surface-2' : 'border-transparent hover:bg-surface-2'"
          @click="chosen = b.id"
        >
          <Presentation class="size-4 shrink-0 text-faint" />
          <span class="min-w-0 flex-1 truncate">{{ b.title }}</span>
          <span class="shrink-0 text-xs text-faint"><RelTime :ts="b.updatedAt" /></span>
          <Check v-if="chosen === b.id" class="size-4 shrink-0 text-accent" />
        </button>
        <button
          type="button"
          role="radio"
          :aria-checked="chosen === 'new'"
          class="menu-item min-h-11 rounded-lg border"
          :class="chosen === 'new' ? 'border-border-strong bg-surface-2' : 'border-transparent hover:bg-surface-2'"
          @click="chosen = 'new'"
        >
          <Plus class="size-4 shrink-0 text-faint" />
          <span class="flex-1">New board</span>
          <Check v-if="chosen === 'new'" class="size-4 shrink-0 text-accent" />
        </button>
      </div>
      <div v-if="chosen === 'new'">
        <label class="label" for="wb-title">Board name</label>
        <input id="wb-title" v-model="newTitle" class="input" :placeholder="sendState.label || 'Untitled board'" maxlength="120" data-autofocus />
      </div>
    </form>
    <template #footer>
      <button class="btn" @click="sendState.open = false">Cancel</button>
      <button class="btn btn-primary" type="submit" form="send-wb" :disabled="busy || boards === null">{{ busy ? 'Sending…' : 'Send' }}</button>
    </template>
  </Modal>
</template>
