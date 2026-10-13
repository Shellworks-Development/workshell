// Typed client for the Workshell dashboard API.

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

type Query = Record<string, string | number | boolean | undefined | null>

function withQuery(path: string, query?: Query) {
  if (!query) return path
  const params = new URLSearchParams()
  for (const [k, v] of Object.entries(query)) {
    if (v !== undefined && v !== null && v !== '') params.set(k, String(v))
  }
  const s = params.toString()
  return s ? `${path}?${s}` : path
}

let onUnauthorized: (() => void) | null = null
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn
}

async function request<T>(method: string, path: string, body?: unknown, query?: Query): Promise<T> {
  const res = await fetch(withQuery('/api' + path, query), {
    method,
    headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
    credentials: 'same-origin',
  })
  const text = await res.text()
  let data: any = null
  try {
    data = text ? JSON.parse(text) : null
  } catch {
    // non-JSON error page
  }
  if (!res.ok) {
    if (res.status === 401 && !path.startsWith('/auth/')) onUnauthorized?.()
    throw new ApiError(res.status, data?.error ?? `Request failed (${res.status})`)
  }
  return data as T
}

export const api = {
  get: <T>(path: string, query?: Query) => request<T>('GET', path, undefined, query),
  post: <T>(path: string, body: unknown = {}) => request<T>('POST', path, body),
  patch: <T>(path: string, body: unknown) => request<T>('PATCH', path, body),
  del: <T>(path: string) => request<T>('DELETE', path),
}

// ---- Shared types ---------------------------------------------------------

export interface Timeseries {
  bucket: number
  points: number[]
  series: { name: string; values: number[] }[]
}

export interface User {
  id: number
  email: string
  name: string
  handle: string
  title: string
  avatar: string
  isAdmin: boolean
  createdAt: number
  prefs?: Record<string, unknown>
}

export interface UserRef {
  id: number
  name: string
  handle: string
  avatar: string
}

export interface Notification {
  id: number
  kind: 'mention' | 'assigned' | 'comment' | 'regression' | string
  body: string
  createdAt: number
  readAt: number | null
  projectId: number
  issueId: number
  issueTitle: string
  actorName: string
  actorAvatar: string
  actorHandle: string
}

export interface Project {
  id: number
  name: string
  ingestKey: string
  createdAt: number
  logRetentionDays: number
  dataRetentionDays: number
  githubRepo: string
  hasGithubToken: boolean
  signingSecret: string
  requireSignature: boolean
  hashUserIds: boolean
  ingestRate: number
  clientRate: number
}

export interface ProjectSummary extends Project {
  livePlayers: number
  liveServers: number
  openIssues: number
  latestRelease: string
  lastEventAt: number
  activity: number[]
}

export type IssueStatus = 'unresolved' | 'resolved' | 'ignored'

export interface Issue {
  id: number
  title: string
  culprit: string
  level: string
  side: 'server' | 'client'
  status: IssueStatus
  firstSeen: number
  lastSeen: number
  eventCount: number
  userCount: number
  rangeEvents: number
  firstRelease: string
  lastRelease: string
  resolvedAt: number | null
  regressedAt: number | null
  commentCount: number
  githubUrl: string
  githubNumber: number | null
  assignee: UserRef | null
  spark?: number[]
}

export interface Frame {
  script: string
  line: number
  function?: string
  raw: string
  file?: string
  url?: string
}

export interface Breadcrumb {
  ts: number
  category: string
  message: string
  level?: string
  data?: Record<string, unknown>
}

export interface ErrorEvent {
  id: number
  ts: number
  count: number
  message: string
  stack: string
  frames: Frame[] | null
  level: string
  side: string
  jobId: string
  placeVersion: string
  release: string
  environment: string
  userId: number | null
  sessionKey: string
  script: string
  tags: Record<string, unknown>
  context: Record<string, unknown>
  breadcrumbs: Breadcrumb[]
}

export interface LogLine {
  id: number
  ts: number
  level: 'debug' | 'info' | 'warn' | 'error'
  message: string
  jobId: string
  userId: number | null
  source: string
  attrs: Record<string, unknown>
}

export interface GameServer {
  jobId: string
  placeId: number
  placeVersion: string
  release: string
  environment: string
  serverType: string
  maxPlayers: number
  startedAt: number
  lastSeen: number
  closedAt: number | null
  players: number
  fps: number
  heartbeatMs: number
  memoryMb: number
  pingMs: number
  live: boolean
}

export interface AnalyticsEvent {
  id: number
  ts: number
  name: string
  userId: number | null
  jobId: string
  value: number | null
  props: Record<string, unknown>
}

export function seriesOf(ts: Timeseries | undefined | null, name: string): number[] {
  return ts?.series.find((s) => s.name === name)?.values ?? []
}

export interface Activity {
  id: number
  kind: 'comment' | 'status' | 'assign' | 'github' | 'regression' | 'first_seen'
  body: string
  data: Record<string, any>
  createdAt: number
  editedAt: number | null
  user: UserRef | null
}

export interface Release {
  version: string
  commitSha: string
  branch: string
  message: string
  author: string
  placeVersion: string
  createdAt: number
  firstSeen: number | null
  lastSeen: number | null
  hasSourcemap: boolean
  commitUrl?: string
  errors: number
  newIssues: number
  sessions: number
  errorFreeRate: number
  liveServers: number
  livePlayers: number
  avgSessionMs: number
}

export interface Session {
  key: string
  userId: number
  jobId: string
  startedAt: number
  endedAt: number | null
  isNew: boolean
  platform: string
  country: string
  release: string
  errorCount: number
  attrs: Record<string, unknown>
}

export interface TimelineItem {
  ts: number
  kind: 'join' | 'leave' | 'event' | 'purchase' | 'log' | 'error' | 'span' | 'detection'
  title: string
  detail?: string
  level?: string
  data?: any
}

export interface Detection {
  id: number
  ts: number
  userId: number | null
  sessionKey: string
  jobId: string
  kind: string
  severity: 'low' | 'medium' | 'high'
  action: string
  source: 'game' | 'workshell'
  details: Record<string, unknown>
}

export interface AlertChannel {
  id: number
  name: string
  kind: 'discord' | 'webhook'
  url: string
  createdAt: number
}

export interface AlertRule {
  id: number
  name: string
  kind: string
  threshold: number
  windowMinutes: number
  cooldownMinutes: number
  channelId: number | null
  enabled: boolean
  lastFiredAt: number | null
}

/** Live update streamed over /live (Server-Sent Events). */
export type LiveMsg =
  | { t: 'log'; d: LogLine & { sessionKey: string } }
  | { t: 'error'; d: { issueId: number; title: string; culprit: string; side: string; ts: number; userId: number | null; sessionKey: string; count: number } }
  | { t: 'event'; d: { ts: number; name: string; userId: number | null; value: number | null; sessionKey: string } }
  | { t: 'session_start'; d: { sessionKey: string; userId: number; ts: number; isNew: boolean } }
  | { t: 'session_end'; d: { sessionKey: string; userId: number; ts: number } }
  | { t: 'detection'; d: { kind: string; userId: number | null; severity: string; ts: number; action?: string } }
