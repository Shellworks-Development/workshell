<script setup lang="ts">
import { Camera, Check, Monitor } from 'lucide-vue-next'
import { reactive, ref } from 'vue'
import Avatar from '@/components/Avatar.vue'
import ErrorBanner from '@/components/ErrorBanner.vue'
import PageHeader from '@/components/PageHeader.vue'
import { api, type User } from '@/lib/api'
import { toast } from '@/lib/overlay'
import { accentKeys, activeTheme, auth, prefs, setPrefs } from '@/lib/state'
import { themes } from '@/lib/themes'

const form = reactive({ name: auth.user?.name ?? '', handle: auth.user?.handle ?? '', title: auth.user?.title ?? '' })
const avatar = ref(auth.user?.avatar ?? '')
const error = ref<string | null>(null)
const saving = ref(false)

async function save() {
  error.value = null
  saving.value = true
  try {
    auth.user = await api.patch<User>('/me', { ...form, avatar: avatar.value })
    toast('Profile saved')
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    saving.value = false
  }
}

// Resize uploads to 160px WebP in the browser so avatars stay small.
const fileInput = ref<HTMLInputElement | null>(null)
async function onFile(e: Event) {
  const file = (e.target as HTMLInputElement).files?.[0]
  if (!file) return
  try {
    const bitmap = await createImageBitmap(file)
    const size = 160
    const canvas = document.createElement('canvas')
    canvas.width = canvas.height = size
    const ctx = canvas.getContext('2d')!
    const s = Math.min(bitmap.width, bitmap.height)
    ctx.drawImage(bitmap, (bitmap.width - s) / 2, (bitmap.height - s) / 2, s, s, 0, 0, size, size)
    avatar.value = canvas.toDataURL('image/webp', 0.85)
  } catch {
    toast.error('Couldn’t read that image')
  }
}

const pw = reactive({ currentPassword: '', newPassword: '' })
const pwError = ref<string | null>(null)
async function changePassword() {
  pwError.value = null
  try {
    await api.post('/auth/password', pw)
    pw.currentPassword = ''
    pw.newPassword = ''
    toast(auth.hasPassword ? 'Password changed' : 'Password set', 'Your other sessions were signed out.')
    auth.hasPassword = true
  } catch (e) {
    pwError.value = (e as Error).message
  }
}

const groups = ['Workshell', 'Catppuccin', 'Rosé Pine', 'Other'] as const
</script>

<template>
  <PageHeader title="Profile & preferences" />

  <div class="max-w-3xl space-y-6">
    <form class="card" @submit.prevent="save">
      <div class="p-5">
        <h2 class="text-base font-semibold">Profile</h2>
        <p class="mt-1 text-[13px] text-muted">How you appear to your team in comments, assignments and mentions.</p>
        <ErrorBanner :message="error" class="mt-4" />
        <div class="mt-5 flex flex-wrap items-start gap-6">
          <div class="flex flex-col items-center gap-2">
            <button type="button" class="group relative cursor-pointer rounded-full" aria-label="Change avatar" @click="fileInput?.click()">
              <Avatar :name="form.name || '?'" :src="avatar" :size="80" />
              <span class="absolute inset-0 grid place-items-center rounded-full bg-black/45 text-white opacity-0 transition-opacity group-hover:opacity-100">
                <Camera class="size-5" />
              </span>
            </button>
            <button v-if="avatar" type="button" class="text-xs text-muted hover:text-fg" @click="avatar = ''">Remove</button>
            <input ref="fileInput" type="file" accept="image/png,image/jpeg,image/webp" class="hidden" @change="onFile" />
          </div>
          <div class="grid min-w-60 flex-1 gap-4 sm:grid-cols-2">
            <div><label class="label" for="pname">Name</label><input id="pname" v-model="form.name" class="input" required /></div>
            <div>
              <label class="label" for="phandle">Handle</label>
              <div class="relative"><span class="absolute top-1.5 left-2.5 text-faint">@</span><input id="phandle" v-model="form.handle" class="input pl-6" required /></div>
            </div>
            <div class="sm:col-span-2"><label class="label" for="ptitle">Title</label><input id="ptitle" v-model="form.title" class="input" placeholder="Lead scripter" /></div>
          </div>
        </div>
      </div>
      <div class="flex items-center justify-between rounded-b-xl border-t border-border bg-surface-2/50 px-5 py-3">
        <span class="text-xs text-muted">{{ auth.user?.email }}</span>
        <button class="btn btn-primary" :disabled="saving">Save</button>
      </div>
    </form>

    <div class="card p-5">
      <h2 class="text-base font-semibold">Appearance</h2>
      <p class="mt-1 text-[13px] text-muted">Saved to your account and applied on every device.</p>
      <div class="mt-5 space-y-6">
        <div>
          <div class="label">Theme</div>
          <button
            class="mb-3 flex cursor-pointer items-center gap-2 rounded-md border px-3 py-2 text-[13px]"
            :class="prefs.theme === 'system' ? 'border-fg/40 bg-surface-2' : 'border-border hover:border-border-strong'"
            :aria-pressed="prefs.theme === 'system'"
            @click="setPrefs({ theme: 'system' })"
          >
            <Monitor class="size-4" /> Match system (Workshell Light / Dark)
          </button>
          <div v-for="group in groups" :key="group" class="mb-3">
            <div class="mb-1.5 text-xs text-muted">{{ group }}</div>
            <div class="grid grid-cols-2 gap-2 sm:grid-cols-4">
              <button
                v-for="t in themes.filter((x) => x.group === group)"
                :key="t.id"
                class="cursor-pointer overflow-hidden rounded-lg border-2 text-left transition-colors"
                :class="prefs.theme === t.id ? 'border-accent' : 'border-border hover:border-border-strong'"
                :aria-pressed="prefs.theme === t.id"
                @click="setPrefs({ theme: t.id })"
              >
                <!-- Mini preview of the theme -->
                <div class="flex h-14 gap-1.5 p-2" :style="{ background: t.p.bg }">
                  <div class="w-6 rounded" :style="{ background: t.p.surface2 }" />
                  <div class="flex-1 rounded p-1.5" :style="{ background: t.p.surface, border: `1px solid ${t.p.border}` }">
                    <div class="h-1.5 w-10 rounded-full" :style="{ background: t.p.fg }" />
                    <div class="mt-1.5 flex gap-1">
                      <span v-for="c in t.p.chart.slice(0, 4)" :key="c" class="h-1.5 w-3 rounded-full" :style="{ background: c }" />
                    </div>
                  </div>
                </div>
                <div class="flex items-center justify-between px-2.5 py-1.5 text-[12.5px]">
                  <span>{{ t.label }}</span>
                  <Check v-if="prefs.theme === t.id" class="size-3.5 text-accent" />
                </div>
              </button>
            </div>
          </div>
        </div>
        <div>
          <div class="label">Accent color</div>
          <div class="flex flex-wrap gap-2">
            <button
              v-for="a in accentKeys"
              :key="a.key"
              class="flex cursor-pointer items-center gap-2 rounded-md border px-2.5 py-1.5 text-[13px]"
              :class="prefs.accent === a.key ? 'border-fg/40 bg-surface-2' : 'border-border hover:border-border-strong'"
              :aria-pressed="prefs.accent === a.key"
              @click="setPrefs({ accent: a.key })"
            >
              <span class="size-3.5 rounded-full" :style="{ background: activeTheme.p.accents[a.key] }" /> {{ a.label }}
            </button>
          </div>
        </div>
        <div>
          <div class="label">Timestamps</div>
          <div class="flex flex-wrap gap-2">
            <button
              v-for="f in ['relative', 'absolute'] as const"
              :key="f"
              class="cursor-pointer rounded-md border px-3 py-1.5 text-[13px]"
              :class="prefs.timeFormat === f ? 'border-fg/40 bg-surface-2' : 'border-border hover:border-border-strong'"
              @click="setPrefs({ timeFormat: f })"
            >
              {{ f === 'relative' ? 'Relative (5m ago)' : 'Absolute (Oct 13, 14:02)' }}
            </button>
          </div>
        </div>
      </div>
    </div>

    <form v-if="auth.sso.passwordLogin" class="card" @submit.prevent="changePassword">
      <div class="p-5">
        <h2 class="text-base font-semibold">Password</h2>
        <p v-if="!auth.hasPassword" class="mt-1 text-[13px] text-muted">You sign in with single sign-on. Set a password to also sign in with your email.</p>
        <ErrorBanner :message="pwError" class="mt-4" />
        <div class="mt-4 grid gap-4 sm:grid-cols-2">
          <div v-if="auth.hasPassword"><label class="label" for="cur">Current password</label><input id="cur" v-model="pw.currentPassword" class="input" type="password" autocomplete="current-password" required /></div>
          <div><label class="label" for="new">New password</label><input id="new" v-model="pw.newPassword" class="input" type="password" autocomplete="new-password" minlength="8" required /></div>
        </div>
      </div>
      <div class="flex justify-end rounded-b-xl border-t border-border bg-surface-2/50 px-5 py-3"><button class="btn btn-primary">{{ auth.hasPassword ? 'Change password' : 'Set password' }}</button></div>
    </form>
  </div>
</template>
