import type { BoardItem } from '@/lib/api'
import type { End } from '@/lib/whiteboard'

export interface Rect {
  x: number
  y: number
  w: number
  h: number
}
export interface Pt {
  x: number
  y: number
}

export const center = (r: Rect): Pt => ({ x: r.x + r.w / 2, y: r.y + r.h / 2 })

export function contains(outer: Rect, inner: Rect) {
  return inner.x >= outer.x && inner.y >= outer.y && inner.x + inner.w <= outer.x + outer.w && inner.y + inner.h <= outer.y + outer.h
}
export function intersects(a: Rect, b: Rect) {
  return a.x < b.x + b.w && a.x + a.w > b.x && a.y < b.y + b.h && a.y + a.h > b.y
}
export function normRect(a: Pt, b: Pt): Rect {
  return { x: Math.min(a.x, b.x), y: Math.min(a.y, b.y), w: Math.abs(a.x - b.x), h: Math.abs(a.y - b.y) }
}

/** Where the line from an item's center toward `toward` leaves its outline. */
function edgePoint(it: BoardItem, toward: Pt, gap: number): Pt {
  const c = center(it)
  const dx = toward.x - c.x
  const dy = toward.y - c.y
  if (dx === 0 && dy === 0) return c
  const hw = Math.max(it.w / 2, 1)
  const hh = Math.max(it.h / 2, 1)
  const shape = it.kind === 'shape' ? it.data.shape : 'rect'
  let s: number
  if (shape === 'ellipse') s = 1 / Math.hypot(dx / hw, dy / hh)
  else if (shape === 'diamond') s = 1 / (Math.abs(dx) / hw + Math.abs(dy) / hh)
  else s = Math.min(hw / Math.abs(dx || 1e-9), hh / Math.abs(dy || 1e-9))
  const len = Math.hypot(dx, dy)
  const t = Math.max(0, s - gap / len)
  return { x: c.x + dx * t, y: c.y + dy * t }
}

/** Resolves a connector's two ends to points, clipped to the items they attach to. */
export function connectorPoints(c: BoardItem, items: Map<string, BoardItem>): [Pt, Pt] | null {
  const from = c.data.from as End | undefined
  const to = c.data.to as End | undefined
  if (!from || !to) return null
  const a = from.id ? items.get(from.id) : undefined
  const b = to.id ? items.get(to.id) : undefined
  if ((from.id && !a) || (to.id && !b)) return null
  const pa: Pt = a ? center(a) : { x: from.x ?? 0, y: from.y ?? 0 }
  const pb: Pt = b ? center(b) : { x: to.x ?? 0, y: to.y ?? 0 }
  return [a ? edgePoint(a, pb, 6) : pa, b ? edgePoint(b, pa, 6) : pb]
}

/** Points of an arrowhead whose tip is at `tip`, pointing away from `from`. */
export function arrowHead(from: Pt, tip: Pt, size = 11): string {
  const ang = Math.atan2(tip.y - from.y, tip.x - from.x)
  const p = (a: number) => `${tip.x - size * Math.cos(ang + a)},${tip.y - size * Math.sin(ang + a)}`
  return `${tip.x},${tip.y} ${p(0.42)} ${p(-0.42)}`
}

export function bounds(list: Rect[]): Rect | null {
  if (!list.length) return null
  let x1 = Infinity
  let y1 = Infinity
  let x2 = -Infinity
  let y2 = -Infinity
  for (const r of list) {
    x1 = Math.min(x1, r.x)
    y1 = Math.min(y1, r.y)
    x2 = Math.max(x2, r.x + r.w)
    y2 = Math.max(y2, r.y + r.h)
  }
  return { x: x1, y: y1, w: x2 - x1, h: y2 - y1 }
}

/** A connector's bounding box (for marquee selection and fitting the view). */
export function connectorRect(c: BoardItem, items: Map<string, BoardItem>): Rect | null {
  const pts = connectorPoints(c, items)
  return pts ? normRect(pts[0], pts[1]) : null
}
