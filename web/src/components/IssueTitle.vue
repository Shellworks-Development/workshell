<script setup lang="ts">
import { GitPullRequestArrow } from 'lucide-vue-next'
import type { Issue } from '@/lib/api'
import Badge from './Badge.vue'
defineProps<{ issue: Pick<Issue, 'title' | 'culprit' | 'side' | 'status' | 'regressedAt' | 'firstSeen'> & Partial<Pick<Issue, 'githubNumber'>> }>()
const DAY = 86_400_000
</script>

<template>
  <div class="min-w-0">
    <div class="truncate text-[13.5px] font-medium text-fg">{{ issue.title }}</div>
    <div class="mt-1 flex min-w-0 items-center gap-1.5 text-xs text-muted">
      <Badge v-if="issue.regressedAt && issue.status === 'unresolved'" tone="warning">Regressed</Badge>
      <Badge v-else-if="Date.now() - issue.firstSeen < DAY" tone="accent">New</Badge>
      <Badge v-if="issue.status === 'resolved'" tone="success">Resolved</Badge>
      <Badge v-if="issue.status === 'ignored'">Ignored</Badge>
      <Badge :tone="issue.side === 'client' ? 'info' : 'neutral'">{{ issue.side }}</Badge>
      <span v-if="issue.githubNumber" class="flex shrink-0 items-center gap-0.5 font-mono"><GitPullRequestArrow class="size-3" />#{{ issue.githubNumber }}</span>
      <span class="truncate font-mono text-[12px]">{{ issue.culprit || 'unknown location' }}</span>
    </div>
  </div>
</template>
