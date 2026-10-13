<script setup lang="ts">
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AuthCard from '@/components/AuthCard.vue'
import ErrorBanner from '@/components/ErrorBanner.vue'
import { api } from '@/lib/api'
import { loadAuth } from '@/lib/state'

const router = useRouter()
const route = useRoute()
const email = ref('')
const password = ref('')
const error = ref<string | null>(null)
const busy = ref(false)

async function submit() {
  busy.value = true
  error.value = null
  try {
    await api.post('/auth/login', { email: email.value, password: password.value })
    await loadAuth()
    const next = typeof route.query.next === 'string' && route.query.next.startsWith('/') ? route.query.next : '/'
    router.replace(next)
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <AuthCard title="Sign in">
    <ErrorBanner :message="error" />
    <form class="space-y-4" @submit.prevent="submit">
      <div>
        <label class="label" for="email">Email</label>
        <input id="email" v-model="email" class="input" type="email" autocomplete="email" required autofocus />
      </div>
      <div>
        <label class="label" for="password">Password</label>
        <input id="password" v-model="password" class="input" type="password" autocomplete="current-password" required />
      </div>
      <button class="btn btn-primary w-full" :disabled="busy">Sign in</button>
    </form>
  </AuthCard>
</template>
