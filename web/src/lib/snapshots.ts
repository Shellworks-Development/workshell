// Snapshot builders for "Send to whiteboard": each freezes what's on screen
// into an EmbedData card with a link back to where it came from.
import type { Detection, ErrorEvent, GameServer, Issue, LogLine, Release, Session, TimelineItem } from './api'
import { dateTime, duration, num, pct, robux, shortId, time } from './format'
import { here, type EmbedData } from './whiteboard'

const now = () => Date.now()
const clip = (s: string, n: number) => (s.length > n ? `${s.slice(0, n)}…` : s)

export function issueSnap(issue: Issue, ev: ErrorEvent | null, pid: number): EmbedData {
  const frames = ev?.frames?.slice(0, 6).map((f) => `${f.function ? `${f.function}  ` : ''}${f.file ?? f.script}:${f.line}`)
  return {
    type: 'issue',
    title: issue.title,
    subtitle: `#${issue.id} · ${issue.status}${issue.side ? ` · ${issue.side}` : ''}`,
    link: `/p/${pid}/issues/${issue.id}`,
    capturedAt: now(),
    tone: issue.status === 'resolved' ? 'success' : issue.status === 'ignored' ? 'muted' : 'danger',
    fields: [
      ['Where', issue.culprit || '—'],
      ['Events', num(issue.eventCount)],
      ['Players', num(issue.userCount)],
      ['First seen', `${dateTime(issue.firstSeen)}${issue.firstRelease ? ` · ${issue.firstRelease}` : ''}`],
      ['Last seen', `${dateTime(issue.lastSeen)}${issue.lastRelease ? ` · ${issue.lastRelease}` : ''}`],
      ...(issue.assignee ? ([['Assignee', issue.assignee.name]] as [string, string][]) : []),
    ],
    code: frames?.length ? frames.join('\n') : ev?.stack ? clip(ev.stack, 600) : undefined,
  }
}

function timelineLines(items: TimelineItem[], n = 8) {
  return items
    .slice(-n)
    .map((t) => `${time(t.ts)}  ${t.kind.padEnd(9)} ${clip(t.title, 80)}`)
    .join('\n')
}

export function sessionSnap(s: Session, timeline: TimelineItem[], pid: number): EmbedData {
  return {
    type: 'session',
    title: `Player ${s.userId} · ${duration((s.endedAt ?? now()) - s.startedAt)}`,
    subtitle: s.endedAt ? `session ${shortId(s.key)}` : `live session ${shortId(s.key)}`,
    link: `/p/${pid}/sessions/${encodeURIComponent(s.key)}`,
    capturedAt: now(),
    tone: s.errorCount ? 'danger' : undefined,
    fields: [
      ['Started', dateTime(s.startedAt)],
      ['Platform', [s.platform, s.country].filter(Boolean).join(' · ') || '—'],
      ['Release', s.release || '—'],
      ['Server', shortId(s.jobId)],
      ['Errors', num(s.errorCount)],
    ],
    code: timeline.length ? timelineLines(timeline) : undefined,
  }
}

export function logSnap(l: LogLine, pid: number): EmbedData {
  const attrs = l.attrs && Object.keys(l.attrs).length ? `\n${clip(JSON.stringify(l.attrs, null, 2), 800)}` : ''
  return {
    type: 'log',
    title: clip(l.message.split('\n')[0] ?? '', 120),
    subtitle: `${l.level.toUpperCase()} · ${dateTime(l.ts)}`,
    link: `/p/${pid}/logs?jobId=${encodeURIComponent(l.jobId)}`,
    capturedAt: now(),
    tone: l.level === 'error' ? 'danger' : l.level === 'warn' ? 'warning' : undefined,
    fields: [
      ['Server', shortId(l.jobId)],
      ...(l.userId ? ([['Player', String(l.userId)]] as [string, string][]) : []),
    ],
    code: clip(l.message, 1200) + attrs,
  }
}

export function detectionSnap(d: Detection, pid: number): EmbedData {
  return {
    type: 'detection',
    title: `${d.kind.replace(/_/g, ' ')}${d.userId ? ` · player ${d.userId}` : ''}`,
    subtitle: `${d.severity} severity · ${dateTime(d.ts)}`,
    link: d.userId ? `/p/${pid}/players/${d.userId}` : `/p/${pid}/security`,
    capturedAt: now(),
    tone: d.severity === 'high' ? 'danger' : d.severity === 'medium' ? 'warning' : 'muted',
    fields: [
      ['Action', d.action || 'none'],
      ['Source', d.source],
      ['Server', shortId(d.jobId) || '—'],
    ],
    code: Object.keys(d.details ?? {}).length ? clip(JSON.stringify(d.details, null, 2), 800) : undefined,
  }
}

export function playerSnap(
  p: { userId: number; firstSeen: number; lastSeen: number; sessionCount: number; playtimeMs: number; spent: number; detections: number; online: boolean },
  timeline: TimelineItem[],
  pid: number,
): EmbedData {
  return {
    type: 'player',
    title: `Player ${p.userId}`,
    subtitle: p.online ? 'online now' : `last seen ${dateTime(p.lastSeen)}`,
    link: `/p/${pid}/players/${p.userId}`,
    capturedAt: now(),
    tone: p.detections ? 'warning' : undefined,
    fields: [
      ['First seen', dateTime(p.firstSeen)],
      ['Sessions', num(p.sessionCount)],
      ['Playtime', duration(p.playtimeMs)],
      ['Spent', robux(p.spent)],
      ['Detections', num(p.detections)],
    ],
    code: timeline.length ? timelineLines(timeline, 6) : undefined,
  }
}

export function serverSnap(s: GameServer, extra: { errorCount: number; sessionCount: number }, pid: number): EmbedData {
  return {
    type: 'server',
    title: `Server ${shortId(s.jobId)}`,
    subtitle: s.live ? 'live' : `closed ${s.closedAt ? dateTime(s.closedAt) : ''}`,
    link: `/p/${pid}/servers/${encodeURIComponent(s.jobId)}`,
    capturedAt: now(),
    tone: extra.errorCount ? 'warning' : undefined,
    fields: [
      ['Players', `${num(s.players)} / ${num(s.maxPlayers)}`],
      ['Frame rate', `${num(s.fps)} fps`],
      ['Heartbeat', `${num(s.heartbeatMs)} ms`],
      ['Memory', `${num(s.memoryMb)} MB`],
      ['Release', s.release || s.placeVersion || '—'],
      ['Errors', num(extra.errorCount)],
      ['Sessions', num(extra.sessionCount)],
    ],
  }
}

export function releaseSnap(r: Release, pid: number): EmbedData {
  return {
    type: 'release',
    title: `Release ${r.version}`,
    subtitle: r.message ? clip(r.message, 80) : r.commitSha ? r.commitSha.slice(0, 7) : undefined,
    link: `/p/${pid}/releases/${encodeURIComponent(r.version)}`,
    capturedAt: now(),
    tone: r.errorFreeRate >= 0 && r.errorFreeRate < 0.95 ? 'danger' : undefined,
    fields: [
      ['First seen', r.firstSeen ? dateTime(r.firstSeen) : '—'],
      ['Commit', r.commitSha ? `${r.commitSha.slice(0, 7)}${r.author ? ` · ${r.author}` : ''}` : '—'],
      ['Sessions', num(r.sessions)],
      ['Error-free', r.errorFreeRate < 0 ? '—' : pct(r.errorFreeRate)],
      ['New issues', num(r.newIssues)],
    ],
  }
}

/** A single number (stat card) as a small card. */
export function statSnap(label: string, value: string, sub?: string): EmbedData {
  return { type: 'stat', title: label, subtitle: sub, link: here(), capturedAt: now(), fields: [['Value', value]] }
}
