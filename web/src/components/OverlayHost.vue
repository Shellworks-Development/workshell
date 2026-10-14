<script setup lang="ts">
import { CircleAlert, CircleCheck, Info, TriangleAlert, X } from 'lucide-vue-next'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { closeDialog, dismissToast, overlay, type Toast } from '@/lib/overlay'
import Modal from './Modal.vue'

// ---- Confirm dialog ---------------------------------------------------------
const typed = ref('')
const dialog = ref(overlay.dialog)
watch(
  () => overlay.dialog,
  (d) => {
    // Keep the last dialog's content while it animates out.
    if (d) {
      dialog.value = d
      typed.value = ''
    }
  },
)
const canConfirm = computed(() => !dialog.value?.typeToConfirm || typed.value === dialog.value.typeToConfirm)
function confirm() {
  if (canConfirm.value) closeDialog(true)
}

// ---- Toasts -------------------------------------------------------------------
// Timers pause while hovered or while the tab is hidden, so nothing vanishes unread.
const hovered = ref(false)
const remaining = new Map<number, number>()
let last = performance.now()
const tick = setInterval(() => {
  const now = performance.now()
  const dt = now - last
  last = now
  if (hovered.value || document.visibilityState !== 'visible') return
  for (const t of [...overlay.toasts]) {
    const left = (remaining.get(t.id) ?? t.duration) - dt
    if (left <= 0) {
      remaining.delete(t.id)
      dismissToast(t.id)
    } else remaining.set(t.id, left)
  }
}, 100)
onBeforeUnmount(() => clearInterval(tick))

const icons = { success: CircleCheck, error: CircleAlert, info: Info }
const iconClass = (t: Toast) => ({ success: 'text-success', error: 'text-danger', info: 'text-info' })[t.tone]
</script>

<template>
  <Modal
    :open="!!overlay.dialog"
    :title="dialog?.title ?? ''"
    :description="dialog?.message"
    width="26rem"
    @close="closeDialog(false)"
  >
    <template #icon>
      <div
        class="grid size-9 shrink-0 place-items-center rounded-full"
        :class="dialog?.danger ? 'bg-danger-soft text-danger' : 'bg-accent-soft text-accent'"
      >
        <TriangleAlert v-if="dialog?.danger" class="size-4.5" />
        <Info v-else class="size-4.5" />
      </div>
    </template>
    <form v-if="dialog?.typeToConfirm" @submit.prevent="confirm">
      <label class="label" for="type-to-confirm">
        Type <span class="mono rounded bg-surface-2 px-1 py-0.5 text-fg">{{ dialog.typeToConfirm }}</span> to confirm
      </label>
      <input id="type-to-confirm" v-model="typed" class="input" autocomplete="off" spellcheck="false" data-autofocus />
    </form>
    <template #footer>
      <button class="btn" @click="closeDialog(false)">{{ dialog?.cancelLabel ?? 'Cancel' }}</button>
      <button
        class="btn"
        :class="dialog?.danger ? 'btn-danger' : 'btn-primary'"
        :disabled="!canConfirm"
        :data-autofocus="dialog?.typeToConfirm ? undefined : ''"
        @click="confirm"
      >
        {{ dialog?.confirmLabel ?? 'Confirm' }}
      </button>
    </template>
  </Modal>

  <Teleport to="body">
    <div
      class="pointer-events-none fixed right-4 bottom-[calc(1rem+env(safe-area-inset-bottom))] z-[100] w-[min(22rem,calc(100vw-2rem))] max-lg:bottom-[calc(4.75rem+env(safe-area-inset-bottom))]"
      aria-live="polite"
      @mouseenter="hovered = true"
      @mouseleave="hovered = false"
    >
      <TransitionGroup name="toast" tag="div" class="relative flex flex-col gap-2">
        <div
          v-for="t in overlay.toasts"
          :key="t.id"
          class="pointer-events-auto flex items-start gap-2.5 rounded-xl border border-border bg-surface px-3.5 py-3 shadow-(--shadow-pop)"
          :role="t.tone === 'error' ? 'alert' : 'status'"
        >
          <component :is="icons[t.tone]" class="mt-px size-4 shrink-0" :class="iconClass(t)" />
          <div class="min-w-0 flex-1">
            <div class="text-[13px] font-medium">{{ t.message }}</div>
            <div v-if="t.description" class="mt-0.5 text-xs text-muted">{{ t.description }}</div>
          </div>
          <button
            v-if="t.action"
            class="btn btn-sm -my-1 shrink-0"
            @click="
              () => {
                t.action!.run()
                dismissToast(t.id)
              }
            "
          >
            {{ t.action.label }}
          </button>
          <button class="-mt-0.5 -mr-1 rounded-md p-0.5 text-faint hover:text-fg" aria-label="Dismiss" @click="dismissToast(t.id)">
            <X class="size-3.5" />
          </button>
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>
