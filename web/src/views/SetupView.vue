<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import AuthCard from '@/components/AuthCard.vue'
import ErrorBanner from '@/components/ErrorBanner.vue'
import { api } from '@/lib/api'
import { loadAuth } from '@/lib/state'

const router = useRouter()
const name = ref('')
const email = ref('')
const password = ref('')
const error = ref<string | null>(null)
const busy = ref(false)

async function submit() {
  busy.value = true
  error.value = null
  try {
    await api.post('/auth/setup', { name: name.value, email: email.value, password: password.value })
    await loadAuth()
    router.replace('/')
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <AuthCard title="Welcome to Workshell" subtitle="Create the admin account for this instance.">
    <ErrorBanner :message="error" />
    <form class="space-y-4" @submit.prevent="submit">
      <div>
        <label class="label" for="name">Name</label>
        <input id="name" v-model="name" class="input" autocomplete="name" required />
      </div>
      <div>
        <label class="label" for="email">Email</label>
        <input id="email" v-model="email" class="input" type="email" autocomplete="email" required />
      </div>
      <div>
        <label class="label" for="password">Password</label>
        <input id="password" v-model="password" class="input" type="password" autocomplete="new-password" minlength="8" required />
        <p class="mt-1 text-xs text-faint">At least 8 characters.</p>
      </div>
      <button class="btn btn-primary w-full" :disabled="busy">Create account</button>
    </form>
  </AuthCard>
</template>
