<script setup lang="ts">
import { X } from 'lucide-vue-next'
import { nextTick, onBeforeUnmount, ref, useId, watch } from 'vue'

const props = withDefaults(defineProps<{ open: boolean; title: string; description?: string; width?: string }>(), {
  width: '28rem',
})
const emit = defineEmits<{ close: [] }>()

const panel = ref<HTMLElement | null>(null)
const titleId = useId()
let returnFocus: HTMLElement | null = null

const focusable = () =>
  [...(panel.value?.querySelectorAll<HTMLElement>('button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])') ?? [])].filter(
    (el) => !el.hasAttribute('disabled'),
  )

watch(
  () => props.open,
  async (open) => {
    if (open) {
      returnFocus = document.activeElement as HTMLElement | null
      document.documentElement.style.overflow = 'hidden'
      await nextTick()
      const target = panel.value?.querySelector<HTMLElement>('[data-autofocus]') ?? focusable()[0]
      target?.focus()
    } else {
      document.documentElement.style.overflow = ''
      returnFocus?.focus?.()
    }
  },
  { immediate: true },
)
onBeforeUnmount(() => (document.documentElement.style.overflow = ''))

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    e.stopPropagation()
    emit('close')
  } else if (e.key === 'Tab') {
    // Keep focus inside the dialog.
    const els = focusable()
    if (!els.length) return
    const first = els[0]!
    const last = els[els.length - 1]!
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault()
      last.focus()
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault()
      first.focus()
    }
  }
}
</script>

<template>
  <Teleport to="body">
    <Transition name="fade">
      <div v-if="open" class="fixed inset-0 z-[90] bg-(--overlay) backdrop-blur-[2px]" aria-hidden="true" @click="emit('close')" />
    </Transition>
    <Transition name="modal">
      <div v-if="open" class="pointer-events-none fixed inset-0 z-[91] grid place-items-center p-4 pb-[calc(1rem+env(safe-area-inset-bottom))]" @keydown="onKeydown">
        <div
          ref="panel"
          role="dialog"
          aria-modal="true"
          :aria-labelledby="titleId"
          class="pointer-events-auto w-full rounded-2xl border border-border bg-surface shadow-(--shadow-pop)"
          :style="{ maxWidth: width }"
        >
          <div class="flex items-start gap-3 px-5 pt-5">
            <slot name="icon" />
            <div class="min-w-0 flex-1">
              <h2 :id="titleId" class="text-[15px] font-semibold tracking-tight">{{ title }}</h2>
              <p v-if="description" class="mt-1 text-[13px] leading-relaxed text-muted">{{ description }}</p>
            </div>
            <button class="btn btn-ghost -mt-1 -mr-2 size-8 shrink-0 px-0 text-muted" aria-label="Close" @click="emit('close')">
              <X class="size-4" />
            </button>
          </div>
          <div class="px-5 pt-4 pb-5"><slot /></div>
          <div v-if="$slots.footer" class="flex justify-end gap-2 rounded-b-2xl border-t border-border bg-surface-2/50 px-5 py-3">
            <slot name="footer" />
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>
