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
