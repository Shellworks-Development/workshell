<script setup lang="ts" generic="T extends string | number">
import { Check, ChevronDown, Search } from 'lucide-vue-next'
import { computed, nextTick, onBeforeUnmount, ref, useId, watch } from 'vue'

export interface SelectOption<V> {
  value: V
  label: string
  hint?: string
}

const props = withDefaults(
  defineProps<{
    options: SelectOption<T>[]
    placeholder?: string
    label?: string
    mono?: boolean
    /** Show a filter box; defaults to on for long lists. */
    searchable?: boolean
  }>(),
  { placeholder: 'Select…', searchable: undefined },
)
const model = defineModel<T>({ required: true })

const open = ref(false)
const trigger = ref<HTMLButtonElement | null>(null)
const list = ref<HTMLElement | null>(null)
const filterInput = ref<HTMLInputElement | null>(null)
const active = ref(0)
const filter = ref('')
const pos = ref({ top: 0, left: 0, right: 0, minWidth: 0, placement: 'bottom' as 'bottom' | 'top', align: 'left' as 'left' | 'right' })
const listId = useId()

const selected = computed(() => props.options.find((o) => o.value === model.value))
const showSearch = computed(() => props.searchable ?? props.options.length > 10)
const visible = computed(() => {
  const q = filter.value.trim().toLowerCase()
  return q ? props.options.filter((o) => o.label.toLowerCase().includes(q)) : props.options
})

function place(flip = false) {
  const r = trigger.value?.getBoundingClientRect()
  if (!r) return
  const below = window.innerHeight - r.bottom
  const placement = flip ? (below < 280 && r.top > below ? 'top' : 'bottom') : pos.value.placement
  // Open toward the side with more room so the list never runs off-screen.
  const align = r.left + r.width / 2 > window.innerWidth / 2 ? 'right' : 'left'
  pos.value = {
    top: placement === 'bottom' ? r.bottom + 6 : r.top - 6,
    left: r.left,
    right: window.innerWidth - r.right,
    minWidth: r.width,
    placement,
    align,
  }
}

async function show() {
  if (open.value) return
  place(true)
  filter.value = ''
  active.value = Math.max(0, props.options.findIndex((o) => o.value === model.value))
  open.value = true
  await nextTick()
  if (showSearch.value) filterInput.value?.focus()
  else list.value?.focus()
  scrollActive()
}

function hide(refocus = true) {
  if (!open.value) return
  open.value = false
  if (refocus) trigger.value?.focus()
}

function choose(o: SelectOption<T>) {
  model.value = o.value
  hide()
}

function scrollActive() {
  nextTick(() => list.value?.querySelector<HTMLElement>(`[data-index="${active.value}"]`)?.scrollIntoView({ block: 'nearest' }))
}

watch(filter, () => (active.value = 0))

function onKey(e: KeyboardEvent) {
  const n = visible.value.length
  switch (e.key) {
    case 'ArrowDown':
      e.preventDefault()
      active.value = (active.value + 1) % Math.max(n, 1)
      scrollActive()
      break
    case 'ArrowUp':
      e.preventDefault()
      active.value = (active.value - 1 + n) % Math.max(n, 1)
      scrollActive()
      break
    case 'Home':
      e.preventDefault()
      active.value = 0
      scrollActive()
      break
    case 'End':
      e.preventDefault()
      active.value = n - 1
      scrollActive()
      break
    case 'Enter':
    case ' ':
      if (e.key === ' ' && showSearch.value) return
      e.preventDefault()
      if (visible.value[active.value]) choose(visible.value[active.value]!)
      break
    case 'Escape':
      e.preventDefault()
      e.stopPropagation()
      hide()
      break
    case 'Tab':
      hide(false)
      break
    default:
      // Typeahead: jump to the next option starting with the typed letter.
      if (!showSearch.value && e.key.length === 1) {
        const k = e.key.toLowerCase()
        const start = active.value + 1
        for (let i = 0; i < n; i++) {
          const idx = (start + i) % n
          if (visible.value[idx]!.label.toLowerCase().startsWith(k)) {
            active.value = idx
            scrollActive()
            break
          }
        }
      }
  }
}

function onTriggerKey(e: KeyboardEvent) {
  if (['ArrowDown', 'ArrowUp', 'Enter', ' '].includes(e.key)) {
    e.preventDefault()
    show()
  }
}

function onOutside(e: Event) {
  const t = e.target as Node
  if (!list.value?.parentElement?.contains(t) && !trigger.value?.contains(t)) hide(false)
}
function onViewportChange(e: Event) {
  // Scrolling inside the list itself doesn't move the trigger.
  if (e.type === 'scroll' && list.value?.contains(e.target as Node)) return
  const r = trigger.value?.getBoundingClientRect()
  // Follow the trigger; close only once it has left the viewport.
  if (!r || r.bottom < 0 || r.top > window.innerHeight) hide(false)
  else place()
}
watch(open, (o) => {
  const m = o ? 'addEventListener' : 'removeEventListener'
  document[m]('pointerdown', onOutside, true)
  window[m]('scroll', onViewportChange, true)
  window[m]('resize', onViewportChange)
})
onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', onOutside, true)
  window.removeEventListener('scroll', onViewportChange, true)
  window.removeEventListener('resize', onViewportChange)
})
</script>

<template>
  <div class="inline-flex min-w-0">
    <button
      ref="trigger"
      type="button"
      class="input inline-flex w-full cursor-pointer items-center gap-2 text-left hover:border-border-strong"
      :class="{ 'border-accent ring-2 ring-accent/20': open }"
      aria-haspopup="listbox"
      :aria-expanded="open"
      :aria-controls="listId"
      :aria-label="label"
      @click="open ? hide() : show()"
      @keydown="onTriggerKey"
    >
      <span class="min-w-0 flex-1 truncate" :class="[{ 'text-faint': !selected }, mono ? 'mono' : '']">
        {{ selected?.label ?? placeholder }}
      </span>
      <ChevronDown class="size-3.5 shrink-0 text-faint transition-transform duration-200" :class="{ 'rotate-180': open }" />
    </button>

    <Teleport to="body">
      <Transition name="pop">
        <div
          v-if="open"
          class="fixed z-[80] max-w-[min(22rem,calc(100vw-1rem))] overflow-hidden rounded-xl border border-border bg-surface shadow-(--shadow-pop)"
          :style="{
            top: `${pos.top}px`,
            left: `${pos.left}px`,
            minWidth: `${pos.minWidth}px`,
            transformOrigin: pos.placement === 'bottom' ? 'top left' : 'bottom left',
            translate: pos.placement === 'top' ? '0 -100%' : undefined,
          }"
        >
          <div v-if="showSearch" class="relative border-b border-border">
            <Search class="pointer-events-none absolute top-2.5 left-2.5 size-3.5 text-faint" />
            <input
              ref="filterInput"
              v-model="filter"
              class="h-9 w-full bg-transparent pr-2 pl-8 text-[13px] outline-none placeholder:text-faint"
              placeholder="Filter…"
              @keydown="onKey"
            />
          </div>
          <ul
            :id="listId"
            ref="list"
            role="listbox"
            tabindex="-1"
            class="max-h-72 overflow-y-auto overscroll-contain p-1 outline-none"
            :aria-activedescendant="`${listId}-${active}`"
            @keydown="onKey"
          >
            <li v-if="visible.length === 0" class="px-2.5 py-2 text-[13px] text-faint">No matches</li>
            <li
              v-for="(o, i) in visible"
              :id="`${listId}-${i}`"
              :key="String(o.value)"
              role="option"
              :data-index="i"
              :aria-selected="o.value === model"
              class="flex cursor-pointer items-center gap-2 rounded-lg px-2.5 py-1.5 text-[13px]"
              :class="i === active ? 'bg-surface-2 text-fg' : 'text-muted'"
              @pointermove="active = i"
              @click="choose(o)"
            >
              <span class="min-w-0 flex-1 truncate" :class="mono ? 'mono' : ''">{{ o.label }}</span>
              <span v-if="o.hint" class="tabular shrink-0 text-xs text-faint">{{ o.hint }}</span>
              <Check class="size-3.5 shrink-0 text-accent" :class="o.value === model ? 'opacity-100' : 'opacity-0'" />
            </li>
          </ul>
        </div>
      </Transition>
    </Teleport>
  </div>
</template>
