// Whiteboard model helpers, telemetry snapshots, and "Send to whiteboard".
import { reactive } from 'vue'
import type { BoardItem, Timeseries, TraceSpan } from './api'
import { spanMs } from './format'

// ---- Item data ----------------------------------------------------------------

export type Swatch = 'yellow' | 'green' | 'blue' | 'pink' | 'purple' | 'gray'
export const swatches: Swatch[] = ['yellow', 'green', 'blue', 'pink', 'purple', 'gray']

/** Theme token behind each swatch, so every color follows the active theme. */
export const swatchVar: Record<Swatch, string> = {
  yellow: 'var(--warning)',
  green: 'var(--success)',
  blue: 'var(--info)',
  pink: 'var(--danger)',
  purple: 'var(--accent)',
  gray: 'var(--muted)',
}
export const swatchOf = (c: unknown): Swatch => (swatches.includes(c as Swatch) ? (c as Swatch) : 'yellow')
export const tint = (c: unknown, pct: number) => `color-mix(in oklab, ${swatchVar[swatchOf(c)]} ${pct}%, var(--surface))`

/** One end of a connector: attached to an item, or a free point. */
export interface End {
  id?: string
  x?: number
  y?: number
}

export interface ChartSnapshot {
  type: 'area' | 'line' | 'bar'
  stacked?: boolean
  daily?: boolean
  data: Timeseries
  series: { key: string; label: string; color?: string; right?: boolean; unit?: string; text?: string[] }[]
}

/** A note pinned to a point on a chart (board charts store these in the item). */
export interface ChartNote {
  id: number | string
  ts: number
  value: number | null
  series: string
  text: string
  author?: { name: string } | null
  createdAt?: number
}

export type EmbedType = 'chart' | 'issue' | 'session' | 'log' | 'player' | 'server' | 'release' | 'detection' | 'span' | 'trace' | 'stat'

/** A snapshot of something from the dashboard, frozen at capture time. */
export interface EmbedData {
  type: EmbedType
  title: string
  subtitle?: string
  /** Where it came from in the dashboard (an in-app path). */
  link?: string
  capturedAt: number
  tone?: 'danger' | 'warning' | 'success' | 'info' | 'muted'
  fields?: [string, string][]
  code?: string
  chart?: ChartSnapshot
  notes?: ChartNote[]
  /** For trace spans: position and width within the whole trace, 0..1. */
  bar?: { offset: number; width: number; status?: string }
}

export const embedSize: Record<EmbedType, [number, number]> = {
  chart: [520, 320],
  issue: [380, 220],
  session: [360, 220],
  log: [420, 150],
  player: [340, 200],
  server: [360, 220],
  release: [340, 190],
  detection: [340, 190],
  span: [240, 104],
  trace: [360, 200],
  stat: [240, 120],
}

let seq = 0
export function uid(): string {
  const r = crypto.getRandomValues(new Uint32Array(2))
  return (Date.now().toString(36) + (seq++).toString(36) + r[0]!.toString(36) + r[1]!.toString(36)).slice(0, 24)
}

export function embedItem(data: EmbedData, at: { x: number; y: number } = { x: 0, y: 0 }): BoardItem {
  const [w, h] = embedSize[data.type] ?? [360, 200]
  return { id: uid(), kind: 'embed', x: at.x, y: at.y, w, h, z: 0, data: data as unknown as Record<string, any> }
}

// ---- Snapshots ----------------------------------------------------------------

export interface SnapSeries {
  key: string
  label?: string
  color?: string
  right?: boolean
  map?: (v: number) => number
  format?: (v: number) => string
}

/** Freezes a chart's data and series so the board can redraw it later. */
export function chartSnapshot(
  data: Timeseries,
  series: SnapSeries[],
  opts: { type?: 'area' | 'line' | 'bar'; stacked?: boolean; daily?: boolean; format?: (v: number) => string } = {},
): ChartSnapshot {
  const keep = new Set(series.map((s) => s.key))
  const values = (s: SnapSeries) => {
    const v = data.series.find((x) => x.name === s.key)?.values ?? []
    return s.map ? v.map(s.map) : v
  }
  return {
    type: opts.type ?? 'area',
    stacked: opts.stacked,
    daily: opts.daily,
    data: {
      bucket: data.bucket,
      points: [...data.points],
      series: series.map((s) => ({ name: s.key, values: values(s) })).filter((s) => keep.has(s.name)),
    },
    series: series.map((s) => {
      const f = s.format ?? opts.format
      const unitLabel = (f as { unitLabel?: string } | undefined)?.unitLabel
      return {
        key: s.key,
        label: s.label ?? s.key,
        color: s.color,
        right: s.right,
        unit: unitLabel,
        // A custom formatter can't be stored; keep its output for tooltips.
        text: f && !unitLabel ? values(s).map((v) => f(v)) : undefined,
      }
    }),
  }
}

/** The current in-app location, used as the "open original" link. */
export function here(): string {
  return location.pathname + location.search
}

// ---- Traces as node graphs ----------------------------------------------------

const NODE_W = 240
const NODE_H = 104
const GAP_X = 90
const GAP_Y = 26

/**
 * Lays a trace out as a left-to-right tree: one card per span, connectors from
 * parent to child labeled with the child's duration, inside a titled frame.
 */
export function traceItems(traceId: string, spans: TraceSpan[], link: string): BoardItem[] {
  const byId = new Map(spans.map((s) => [s.spanId, s]))
  const children = new Map<string, TraceSpan[]>()
  const roots: TraceSpan[] = []
  for (const s of spans) {
    if (s.parent && byId.has(s.parent) && s.parent !== s.spanId) {
      const list = children.get(s.parent) ?? []
      list.push(s)
      children.set(s.parent, list)
    } else roots.push(s)
  }
  for (const list of children.values()) list.sort((a, b) => a.start - b.start)
  roots.sort((a, b) => a.start - b.start)

  const t0 = Math.min(...spans.map((s) => s.start))
  const t1 = Math.max(...spans.map((s) => s.start + s.durationMs))
  const total = Math.max(t1 - t0, 0.001)
  const pad = 40
  const top = 64

  const items: BoardItem[] = []
  const nodeId = new Map<string, string>()
  let row = 0
  let maxDepth = 0
  const seen = new Set<string>()
  function place(s: TraceSpan, depth: number, parent?: TraceSpan): number {
    if (seen.has(s.spanId) || items.length > 600) return row * (NODE_H + GAP_Y)
    seen.add(s.spanId)
    maxDepth = Math.max(maxDepth, depth)
    const kids = children.get(s.spanId) ?? []
    const id = uid()
    nodeId.set(s.spanId, id)
    let y: number
    if (kids.length === 0) {
      y = top + row * (NODE_H + GAP_Y)
      row++
    } else {
      const ys = kids.map((k) => place(k, depth + 1, s))
      y = (ys[0]! + ys[ys.length - 1]!) / 2
    }
    const fields: [string, string][] = [
      ['Duration', spanMs(s.durationMs)],
      ['Starts', `+${spanMs(Math.max(0, s.start - (parent?.start ?? t0)))}`],
    ]
    items.push({
      id,
      kind: 'embed',
      x: pad + depth * (NODE_W + GAP_X),
      y,
      w: NODE_W,
      h: NODE_H,
      z: 2,
      data: {
        type: 'span',
        title: s.name,
        subtitle: s.status === 'ok' ? undefined : s.status,
        tone: s.status === 'error' ? 'danger' : undefined,
        link,
        capturedAt: Date.now(),
        fields,
        bar: { offset: (s.start - t0) / total, width: s.durationMs / total, status: s.status },
      } satisfies EmbedData,
    })
    return y
  }
  for (const r of roots) place(r, 0)

  for (const s of spans) {
    const from = s.parent ? nodeId.get(s.parent) : undefined
    const to = nodeId.get(s.spanId)
    if (from && to) {
      items.push({
        id: uid(),
        kind: 'connector',
        x: 0,
        y: 0,
        w: 0,
        h: 0,
        z: 1,
        data: { from: { id: from }, to: { id: to }, label: spanMs(s.durationMs), style: 'arrow', color: s.status === 'error' ? 'pink' : 'gray' },
      })
    }
  }

  const root = roots[0]
  items.push({
    id: uid(),
    kind: 'frame',
    x: 0,
    y: 0,
    w: pad * 2 + (maxDepth + 1) * NODE_W + maxDepth * GAP_X,
    h: top + row * (NODE_H + GAP_Y) + pad - GAP_Y,
    z: 0,
    data: { title: `Trace ${root?.name ?? traceId} · ${spanMs(total)} · ${spans.length} spans`, color: 'gray', link },
  })
  return items
}

// ---- Send to whiteboard -------------------------------------------------------

export type SendPayload = EmbedData | BoardItem[]

/** State for the board picker dialog (mounted once in the app). */
export const sendState = reactive({
  open: false,
  pid: 0,
  items: [] as BoardItem[],
  label: '',
})

/** Opens the board picker for a snapshot (or a prepared group of items). */
export function sendToWhiteboard(pid: number, payload: SendPayload) {
  const items = Array.isArray(payload) ? payload : [embedItem(payload)]
  sendState.pid = pid
  sendState.items = items
  sendState.label = Array.isArray(payload) ? `${items.filter((i) => i.kind !== 'connector').length} items` : payload.title
  sendState.open = true
}
