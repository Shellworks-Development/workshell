<script setup lang="ts">
import { Bug, ChevronRight, LogIn, LogOut, ScrollText, ShieldAlert, ShoppingCart, Timer, Zap } from 'lucide-vue-next'
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'
import type { TimelineItem } from '@/lib/api'
import { num, time } from '@/lib/format'
import Badge from './Badge.vue'

const props = defineProps<{ items: TimelineItem[]; pid: number; filter?: string[] }>()

const icons: Record<string, unknown> = { join: LogIn, leave: LogOut, event: Zap, purchase: ShoppingCart, log: ScrollText, error: Bug, span: Timer, detection: ShieldAlert }
const dotClass: Record<string, string> = {
  join: 'bg-success-soft text-success',
  leave: 'bg-surface-2 text-muted',
  event: 'bg-info-soft text-info',
  purchase: 'bg-accent-soft text-accent',
  log: 'bg-surface-2 text-muted',
  error: 'bg-danger-soft text-danger',
  span: 'bg-surface-2 text-muted',
  detection: 'bg-warning-soft text-warning',
}
const open = ref<Set<number>>(new Set())
function toggle(i: number) {
  const s = new Set(open.value)
  if (s.has(i)) s.delete(i)
  else s.add(i)
  open.value = s
}
const visible = computed(() => (props.filter?.length ? props.items.filter((i) => props.filter!.includes(i.kind)) : props.items))
const expandable = (it: TimelineItem) =>
  (it.kind === 'error' && it.data?.breadcrumbs?.length) ||
  ((it.kind === 'event' || it.kind === 'purchase') && it.data?.props && Object.keys(it.data.props).length) ||
  (it.kind === 'detection' && it.data?.details && Object.keys(it.data.details).length) ||
  (it.kind === 'log' && it.data?.attrs && Object.keys(it.data.attrs).length)
</script>

<template>
  <ol class="relative before:absolute before:top-3 before:bottom-3 before:left-[11px] before:w-px before:bg-border">
    <li v-if="visible.length === 0" class="py-6 pl-10 text-[13px] text-muted">Nothing recorded.</li>
    <li v-for="(it, i) in visible" :key="i" class="relative py-1.5 pl-9">
      <span class="absolute top-2 left-0 grid size-6 place-items-center rounded-full ring-4 ring-surface" :class="dotClass[it.kind]">
        <component :is="icons[it.kind]" class="size-3" />
      </span>
      <div
        class="flex min-w-0 items-baseline gap-2 text-[13px]"
        :class="expandable(it) ? 'cursor-pointer' : ''"
        @click="expandable(it) && toggle(i)"
      >
        <span class="mono w-[4.6rem] shrink-0 text-faint">{{ time(it.ts) }}</span>
        <div class="min-w-0 flex-1">
          <span v-if="it.kind === 'error'" class="font-medium text-danger">
            <RouterLink :to="{ name: 'issue', params: { pid, iid: it.data.issueId } }" class="hover:underline" @click.stop>{{ it.title }}</RouterLink>
          </span>
          <span v-else-if="it.kind === 'span'">
            <span class="mono">{{ it.title }}</span>
            <span class="tabular ml-2 text-muted">{{ num(it.data.durationMs) }} ms</span>
            <Badge v-if="it.data.status !== 'ok'" tone="danger" class="ml-1">{{ it.data.status }}</Badge>
          </span>
          <span v-else-if="it.kind === 'detection'" class="font-medium text-warning">
            {{ it.title }}
            <Badge :tone="it.level === 'high' ? 'danger' : it.level === 'medium' ? 'warning' : 'neutral'" class="ml-1">{{ it.level }}</Badge>
            <span v-if="it.data.action" class="ml-1 font-normal text-muted">→ {{ it.data.action }}</span>
          </span>
          <span v-else-if="it.kind === 'purchase'" class="font-medium">Purchase <span class="tabular text-accent">R$ {{ num(it.data.value ?? 0) }}</span></span>
          <span v-else-if="it.kind === 'event'" class="mono">{{ it.title }}<span v-if="it.data.value != null" class="tabular ml-2 text-muted">{{ num(it.data.value) }}</span></span>
          <span v-else-if="it.kind === 'log'" class="mono break-all" :class="it.level === 'error' ? 'text-danger' : it.level === 'warn' ? 'text-warning' : 'text-muted'">{{ it.title }}</span>
          <span v-else class="font-medium">{{ it.title }}<span v-if="it.detail" class="mono ml-2 font-normal text-faint">{{ it.detail.slice(0, 8) }}</span></span>
        </div>
        <ChevronRight v-if="expandable(it)" class="size-3.5 shrink-0 text-faint transition-transform duration-200" :class="open.has(i) ? 'rotate-90' : ''" />
      </div>
      <div v-if="open.has(i)" class="mt-1.5 ml-[5.1rem] rounded-md border border-border bg-surface-2 p-2.5 font-mono text-[12px]">
        <template v-if="it.kind === 'error'">
          <div class="mb-1 font-sans text-xs text-muted">Breadcrumbs</div>
          <div v-for="(b, j) in it.data.breadcrumbs" :key="j" class="flex gap-2">
            <span class="text-faint">{{ time(b.ts) }}</span><span class="text-muted">{{ b.category }}</span><span class="break-all">{{ b.message }}</span>
          </div>
        </template>
        <pre v-else class="whitespace-pre-wrap">{{ JSON.stringify(it.data.props ?? it.data.details ?? it.data.attrs, null, 2) }}</pre>
      </div>
    </li>
  </ol>
</template>
