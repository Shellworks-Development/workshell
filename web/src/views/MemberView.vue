<script setup lang="ts">
import { Bug, MessageSquare } from 'lucide-vue-next'
import { RouterLink } from 'vue-router'
import Avatar from '@/components/Avatar.vue'
import Badge from '@/components/Badge.vue'
import EmptyState from '@/components/EmptyState.vue'
import ErrorBanner from '@/components/ErrorBanner.vue'
import RelTime from '@/components/RelTime.vue'
import { api, type User } from '@/lib/api'
import { auth } from '@/lib/state'
import { useAsync } from '@/lib/useAsync'

const props = defineProps<{ uid: string }>()
interface Member {
  user: User
  assigned: { id: number; projectId: number; project: string; title: string; status: string; lastSeen: number }[]
  activity: { kind: string; body: string; createdAt: number; issueId: number; projectId: number; title: string }[]
}
const { data, error } = useAsync(() => api.get<Member>(`/users/${props.uid}`), [() => props.uid])
const verbs: Record<string, string> = { comment: 'commented on', status: 'changed the status of', assign: 'changed the assignee of', github: 'linked GitHub for' }
</script>

<template>
  <ErrorBanner :message="error" />
  <template v-if="data">
    <div class="mb-8 flex flex-wrap items-center gap-5">
      <Avatar :name="data.user.name" :src="data.user.avatar" :size="72" />
      <div class="min-w-0">
        <h1 class="text-2xl font-semibold tracking-tight">{{ data.user.name }}</h1>
        <div class="mt-0.5 text-muted">@{{ data.user.handle }}<template v-if="data.user.title"> · {{ data.user.title }}</template></div>
        <div class="mt-2 flex gap-2">
          <Badge v-if="data.user.isAdmin">Admin</Badge>
          <span class="text-xs text-faint">Member since <RelTime :ts="data.user.createdAt" absolute-only /></span>
        </div>
      </div>
      <RouterLink v-if="data.user.id === auth.user?.id" to="/profile" class="btn ml-auto">Edit profile</RouterLink>
    </div>

    <div class="grid gap-6 lg:grid-cols-2">
      <section>
        <h2 class="section-title">Assigned issues</h2>
        <div class="card">
          <EmptyState v-if="data.assigned.length === 0" title="Nothing assigned" :icon="Bug">Open issues assigned to {{ data.user.name.split(' ')[0] }} show up here.</EmptyState>
          <RouterLink
            v-for="i in data.assigned"
            :key="i.id"
            :to="{ name: 'issue', params: { pid: i.projectId, iid: i.id } }"
            class="block border-b border-border px-4 py-3 last:border-0 hover:bg-surface-2"
          >
            <div class="truncate text-[13.5px] font-medium">{{ i.title }}</div>
            <div class="mt-0.5 text-xs text-muted">{{ i.project }} · last seen <RelTime :ts="i.lastSeen" /></div>
          </RouterLink>
        </div>
      </section>
      <section>
        <h2 class="section-title">Recent activity</h2>
        <div class="card">
          <EmptyState v-if="data.activity.length === 0" title="No activity yet" :icon="MessageSquare">Comments and triage show up here.</EmptyState>
          <RouterLink
            v-for="(a, i) in data.activity"
            :key="i"
            :to="{ name: 'issue', params: { pid: a.projectId, iid: a.issueId } }"
            class="block border-b border-border px-4 py-3 text-[13px] last:border-0 hover:bg-surface-2"
          >
            <div class="truncate"><span class="text-muted">{{ verbs[a.kind] ?? a.kind }}</span> <span class="font-medium">{{ a.title }}</span></div>
            <div v-if="a.body" class="mt-0.5 line-clamp-2 text-muted">{{ a.body }}</div>
            <div class="mt-0.5 text-[11px] text-faint"><RelTime :ts="a.createdAt" /></div>
          </RouterLink>
        </div>
      </section>
    </div>
  </template>
</template>
