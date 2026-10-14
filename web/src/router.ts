import { createRouter, createWebHistory } from 'vue-router'
import { setUnauthorizedHandler } from './lib/api'
import { auth, loadAuth } from './lib/state'

const page = (name: string, title: string, component: () => Promise<unknown>, path = name) => ({
  path,
  name,
  component,
  meta: { title },
  props: true,
})

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/setup', component: () => import('./views/SetupView.vue'), meta: { public: true } },
    { path: '/login', component: () => import('./views/LoginView.vue'), meta: { public: true } },
    { path: '/account', redirect: '/profile' },
    {
      path: '/',
      component: () => import('./views/TeamLayout.vue'),
      children: [
        page('projects', 'Projects', () => import('./views/ProjectsView.vue'), ''),
        page('team', 'Team', () => import('./views/TeamView.vue')),
        page('member', 'Member', () => import('./views/MemberView.vue'), 'team/:uid'),
        page('inbox', 'Inbox', () => import('./views/InboxView.vue')),
        page('profile', 'Profile', () => import('./views/ProfileView.vue')),
      ],
    },
    {
      path: '/p/:pid',
      component: () => import('./views/ProjectLayout.vue'),
      props: (r) => ({ pid: Number(r.params.pid) }),
      children: [
        page('overview', 'Overview', () => import('./views/OverviewView.vue'), ''),
        page('issues', 'Issues', () => import('./views/IssuesView.vue')),
        { ...page('issue', 'Issue', () => import('./views/IssueView.vue'), 'issues/:iid'), props: (r: any) => ({ iid: Number(r.params.iid) }) },
        page('logs', 'Logs', () => import('./views/LogsView.vue')),
        page('sessions', 'Sessions', () => import('./views/SessionsView.vue')),
        page('session', 'Session', () => import('./views/SessionView.vue'), 'sessions/:sid'),
        page('players', 'Players', () => import('./views/PlayersView.vue')),
        page('player', 'Player', () => import('./views/PlayerView.vue'), 'players/:userId'),
        page('performance', 'Performance', () => import('./views/PerformanceView.vue')),
        page('servers', 'Servers', () => import('./views/ServersView.vue')),
        page('server', 'Server', () => import('./views/ServerView.vue'), 'servers/:jobId'),
        page('releases', 'Releases', () => import('./views/ReleasesView.vue')),
        page('release', 'Release', () => import('./views/ReleaseView.vue'), 'releases/:version'),
        page('security', 'Security', () => import('./views/SecurityView.vue')),
        page('analytics', 'Analytics', () => import('./views/AnalyticsView.vue')),
        page('events', 'Events', () => import('./views/EventsView.vue')),
        page('metrics', 'Metrics', () => import('./views/MetricsView.vue')),
        page('whiteboards', 'Whiteboards', () => import('./views/WhiteboardsView.vue')),
        {
          ...page('whiteboard', 'Whiteboard', () => import('./views/WhiteboardView.vue'), 'whiteboards/:bid'),
          props: (r: any) => ({ bid: Number(r.params.bid), focus: typeof r.query.focus === 'string' ? r.query.focus : undefined }),
          meta: { title: 'Whiteboard', fullBleed: true },
        },
        page('alerts', 'Alerts', () => import('./views/AlertsView.vue')),
        page('settings', 'Settings', () => import('./views/ProjectSettingsView.vue')),
      ],
    },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})

router.beforeEach(async (to) => {
  if (!auth.loaded) await loadAuth()
  if (auth.setupRequired) return to.path === '/setup' ? true : '/setup'
  if (to.path === '/setup') return '/'
  if (!auth.user && !to.meta.public) return { path: '/login', query: { next: to.fullPath } }
  if (auth.user && to.path === '/login') return '/'
  return true
})

router.afterEach((to) => {
  const title = typeof to.meta.title === 'string' ? to.meta.title : ''
  document.title = title ? `${title} · Workshell` : 'Workshell'
})

setUnauthorizedHandler(() => {
  auth.user = null
  const current = router.currentRoute.value
  if (!current.meta.public) router.replace({ path: '/login', query: { next: current.fullPath } })
})

// After a deploy, an open tab still references the previous build's page
// chunks, which no longer exist. Instead of navigation silently failing,
// reload into the new build at the page the user was going to (once).
function isChunkError(err: unknown) {
  const msg = err instanceof Error ? err.message : String(err)
  return /dynamically imported module|Importing a module script failed|module script|Failed to fetch/i.test(msg)
}
router.onError((err, to) => {
  if (!isChunkError(err)) return
  const key = 'workshell-reloaded-for'
  if (sessionStorage.getItem(key) === to.fullPath) return // already tried; don't loop
  sessionStorage.setItem(key, to.fullPath)
  window.location.assign(to.fullPath)
})
window.addEventListener('vite:preloadError', (e) => {
  e.preventDefault()
  window.location.reload()
})
router.afterEach(() => {
  try {
    sessionStorage.removeItem('workshell-reloaded-for')
  } catch {
    // ignore
  }
})
