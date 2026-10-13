<script setup lang="ts">
import { Check, Copy } from 'lucide-vue-next'
import { ref } from 'vue'
import { toast } from '@/lib/overlay'

const props = defineProps<{ text: string; label?: string }>()
const copied = ref(false)
let timer: ReturnType<typeof setTimeout> | undefined

async function copy() {
  try {
    await navigator.clipboard.writeText(props.text)
    copied.value = true
    clearTimeout(timer)
    timer = setTimeout(() => (copied.value = false), 1500)
  } catch {
    toast.error('Couldn’t copy', 'Clipboard access needs HTTPS or localhost.')
  }
}
</script>

<template>
  <button type="button" class="btn btn-sm" @click="copy">
    <!-- Icons crossfade in place; feedback that the copy happened. -->
    <span class="relative grid size-3.5 place-items-center">
      <Copy
        class="absolute size-3.5 transition-[opacity,transform,filter] duration-200 ease-(--ease-out)"
        :class="copied ? 'scale-75 opacity-0 blur-[2px]' : ''"
      />
      <Check
        class="absolute size-3.5 text-success transition-[opacity,transform,filter] duration-200 ease-(--ease-out)"
        :class="copied ? '' : 'scale-75 opacity-0 blur-[2px]'"
      />
    </span>
    {{ copied ? 'Copied' : (label ?? 'Copy') }}
  </button>
</template>
