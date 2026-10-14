<script setup lang="ts">
import { ChevronLeft, CloudOff, Presentation } from 'lucide-vue-next'
import { computed, nextTick, ref, toRef, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import Avatar from '@/components/Avatar.vue'
import EmptyState from '@/components/EmptyState.vue'
import BoardCanvas from '@/components/whiteboard/BoardCanvas.vue'
import { useBoard } from '@/lib/board'
import { toast } from '@/lib/overlay'
import { useProject } from '@/lib/project'

const props = defineProps<{ bid: number; focus?: string }>()
const { pid } = useProject()
const router = useRouter()
const store = useBoard(pid, toRef(props, 'bid'))
store.load()
store.onRemoteDelete.value = () => {
  toast.info('This board was deleted', 'Someone else deleted it.')
  router.replace({ name: 'whiteboards', params: { pid: pid.value } })
}

watch(
  () => store.board.value?.title,
  (t) => {
    if (t) document.title = `${t} · Workshell`
  },
)

// Inline rename.
const renaming = ref(false)
const draft = ref('')
const titleInput = ref<HTMLInputElement | null>(null)
async function startRename() {
  draft.value = store.board.value?.title ?? ''
  renaming.value = true
  await nextTick()
  titleInput.value?.select()
}
async function finishRename(save: boolean) {
  if (!renaming.value) return
  renaming.value = false
  if (!save || !draft.value.trim() || draft.value === store.board.value?.title) return
  try {
    await store.rename(draft.value)
  } catch (e) {
    toast.error('Could not rename board', (e as Error).message)
  }
}

const peers = computed(() => {
  // One avatar per person, even with several tabs open.
  const seen = new Map<number, { name: string; avatar: string }>()
  for (const p of store.peers.values()) seen.set(p.user.id, p.user)
  return [...seen.values()]
})
const statusText = computed(
  () => ({ loading: 'Loading…', saved: 'Saved', saving: 'Saving…', error: 'Offline · retrying', missing: '' })[store.status.value],
)
</script>

<template>
  <div class="flex h-full flex-col">
    <div class="flex h-12 shrink-0 items-center gap-2 border-b border-border bg-bg px-2 sm:px-3">
      <RouterLink :to="{ name: 'whiteboards', params: { pid } }" class="btn btn-ghost btn-icon btn-sm shrink-0 text-muted" aria-label="All whiteboards">
        <ChevronLeft class="size-4" />
      </RouterLink>
      <Presentation class="size-4 shrink-0 text-faint max-sm:hidden" />
      <input
        v-if="renaming"
        ref="titleInput"
        v-model="draft"
        class="input h-8 max-w-72 min-w-0 flex-1 font-medium"
        maxlength="120"
        aria-label="Board name"
        @keydown.enter.prevent="finishRename(true)"
        @keydown.esc.prevent="finishRename(false)"
        @blur="finishRename(true)"
      />
      <button v-else class="min-w-0 truncate rounded-md px-1.5 py-1 text-left text-[14px] font-medium hover:bg-surface-2" title="Rename" @click="startRename">
        {{ store.board.value?.title ?? ' ' }}
      </button>
      <span class="flex shrink-0 items-center gap-1 text-xs" :class="store.status.value === 'error' ? 'text-warning' : 'text-faint'" :title="store.error.value || undefined" aria-live="polite">
        <CloudOff v-if="store.status.value === 'error'" class="size-3.5" />
        {{ statusText }}
      </span>
      <div class="ml-auto flex shrink-0 items-center gap-2">
        <div v-if="peers.length" class="flex -space-x-1.5" :title="`Also here: ${peers.map((p) => p.name).join(', ')}`">
          <Avatar v-for="p in peers.slice(0, 4)" :key="p.name" :name="p.name" :src="p.avatar" :size="24" class="ring-2 ring-bg" />
          <span v-if="peers.length > 4" class="grid size-6 place-items-center rounded-full bg-surface-2 text-[10px] text-muted ring-2 ring-bg">+{{ peers.length - 4 }}</span>
        </div>
        <span class="flex items-center gap-1.5 text-xs text-faint max-sm:hidden">
          <span class="dot" :class="store.connected.value ? 'bg-success' : 'bg-faint/50'" />
          {{ store.connected.value ? 'Live' : 'Connecting' }}
        </span>
      </div>
    </div>

    <div v-if="store.status.value === 'missing'" class="grid flex-1 place-items-center">
      <EmptyState title="Board not found" :icon="Presentation">
        It may have been deleted.
        <div class="mt-4"><RouterLink :to="{ name: 'whiteboards', params: { pid } }" class="btn">All whiteboards</RouterLink></div>
      </EmptyState>
    </div>
    <div v-else class="relative min-h-0 flex-1">
      <BoardCanvas :store="store" :board-id="bid" :focus="focus" />
    </div>
  </div>
</template>
