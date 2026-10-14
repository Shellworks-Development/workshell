<script setup lang="ts">
import { RouterView } from 'vue-router'
import CommandPalette from './components/CommandPalette.vue'
import OverlayHost from './components/OverlayHost.vue'
import SendDialog from './components/whiteboard/SendDialog.vue'
import { auth } from './lib/state'
</script>

<template>
  <RouterView />
  <OverlayHost />
  <CommandPalette v-if="auth.user" />
  <SendDialog v-if="auth.user" />
</template>
