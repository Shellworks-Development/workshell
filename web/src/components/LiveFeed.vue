<script setup lang="ts">
import { Bug, LogIn, LogOut, ShieldAlert, ShoppingCart } from 'lucide-vue-next'
import { ref, toRef } from 'vue'
import { RouterLink } from 'vue-router'
import type { LiveMsg } from '@/lib/api'
import { useLive } from '@/lib/live'
import RelTime from './RelTime.vue'

/* Recent activity streamed live: joins, leaves, errors, purchases, detections. */
const props = defineProps<{ pid: number }>()
interface Row {
  id: number
  ts: number
  kind: string
  text: string
  sub?: string
  to?: object
}
const rows = ref<Row[]>([])
let seq = 0

function add(r: Omit<Row, 'id'>) {
  rows.value = [{ ...r, id: ++seq }, ...rows.value].slice(0, 40)
}

const { connected } = useLive(toRef(props, 'pid'), (m: LiveMsg) => {
  const pid = props.pid
  switch (m.t) {
    case 'session_start':
      add({ ts: m.d.ts, kind: 'join', text: `Player ${m.d.userId} joined`, sub: m.d.isNew ? 'new player' : undefined, to: { name: 'session', params: { pid, sid: m.d.sessionKey } } })
      break
    case 'session_end':
      add({ ts: m.d.ts, kind: 'leave', text: `Player ${m.d.userId} left`, to: { name: 'session', params: { pid, sid: m.d.sessionKey } } })
      break
    case 'error':
      add({ ts: m.d.ts, kind: 'error', text: m.d.title, sub: m.d.culprit, to: { name: 'issue', params: { pid, iid: m.d.issueId } } })
      break
    case 'event':
      if (m.d.name === '$purchase') add({ ts: m.d.ts, kind: 'purchase', text: `R$ ${m.d.value ?? 0} purchase`, sub: m.d.userId ? `player ${m.d.userId}` : undefined })
      break
    case 'detection':
      add({ ts: m.d.ts, kind: 'detection', text: `${m.d.kind} detected`, sub: `${m.d.severity}${m.d.userId ? ` · player ${m.d.userId}` : ''}`, to: { name: 'security', params: { pid } } })
      break
  }
})

const icons: Record<string, unknown> = { join: LogIn, leave: LogOut, error: Bug, purchase: ShoppingCart, detection: ShieldAlert }
const tones: Record<string, string> = { join: 'text-success', leave: 'text-faint', error: 'text-danger', purchase: 'text-accent', detection: 'text-warning' }
</script>

<template>
  <div class="card flex min-h-0 flex-col">
    <div class="card-header">
      <span class="card-title">Live activity</span>
      <span class="flex items-center gap-1.5 text-xs" :class="connected ? 'text-success' : 'text-faint'">
        <span class="relative flex size-2">
          <span v-if="connected" class="absolute inline-flex size-full animate-ping rounded-full bg-success opacity-60" />
          <span class="relative inline-flex size-2 rounded-full" :class="connected ? 'bg-success' : 'bg-faint'" />
        </span>
        {{ connected ? 'Streaming' : 'Connecting…' }}
      </span>
    </div>
    <div class="max-h-[26rem] min-h-40 flex-1 overflow-y-auto">
      <div v-if="rows.length === 0" class="px-4 py-10 text-center text-[13px] text-muted">Waiting for activity from your game servers…</div>
      <TransitionGroup name="feed">
        <component
          :is="r.to ? RouterLink : 'div'"
          v-for="r in rows"
          :key="r.id"
          :to="r.to"
          class="flex items-start gap-2.5 border-b border-border px-4 py-2 text-[13px] last:border-0"
          :class="r.to ? 'hover:bg-surface-2' : ''"
        >
          <component :is="icons[r.kind]" class="mt-0.5 size-3.5 shrink-0" :class="tones[r.kind]" />
          <div class="min-w-0 flex-1">
            <div class="truncate">{{ r.text }}</div>
            <div v-if="r.sub" class="truncate font-mono text-[11.5px] text-faint">{{ r.sub }}</div>
          </div>
          <span class="shrink-0 text-[11px] text-faint"><RelTime :ts="r.ts" /></span>
        </component>
      </TransitionGroup>
    </div>
  </div>
</template>

<style scoped>
/* New rows slide in at the top; this is occasional, ambient activity. */
.feed-enter-active {
  transition:
    opacity 300ms var(--ease-out),
    transform 300ms var(--ease-out);
}
.feed-enter-from {
  opacity: 0;
  transform: translateY(-6px);
}
@media (prefers-reduced-motion: reduce) {
  .feed-enter-from {
    transform: none;
  }
}
</style>
