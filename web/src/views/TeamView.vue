<script setup lang="ts">
import { Trash2, UserPlus } from 'lucide-vue-next'
import { reactive, ref } from 'vue'
import { RouterLink } from 'vue-router'
import Avatar from '@/components/Avatar.vue'
import Badge from '@/components/Badge.vue'
import ErrorBanner from '@/components/ErrorBanner.vue'
import Modal from '@/components/Modal.vue'
import PageHeader from '@/components/PageHeader.vue'
import RelTime from '@/components/RelTime.vue'
import { api, type User } from '@/lib/api'
import { confirmDialog, toast } from '@/lib/overlay'
import { auth } from '@/lib/state'
import { useAsync } from '@/lib/useAsync'

const users = useAsync(() => api.get<User[]>('/users'))
const inviting = ref(false)
const invite = reactive({ name: '', email: '', password: '', isAdmin: false })
const inviteError = ref<string | null>(null)
async function addUser() {
  inviteError.value = null
  try {
    await api.post('/users', invite)
    toast(`Added ${invite.name}`, `They can sign in as ${invite.email}.`)
    Object.assign(invite, { name: '', email: '', password: '', isAdmin: false })
    inviting.value = false
    await users.reload()
  } catch (e) {
    inviteError.value = (e as Error).message
  }
}
async function removeUser(u: User) {
  const ok = await confirmDialog({ title: `Remove ${u.name}?`, message: `${u.email} will be signed out and lose access to every project.`, confirmLabel: 'Remove', danger: true })
  if (!ok) return
  await api.del(`/users/${u.id}`)
  await users.reload()
  toast(`Removed ${u.name}`)
}
</script>

<template>
  <PageHeader title="Team" subtitle="Everyone here can see every project. Mention teammates in issue comments with @handle.">
    <button v-if="auth.user?.isAdmin" class="btn btn-primary" @click="inviting = true"><UserPlus class="size-4" /> Add member</button>
  </PageHeader>

  <div class="card">
    <RouterLink
      v-for="u in users.data.value"
      :key="u.id"
      :to="`/team/${u.id}`"
      class="flex items-center gap-3 border-b border-border px-4 py-3 last:border-0 hover:bg-surface-2"
    >
      <Avatar :name="u.name" :src="u.avatar" :size="36" />
      <div class="min-w-0 flex-1">
        <div class="flex items-center gap-2">
          <span class="truncate font-medium">{{ u.name }}</span>
          <Badge v-if="u.isAdmin">Admin</Badge>
          <Badge v-if="u.id === auth.user?.id" tone="accent">You</Badge>
        </div>
        <div class="truncate text-[13px] text-muted">@{{ u.handle }}<template v-if="u.title"> · {{ u.title }}</template></div>
      </div>
      <span class="hidden text-xs text-faint sm:block">joined <RelTime :ts="u.createdAt" /></span>
      <button
        v-if="auth.user?.isAdmin && u.id !== auth.user.id"
        class="btn btn-ghost btn-icon text-danger"
        aria-label="Remove member"
        @click.prevent="removeUser(u)"
      >
        <Trash2 class="size-4" />
      </button>
    </RouterLink>
  </div>

  <Modal :open="inviting" title="Add a team member" description="They'll sign in with this email and password, and can change the password later." @close="inviting = false">
    <form id="invite" class="space-y-3" @submit.prevent="addUser">
      <ErrorBanner :message="inviteError" />
      <div><label class="label" for="iname">Name</label><input id="iname" v-model="invite.name" class="input" required data-autofocus /></div>
      <div><label class="label" for="iemail">Email</label><input id="iemail" v-model="invite.email" class="input" type="email" required /></div>
      <div><label class="label" for="ipass">Initial password</label><input id="ipass" v-model="invite.password" class="input" type="password" minlength="8" required autocomplete="new-password" /></div>
      <label class="flex items-center gap-2 text-[13px]"><input v-model="invite.isAdmin" type="checkbox" class="accent-(--fg)" /> Admin (can add and remove members)</label>
    </form>
    <template #footer>
      <button class="btn" @click="inviting = false">Cancel</button>
      <button class="btn btn-primary" type="submit" form="invite">Add member</button>
    </template>
  </Modal>
</template>
