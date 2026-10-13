<script setup lang="ts">
import { Bell, Bug, Check } from 'lucide-vue-next'
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import Avatar from '@/components/Avatar.vue'
import EmptyState from '@/components/EmptyState.vue'
import PageHeader from '@/components/PageHeader.vue'
import RelTime from '@/components/RelTime.vue'
import Segmented from '@/components/Segmented.vue'
import { inbox, loadInbox, markRead } from '@/lib/state'

const router = useRouter()
const filter = ref<'all' | 'unread'>('unread')
onMounted(() => loadInbox().catch(() => {}))
const list = computed(() => (filter.value === 'unread' ? inbox.notifications.filter((n) => !n.readAt) : inbox.notifications))
const verbs: Record<string, string> = { mention: 'mentioned you on', assigned: 'assigned you', comment: 'commented on', regression: 'Regression in' }

function open(n: (typeof inbox.notifications)[number]) {
  if (!n.readAt) markRead([n.id]).catch(() => {})
  router.push({ name: 'issue', params: { pid: n.projectId, iid: n.issueId } })
}
</script>

<template>
  <PageHeader title="Inbox" subtitle="Mentions, assignments, comments on your issues, and regressions.">
    <Segmented
      v-model="filter"
      :options="[
        { value: 'unread', label: 'Unread', count: String(inbox.unread) },
        { value: 'all', label: 'All' },
      ]"
      size="md"
    />
    <button class="btn" :disabled="!inbox.unread" @click="markRead()"><Check class="size-4" /> Mark all read</button>
  </PageHeader>
  <div class="card">
    <EmptyState v-if="list.length === 0" :title="filter === 'unread' ? 'All caught up' : 'No notifications yet'" :icon="Bell">
      You'll be notified when someone mentions or assigns you.
    </EmptyState>
    <button
      v-for="n in list"
      :key="n.id"
      class="flex w-full cursor-pointer items-start gap-3 border-b border-border px-4 py-3 text-left last:border-0 hover:bg-surface-2"
      @click="open(n)"
    >
      <Avatar v-if="n.actorName" :name="n.actorName" :src="n.actorAvatar" :size="28" />
      <span v-else class="grid size-7 shrink-0 place-items-center rounded-full bg-warning-soft text-warning"><Bug class="size-3.5" /></span>
      <div class="min-w-0 flex-1 text-[13.5px]">
        <div><span v-if="n.actorName" class="font-medium">{{ n.actorName }} </span><span class="text-muted">{{ verbs[n.kind] ?? n.kind }}</span> <span class="font-medium">{{ n.issueTitle }}</span></div>
        <div v-if="n.kind === 'mention' || n.kind === 'comment'" class="mt-1 line-clamp-2 text-[13px] text-muted">{{ n.body }}</div>
        <div class="mt-1 text-xs text-faint"><RelTime :ts="n.createdAt" /></div>
      </div>
      <span v-if="!n.readAt" class="mt-2 size-2 shrink-0 rounded-full bg-accent" />
    </button>
  </div>
</template>
