import { computed, reactive, ref, watch } from 'vue'
import { api, type Notification, type ProjectSummary, type User } from './api'
import { themeById, themes, themeVars, type AccentKey, type Theme } from './themes'

export const auth = reactive({
  loaded: false,
  setupRequired: false,
  user: null as User | null,
  publicUrl: '',
})

export async function loadAuth() {
  const s = await api.get<{ setupRequired: boolean; user: User | null; publicUrl: string }>('/auth/state')
  auth.setupRequired = s.setupRequired
  auth.user = s.user
  auth.publicUrl = s.publicUrl
  auth.loaded = true
  if (s.user?.prefs) applyPrefs(s.user.prefs)
}

export const projects = ref<ProjectSummary[]>([])

export async function loadProjects() {
  projects.value = await api.get<ProjectSummary[]>('/projects')
}

// ---- Preferences (stored on the user, mirrored to localStorage) -------------

/** 'system' follows the OS between Workshell Light and Dark; otherwise a theme id. */
export type ThemePref = string
export interface Prefs {
  theme: ThemePref
  accent: AccentKey
  timeFormat: 'relative' | 'absolute'
}

export const accentKeys: { key: AccentKey; label: string }[] = [
  { key: 'violet', label: 'Violet' },
  { key: 'blue', label: 'Blue' },
  { key: 'teal', label: 'Teal' },
  { key: 'pink', label: 'Pink' },
  { key: 'orange', label: 'Orange' },
  { key: 'green', label: 'Green' },
]

function stored(key: string, fallback: string) {
  try {
    return localStorage.getItem(key) ?? fallback
  } catch {
    return fallback
  }
}
function store(key: string, value: string) {
  try {
    localStorage.setItem(key, value)
  } catch {
    // storage unavailable
  }
}

export const prefs = reactive<Prefs>({
  theme: stored('workshell-theme', 'system'),
  accent: stored('workshell-accent', 'violet') as AccentKey,
  timeFormat: stored('workshell-time', 'relative') as Prefs['timeFormat'],
})

const media = matchMedia('(prefers-color-scheme: dark)')
const systemDark = ref(media.matches)
media.addEventListener('change', (e) => (systemDark.value = e.matches))

/** The theme actually shown. */
export const activeTheme = computed<Theme>(() => (prefs.theme === 'system' ? themeById(systemDark.value ? 'dark' : 'light') : themeById(prefs.theme)))
export const dark = computed(() => activeTheme.value.dark)

watch(
  [activeTheme, () => prefs.accent],
  ([theme, accent]) => {
    const root = document.documentElement
    root.classList.toggle('dark', theme.dark)
    const vars = themeVars(theme, accent)
    for (const [k, v] of Object.entries(vars)) root.style.setProperty(k, v)
    // Status bar / browser chrome color matches the page background.
    for (const meta of document.querySelectorAll<HTMLMetaElement>('meta[name="theme-color"]')) meta.content = theme.p.bg
    // theme.js applies this before first paint on the next load.
    store('workshell-theme-vars', JSON.stringify({ dark: theme.dark, vars }))
  },
  { immediate: true },
)
watch(
  prefs,
  (p) => {
    store('workshell-theme', p.theme)
    store('workshell-accent', p.accent)
    store('workshell-time', p.timeFormat)
  },
  { deep: true },
)

function applyPrefs(raw: unknown) {
  const p = (raw ?? {}) as Partial<Prefs>
  if (typeof p.theme === 'string' && (p.theme === 'system' || themes.some((t) => t.id === p.theme))) prefs.theme = p.theme
  if (p.accent && accentKeys.some((a) => a.key === p.accent)) prefs.accent = p.accent
  if (p.timeFormat === 'relative' || p.timeFormat === 'absolute') prefs.timeFormat = p.timeFormat
}

let saveTimer: ReturnType<typeof setTimeout> | undefined
/** Updates preferences locally and saves them to the user's profile. */
export function setPrefs(patch: Partial<Prefs>) {
  Object.assign(prefs, patch)
  clearTimeout(saveTimer)
  saveTimer = setTimeout(() => {
    api.patch('/me', { prefs: { ...prefs } }).catch(() => {})
  }, 400)
}

// ---- Notifications ------------------------------------------------------------

export const inbox = reactive({ notifications: [] as Notification[], unread: 0 })

export async function loadInbox() {
  const res = await api.get<{ notifications: Notification[]; unread: number }>('/notifications')
  inbox.notifications = res.notifications
  inbox.unread = res.unread
}

export async function markRead(ids: number[] = []) {
  await api.post('/notifications/read', { ids })
  await loadInbox()
}

// ---- Time range (shared across pages, remembered) ---------------------------

export const range = ref(stored('workshell-range', '24h'))
watch(range, (v) => store('workshell-range', v))
