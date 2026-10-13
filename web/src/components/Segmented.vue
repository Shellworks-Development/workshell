<script setup lang="ts" generic="T extends string">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'

/*
 * Segmented control. The active styling lives on a duplicate row that is
 * clipped to the active segment; moving the clip animates text color and
 * background together, in sync (clip-path tab technique).
 */
const props = defineProps<{ options: { value: T; label: string; count?: string }[]; size?: 'sm' | 'md' }>()
const model = defineModel<T>({ required: true })

const root = ref<HTMLElement | null>(null)
const clip = ref('inset(0 100% 0 0 round 6px)')
const ready = ref(false)

function measure() {
  const el = root.value
  const btn = el?.querySelector<HTMLElement>(`[data-value="${CSS.escape(String(model.value))}"]`)
  if (!el || !btn) return
  const left = btn.offsetLeft
  const right = el.clientWidth - (btn.offsetLeft + btn.offsetWidth)
  clip.value = `inset(2px ${right}px 2px ${left}px round 6px)`
}

let ro: ResizeObserver | undefined
onMounted(() => {
  measure()
  // Enable the transition after the first position so it doesn't slide in on load.
  requestAnimationFrame(() => (ready.value = true))
  ro = new ResizeObserver(measure)
  if (root.value) ro.observe(root.value)
})
onBeforeUnmount(() => ro?.disconnect())
watch([model, () => props.options], () => nextTick(measure))
</script>

<template>
  <div ref="root" class="relative inline-flex rounded-lg border border-border bg-surface p-0.5" role="tablist">
    <button
      v-for="o in options"
      :key="o.value"
      type="button"
      role="tab"
      :aria-selected="model === o.value"
      :data-value="o.value"
      class="relative cursor-pointer rounded-md px-2.5 font-medium text-muted transition-colors duration-150 hover:text-fg"
      :class="size === 'md' ? 'h-7 text-[13px]' : 'h-6 text-xs'"
      @click="model = o.value"
    >
      {{ o.label }}<span v-if="o.count !== undefined" class="tabular ml-1.5 text-faint">{{ o.count }}</span>
    </button>
    <!-- Active copy, clipped to the selected segment -->
    <div
      class="pointer-events-none absolute inset-0 flex p-0.5"
      :style="{ clipPath: clip, transition: ready ? 'clip-path 220ms var(--ease-in-out)' : 'none' }"
      aria-hidden="true"
    >
      <div class="absolute inset-0 rounded-lg bg-surface-3" />
      <span
        v-for="o in options"
        :key="o.value"
        class="relative flex items-center rounded-md px-2.5 font-medium text-fg"
        :class="size === 'md' ? 'h-7 text-[13px]' : 'h-6 text-xs'"
      >
        {{ o.label }}<span v-if="o.count !== undefined" class="tabular ml-1.5 text-muted">{{ o.count }}</span>
      </span>
    </div>
  </div>
</template>
