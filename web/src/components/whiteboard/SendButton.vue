<script setup lang="ts">
import { Presentation } from 'lucide-vue-next'
import { ref } from 'vue'
import { toast } from '@/lib/overlay'
import { useProject } from '@/lib/project'
import { sendToWhiteboard, type SendPayload } from '@/lib/whiteboard'

/** "Send to whiteboard" for any page: `build` returns the snapshot to send. */
const props = withDefaults(defineProps<{ build: () => SendPayload | Promise<SendPayload>; label?: string; small?: boolean; iconOnly?: boolean }>(), {
  label: 'Send to whiteboard',
})
const { pid } = useProject()
const busy = ref(false)

async function go() {
  busy.value = true
  try {
    sendToWhiteboard(pid.value, await props.build())
  } catch (e) {
    toast.error('Could not capture this', (e as Error).message)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <button
    type="button"
    class="btn"
    :class="[small && 'btn-sm', iconOnly && 'btn-icon']"
    :disabled="busy"
    :title="label"
    :aria-label="iconOnly ? label : undefined"
    @click.stop="go"
  >
    <Presentation class="size-4" />
    <span v-if="!iconOnly" class="max-sm:hidden">{{ label }}</span>
  </button>
</template>
