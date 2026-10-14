<script setup lang="ts">
import { ExternalLink, FileCode2 } from 'lucide-vue-next'
import type { Frame } from '@/lib/api'
defineProps<{ frames: Frame[] | null; raw: string }>()
</script>

<template>
  <div v-if="frames && frames.length" class="overflow-hidden rounded-lg border border-border">
    <div
      v-for="(f, i) in frames"
      :key="i"
      class="flex items-baseline gap-3 border-b border-border px-3 py-2 last:border-0"
      :class="i === 0 ? 'bg-danger-soft/40' : 'bg-surface'"
    >
      <span class="mono w-5 shrink-0 text-right text-faint">{{ i + 1 }}</span>
      <div class="min-w-0 flex-1">
        <div class="mono break-all">
          <span :class="i === 0 ? 'font-semibold text-fg' : 'text-fg'">{{ f.script }}</span>
          <span v-if="f.line" class="text-faint">:{{ f.line }}</span>
          <span v-if="f.function" class="ml-2 text-accent">{{ f.function }}</span>
        </div>
        <component
          :is="f.url ? 'a' : 'div'"
          v-if="f.file"
          :href="f.url"
          target="_blank"
          rel="noopener noreferrer"
          class="mt-0.5 block font-mono text-[11.5px] break-all text-muted"
          :class="f.url ? 'hover:text-fg hover:underline' : ''"
        >
          <FileCode2 class="mr-1 inline size-3 align-[-2px]" />{{ f.file }}:{{ f.line }}
          <ExternalLink v-if="f.url" class="ml-1 inline size-3 align-[-2px]" />
        </component>
      </div>
    </div>
  </div>
  <pre v-else-if="raw" class="mono overflow-x-auto rounded-lg border border-border bg-surface-2 p-3 whitespace-pre-wrap">{{ raw }}</pre>
  <div v-else class="text-[13px] text-muted">No stack trace was captured for this event.</div>
</template>
