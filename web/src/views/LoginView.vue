<script setup lang="ts">
import { Cloud, KeyRound } from 'lucide-vue-next'
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AuthCard from '@/components/AuthCard.vue'
import ErrorBanner from '@/components/ErrorBanner.vue'
import { api } from '@/lib/api'
import { auth, loadAuth } from '@/lib/state'

const router = useRouter()
const route = useRoute()
const email = ref('')
const password = ref('')
const error = ref<string | null>(typeof route.query.error === 'string' ? route.query.error : auth.ssoError || null)
const busy = ref(false)

const next = computed(() => (typeof route.query.next === 'string' && route.query.next.startsWith('/') && !route.query.next.startsWith('//') ? route.query.next : '/'))
const oidcHref = computed(() => `/api/auth/oidc/start?next=${encodeURIComponent(next.value)}`)
const hasSSO = computed(() => !!auth.sso.oidc || auth.sso.cfAccess)

async function signedIn() {
  await loadAuth()
  router.replace(next.value)
}

async function submit() {
  busy.value = true
  error.value = null
  try {
    await api.post('/auth/login', { email: email.value, password: password.value })
    await signedIn()
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    busy.value = false
  }
}

async function cloudflare() {
  busy.value = true
  error.value = null
  try {
    await api.post('/auth/cf-access', {})
    await signedIn()
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
    <div class="space-y-4">
      <div v-if="hasSSO" class="space-y-2">
        <a v-if="auth.sso.oidc" class="btn w-full" :class="{ 'btn-primary': !auth.sso.passwordLogin }" :href="oidcHref">
          <KeyRound class="size-4" /> Continue with {{ auth.sso.oidc.name }}
        </a>
        <button v-if="auth.sso.cfAccess" class="btn w-full" :disabled="busy" @click="cloudflare">
          <Cloud class="size-4" /> Continue with Cloudflare Access
        </button>
      </div>
      <div v-if="hasSSO && auth.sso.passwordLogin" class="flex items-center gap-3 py-1 text-xs text-faint" aria-hidden="true">
        <span class="h-px flex-1 bg-border" />or<span class="h-px flex-1 bg-border" />
      </div>
      <form v-if="auth.sso.passwordLogin" class="space-y-4" @submit.prevent="submit">
        <div>
          <label class="label" for="email">Email</label>
          <input id="email" v-model="email" class="input" type="email" autocomplete="email" required :autofocus="!hasSSO" />
        </div>
        <div>
          <label class="label" for="password">Password</label>
          <input id="password" v-model="password" class="input" type="password" autocomplete="current-password" required />
        </div>
        <button class="btn btn-primary w-full" :disabled="busy">Sign in</button>
      </form>
    </div>
  </AuthCard>
</template>
