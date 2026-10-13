<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'

/*
 * An anchored popover (menus, inbox, pickers). Scales out of its trigger,
 * closes on outside click / Escape, and follows the trigger on scroll.
 */
const props = withDefaults(defineProps<{ placement?: 'bottom-start' | 'bottom-end' | 'top-start' | 'top-end'; width?: string }>(), {
  placement: 'bottom-start',
})
const open = defineModel<boolean>('open', { default: false })
const trigger = ref<HTMLElement | null>(null)
const panel = ref<HTMLElement | null>(null)
const pos = ref<Record<string, string>>({})

function place() {
  // The wrapper is display:contents (no box), so measure the trigger itself.
  const el = (trigger.value?.firstElementChild as HTMLElement | null) ?? trigger.value
  const r = el?.getBoundingClientRect()
  if (!r) return
  const [v, h] = props.placement.split('-') as ['bottom' | 'top', 'start' | 'end']
  const style: Record<string, string> = {}
  if (v === 'bottom') style.top = `${r.bottom + 6}px`
  else style.bottom = `${window.innerHeight - r.top + 6}px`
  if (h === 'start') style.left = `${Math.max(8, r.left)}px`
  else style.right = `${Math.max(8, window.innerWidth - r.right)}px`
  style.transformOrigin = `${v === 'bottom' ? 'top' : 'bottom'} ${h === 'start' ? 'left' : 'right'}`
  if (props.width) style.width = props.width
  pos.value = style
}

function onOutside(e: Event) {
  const t = e.target as Node
  if (!panel.value?.contains(t) && !trigger.value?.contains(t)) open.value = false
}
function onKey(e: Event) {
  if ((e as KeyboardEvent).key === 'Escape') {
    open.value = false
    trigger.value?.querySelector<HTMLElement>('button, a')?.focus()
  }
}
watch(open, async (o) => {
  const m = o ? 'addEventListener' : 'removeEventListener'
  document[m]('pointerdown', onOutside, true)
  document[m]('keydown', onKey)
  window[m]('scroll', place, true)
  window[m]('resize', place)
  if (o) {
    place()
    await nextTick()
    panel.value?.querySelector<HTMLElement>('[data-autofocus]')?.focus()
  }
})
onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', onOutside, true)
  document.removeEventListener('keydown', onKey)
  window.removeEventListener('scroll', place, true)
  window.removeEventListener('resize', place)
})
</script>

<template>
  <div ref="trigger" class="contents">
    <slot name="trigger" :open="open" :toggle="() => (open = !open)" />
  </div>
  <Teleport to="body">
    <Transition name="pop">
      <div
        v-if="open"
        ref="panel"
        class="fixed z-[85] max-h-[min(32rem,calc(100dvh-4rem))] overflow-auto overscroll-contain rounded-xl border border-border bg-surface shadow-(--shadow-pop)"
        :style="pos"
      >
        <slot :close="() => (open = false)" />
      </div>
    </Transition>
  </Teleport>
</template>
