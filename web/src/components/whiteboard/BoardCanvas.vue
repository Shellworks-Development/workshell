<script setup lang="ts">
import {
  ArrowUpRight,
  BringToFront,
  Circle,
  Copy,
  Diamond,
  ExternalLink,
  Frame,
  Hand,
  Maximize,
  Minus,
  MousePointer2,
  MoveRight,
  Pencil,
  Plus,
  Redo2,
  SendToBack,
  Square,
  StickyNote,
  Trash2,
  Type,
  Undo2,
} from 'lucide-vue-next'
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import type { BoardItem } from '@/lib/api'
import { clone, type BoardStore } from '@/lib/board'
import { hue } from '@/lib/format'
import { dark } from '@/lib/state'
import { swatchOf, swatches, swatchVar, tint, uid, type End, type Swatch } from '@/lib/whiteboard'
import EmbedCard from './EmbedCard.vue'
import { arrowHead, bounds, connectorPoints, connectorRect, contains, intersects, normRect, type Pt, type Rect } from './geometry'

const props = defineProps<{ store: BoardStore; boardId: number; focus?: string }>()
const store = props.store

type Tool = 'select' | 'hand' | 'sticky' | 'text' | 'shape' | 'arrow' | 'frame'
const tools: { id: Tool; label: string; key: string; icon: unknown }[] = [
  { id: 'select', label: 'Select', key: 'V', icon: MousePointer2 },
  { id: 'hand', label: 'Pan', key: 'H', icon: Hand },
  { id: 'sticky', label: 'Sticky note', key: 'S', icon: StickyNote },
  { id: 'text', label: 'Text', key: 'T', icon: Type },
  { id: 'shape', label: 'Shape', key: 'R', icon: Square },
  { id: 'arrow', label: 'Arrow', key: 'A', icon: MoveRight },
  { id: 'frame', label: 'Frame', key: 'F', icon: Frame },
]
const tool = ref<Tool>('select')

// ---- View ---------------------------------------------------------------------------
const viewport = ref<HTMLElement | null>(null)
const view = reactive({ x: 0, y: 0, zoom: 1 })
const MIN_ZOOM = 0.1
const MAX_ZOOM = 3
const clampZoom = (z: number) => Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, z))

function rect() {
  return viewport.value?.getBoundingClientRect() ?? new DOMRect(0, 0, 1, 1)
}
function toWorld(clientX: number, clientY: number): Pt {
  const r = rect()
  return { x: (clientX - r.left - view.x) / view.zoom, y: (clientY - r.top - view.y) / view.zoom }
}
function zoomAt(screen: Pt, zoom: number) {
  const z = clampZoom(zoom)
  const wx = (screen.x - view.x) / view.zoom
  const wy = (screen.y - view.y) / view.zoom
  view.zoom = z
  view.x = screen.x - wx * z
  view.y = screen.y - wy * z
}
function zoomBy(f: number) {
  const r = rect()
  zoomAt({ x: r.width / 2, y: r.height / 2 }, view.zoom * f)
}
function itemRect(it: BoardItem): Rect | null {
  return it.kind === 'connector' ? connectorRect(it, store.items) : it
}
function fit(ids?: string[]) {
  const list = (ids ? ids.map((id) => store.items.get(id)).filter(Boolean) : [...store.items.values()]) as BoardItem[]
  const b = bounds(list.map(itemRect).filter(Boolean) as Rect[])
  const r = rect()
  if (!b) {
    view.zoom = 1
    view.x = r.width / 2
    view.y = r.height / 2
    return
  }
  const pad = 64
  const z = clampZoom(Math.min(1, (r.width - pad * 2) / Math.max(b.w, 1), (r.height - pad * 2) / Math.max(b.h, 1)))
  view.zoom = z
  view.x = r.width / 2 - (b.x + b.w / 2) * z
  view.y = r.height / 2 - (b.y + b.h / 2) * z
}

// Remember where each viewer was on each board (a per-browser convenience).
const viewKey = computed(() => `workshell-wb-view-${props.boardId}`)
function saveView() {
  try {
    localStorage.setItem(viewKey.value, JSON.stringify(view))
  } catch {
    // storage unavailable
  }
}
let viewTimer: ReturnType<typeof setTimeout> | null = null
watch(view, () => {
  if (viewTimer) clearTimeout(viewTimer)
  viewTimer = setTimeout(saveView, 400)
})

const gridStyle = computed(() => {
  let size = 24 * view.zoom
  while (size < 12) size *= 2
  return {
    backgroundImage: 'radial-gradient(circle, var(--border-strong) 1px, transparent 1.2px)',
    backgroundSize: `${size}px ${size}px`,
    backgroundPosition: `${view.x}px ${view.y}px`,
  }
})

// ---- Selection -------------------------------------------------------------------
const selection = ref<string[]>([])
const selected = computed(() => new Set(selection.value))
function select(ids: string[]) {
  selection.value = [...new Set(ids)].filter((id) => store.items.has(id))
}
watch(selection, (s) => {
  store.selected.value = s
  store.sendPresence(true)
})
// Drop selection of items deleted elsewhere.
watch(
  () => store.items.size,
  () => {
    if (selection.value.some((id) => !store.items.has(id))) select(selection.value)
  },
)
const single = computed(() => (selection.value.length === 1 ? store.items.get(selection.value[0]!) ?? null : null))

const editing = ref<string | null>(null)
const editText = ref('')
const pickingId = ref<string | null>(null)
const hover = ref<string | null>(null)

// ---- Derived render lists ---------------------------------------------------------
const frames = computed(() => store.sorted.value.filter((i) => i.kind === 'frame'))
const others = computed(() => store.sorted.value.filter((i) => i.kind !== 'frame' && i.kind !== 'connector'))
const connectors = computed(() =>
  store.sorted.value
    .filter((i) => i.kind === 'connector')
    .map((c) => {
      const pts = connectorPoints(c, store.items)
      return pts ? { c, a: pts[0], b: pts[1], mid: { x: (pts[0].x + pts[1].x) / 2, y: (pts[0].y + pts[1].y) / 2 } } : null
    })
    .filter((x): x is NonNullable<typeof x> => !!x),
)
const peerList = computed(() => [...store.peers.values()])
const peerSelected = computed(() => {
  const m = new Map<string, string>()
  for (const p of peerList.value) for (const id of p.selected) m.set(id, `hsl(${hue(String(p.user.id))} 70% 55%)`)
  return m
})
const peerColor = (id: number) => `hsl(${hue(String(id))} 70% 55%)`

// Stickies look like paper: a light pastel with dark ink, even on dark themes
// (a dark yellow just turns brown).
function stickyStyle(c: unknown) {
  const v = swatchVar[swatchOf(c)]
  return dark.value
    ? { background: `color-mix(in oklch, ${v} 78%, white)`, color: '#1d1d20', border: `1px solid color-mix(in oklch, ${v} 90%, black)` }
    : { background: `color-mix(in oklch, ${v} 30%, white)`, color: 'var(--fg)', border: `1px solid color-mix(in oklch, ${v} 45%, white)` }
}

// ---- Item factories ---------------------------------------------------------------
const textSizes = { sm: 14, md: 18, lg: 28, xl: 44 } as const
type TextSize = keyof typeof textSizes

function newItem(kind: BoardItem['kind'], at: Pt, data: Record<string, unknown>, w: number, h: number): BoardItem {
  return { id: uid(), kind, x: Math.round(at.x), y: Math.round(at.y), w, h, z: kind === 'frame' ? store.minZ() - 1 : store.maxZ() + 1, data }
}

// ---- Pointer interaction ---------------------------------------------------------
type Drag =
  | { type: 'pan'; sx: number; sy: number; vx: number; vy: number; tapItem?: string }
  | { type: 'move'; start: Pt; origins: Map<string, Pt>; ends: Map<string, { from?: Pt; to?: Pt }> }
  | { type: 'resize'; id: string; handle: string; orig: Rect; start: Pt }
  | { type: 'marquee'; start: Pt; additive: string[] }
  | { type: 'create'; id: string; start: Pt; kind: 'shape' | 'frame' }
  | { type: 'connect'; id: string; end: 'from' | 'to'; source?: string }
  | { type: 'pinch'; dist: number; zoom: number; mid: Pt; vx: number; vy: number }

let drag: Drag | null = null
const marquee = ref<Rect | null>(null)
const pointers = new Map<number, Pt>()
let down: { x: number; y: number; t: number; moved: boolean; pointerType: string } | null = null
let lastTap: { t: number; x: number; y: number; target: string | null } | null = null
const spaceDown = ref(false)
const panning = ref(false)

function itemAt(clientX: number, clientY: number, exclude?: string): string | null {
  for (const el of document.elementsFromPoint(clientX, clientY)) {
    const id = (el as HTMLElement).closest?.('[data-item]')?.getAttribute('data-item')
    if (id && id !== exclude && store.items.get(id)?.kind !== 'connector') return id
  }
  return null
}

function startPinch() {
  const [p1, p2] = [...pointers.values()] as [Pt, Pt]
  const r = rect()
  finishDrag()
  drag = { type: 'pinch', dist: Math.hypot(p1.x - p2.x, p1.y - p2.y) || 1, zoom: view.zoom, mid: { x: (p1.x + p2.x) / 2 - r.left, y: (p1.y + p2.y) / 2 - r.top }, vx: view.x, vy: view.y }
}

function startMove(start: Pt) {
  const ids = new Set(selection.value)
  // Moving a frame carries everything inside it.
  for (const id of selection.value) {
    const f = store.items.get(id)
    if (f?.kind !== 'frame') continue
    for (const it of store.items.values()) if (it.kind !== 'connector' && it.id !== f.id && contains(f, it)) ids.add(it.id)
  }
  const origins = new Map<string, Pt>()
  const ends = new Map<string, { from?: Pt; to?: Pt }>()
  store.begin()
  for (const id of ids) {
    const it = store.items.get(id)
    if (!it) continue
    store.touch(id)
    store.locked.add(id)
    if (it.kind === 'connector') {
      const from = it.data.from as End
      const to = it.data.to as End
      ends.set(id, { from: from.id ? undefined : { x: from.x ?? 0, y: from.y ?? 0 }, to: to.id ? undefined : { x: to.x ?? 0, y: to.y ?? 0 } })
    } else origins.set(id, { x: it.x, y: it.y })
  }
  drag = { type: 'move', start, origins, ends }
}

function onPointerDown(e: PointerEvent) {
  const t = e.target as HTMLElement
  if (t.closest('[data-ui], [data-editing], textarea, input')) return
  if (e.button === 2) return
  const itemEl = t.closest<HTMLElement>('[data-item]')
  const id = itemEl?.getAttribute('data-item') ?? null
  // Interactive content of a selected card (chart tooltips, links) keeps the pointer.
  if (id && selected.value.has(id) && t.closest('[data-interactive]') && tool.value === 'select') return
  if (id && pickingId.value === id) return

  // The canvas owns this press: no text selection, and no mousedown focus
  // change (which would blur an editor we're about to open).
  e.preventDefault()
  pointers.set(e.pointerId, { x: e.clientX, y: e.clientY })
  viewport.value?.setPointerCapture(e.pointerId)
  if (pointers.size === 2) return startPinch()
  if (pointers.size > 2) return
  down = { x: e.clientX, y: e.clientY, t: performance.now(), moved: false, pointerType: e.pointerType }
  if (editing.value) commitEdit()
  pickingId.value = null

  const w = toWorld(e.clientX, e.clientY)
  const touch = e.pointerType === 'touch'

  if (e.button === 1 || tool.value === 'hand' || spaceDown.value) {
    drag = { type: 'pan', sx: e.clientX, sy: e.clientY, vx: view.x, vy: view.y }
    panning.value = true
    return
  }

  // Creation tools.
  if (tool.value === 'sticky' || tool.value === 'text') {
    const it =
      tool.value === 'sticky'
        ? newItem('sticky', { x: w.x - 100, y: w.y - 100 }, { text: '', color: lastSwatch.value }, 200, 200)
        : newItem('text', { x: w.x, y: w.y - 16 }, { text: '', size: 'md' }, 260, 32)
    store.put(it)
    select([it.id])
    tool.value = 'select'
    startEdit(it.id)
    drag = null
    return
  }
  if (tool.value === 'shape' || tool.value === 'frame') {
    const it =
      tool.value === 'shape'
        ? newItem('shape', w, { shape: lastShape.value, text: '', color: lastSwatch.value }, 0, 0)
        : newItem('frame', w, { title: 'Frame', color: 'gray' }, 0, 0)
    store.begin()
    store.put(it)
    store.locked.add(it.id)
    select([it.id])
    drag = { type: 'create', id: it.id, start: w, kind: tool.value }
    return
  }
  if (tool.value === 'arrow' || t.closest('[data-dot]')) {
    const source = t.closest('[data-dot]') ? id ?? undefined : itemAt(e.clientX, e.clientY) ?? undefined
    const from: End = source ? { id: source } : { x: w.x, y: w.y }
    const c = newItem('connector', { x: 0, y: 0 }, { from, to: { x: w.x, y: w.y }, style: 'arrow', color: 'gray' }, 0, 0)
    store.begin()
    store.put(c)
    store.locked.add(c.id)
    select([c.id])
    drag = { type: 'connect', id: c.id, end: 'to', source }
    return
  }

  // Select tool.
  const handle = t.closest<HTMLElement>('[data-handle]')
  if (handle && id) {
    const it = store.items.get(id)!
    store.begin()
    store.touch(id)
    store.locked.add(id)
    drag = { type: 'resize', id, handle: handle.dataset.handle!, orig: { x: it.x, y: it.y, w: it.w, h: it.h }, start: w }
    return
  }
  const endEl = t.closest<HTMLElement>('[data-end]')
  if (endEl && id) {
    store.begin()
    store.touch(id)
    store.locked.add(id)
    drag = { type: 'connect', id, end: endEl.dataset.end as 'from' | 'to' }
    return
  }
  if (id) {
    // On touch, dragging an unselected item pans the board (a tap selects it),
    // so scrolling around a busy board doesn't shove things around.
    if (touch && !selected.value.has(id)) {
      drag = { type: 'pan', sx: e.clientX, sy: e.clientY, vx: view.x, vy: view.y, tapItem: id }
      return
    }
    if (e.shiftKey || e.metaKey || e.ctrlKey) {
      select(selected.value.has(id) ? selection.value.filter((s) => s !== id) : [...selection.value, id])
      if (!selected.value.has(id)) return
    } else if (!selected.value.has(id)) select([id])
    startMove(w)
    return
  }
  // Empty canvas.
  if (touch) {
    drag = { type: 'pan', sx: e.clientX, sy: e.clientY, vx: view.x, vy: view.y }
    return
  }
  drag = { type: 'marquee', start: w, additive: e.shiftKey ? [...selection.value] : [] }
  if (!e.shiftKey) select([])
}

function onPointerMove(e: PointerEvent) {
  if (e.pointerType === 'mouse' || e.pointerType === 'pen') {
    store.cursor.value = (() => {
      const w = toWorld(e.clientX, e.clientY)
      return [Math.round(w.x), Math.round(w.y)]
    })()
    store.sendPresence()
  }
  if (!pointers.has(e.pointerId)) return
  pointers.set(e.pointerId, { x: e.clientX, y: e.clientY })
  if (down && Math.hypot(e.clientX - down.x, e.clientY - down.y) > (down.pointerType === 'touch' ? 8 : 3)) down.moved = true
  if (!drag) return
  const w = toWorld(e.clientX, e.clientY)

  switch (drag.type) {
    case 'pinch': {
      if (pointers.size < 2) return
      const [p1, p2] = [...pointers.values()] as [Pt, Pt]
      const r = rect()
      const mid = { x: (p1.x + p2.x) / 2 - r.left, y: (p1.y + p2.y) / 2 - r.top }
      const z = clampZoom((drag.zoom * Math.hypot(p1.x - p2.x, p1.y - p2.y)) / drag.dist)
      const wx = (drag.mid.x - drag.vx) / drag.zoom
      const wy = (drag.mid.y - drag.vy) / drag.zoom
      view.zoom = z
      view.x = mid.x - wx * z
      view.y = mid.y - wy * z
      break
    }
    case 'pan':
      if (!down?.moved) return
      view.x = drag.vx + e.clientX - drag.sx
      view.y = drag.vy + e.clientY - drag.sy
      break
    case 'move': {
      if (!down?.moved) return
      let dx = w.x - drag.start.x
      let dy = w.y - drag.start.y
      if (e.shiftKey) Math.abs(dx) > Math.abs(dy) ? (dy = 0) : (dx = 0)
      for (const [id, o] of drag.origins) {
        const it = store.items.get(id)
        if (it) {
          it.x = Math.round(o.x + dx)
          it.y = Math.round(o.y + dy)
          store.markDirty(id)
        }
      }
      for (const [id, o] of drag.ends) {
        const it = store.items.get(id)
        if (!it) continue
        if (o.from) it.data.from = { x: o.from.x + dx, y: o.from.y + dy }
        if (o.to) it.data.to = { x: o.to.x + dx, y: o.to.y + dy }
        store.markDirty(id)
      }
      break
    }
    case 'resize': {
      const it = store.items.get(drag.id)
      if (!it) return
      const o = drag.orig
      const dx = w.x - drag.start.x
      const dy = w.y - drag.start.y
      const min = it.kind === 'embed' ? 120 : 24
      let { x, y, w: ww, h } = o
      if (drag.handle.includes('e')) ww = Math.max(min, o.w + dx)
      if (drag.handle.includes('s')) h = Math.max(min, o.h + dy)
      if (drag.handle.includes('w')) {
        ww = Math.max(min, o.w - dx)
        x = o.x + o.w - ww
      }
      if (drag.handle.includes('n')) {
        h = Math.max(min, o.h - dy)
        y = o.y + o.h - h
      }
      if (e.shiftKey && o.w > 0 && o.h > 0) {
        const ratio = o.w / o.h
        if (ww / h > ratio) ww = h * ratio
        else h = ww / ratio
        if (drag.handle.includes('w')) x = o.x + o.w - ww
        if (drag.handle.includes('n')) y = o.y + o.h - h
      }
      Object.assign(it, { x: Math.round(x), y: Math.round(y), w: Math.round(ww), h: Math.round(h) })
      store.markDirty(it.id)
      break
    }
    case 'create': {
      const it = store.items.get(drag.id)
      if (!it) return
      const r = normRect(drag.start, w)
      Object.assign(it, { x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.w), h: Math.round(r.h) })
      store.markDirty(it.id)
      break
    }
    case 'connect': {
      const it = store.items.get(drag.id)
      if (!it) return
      const target = itemAt(e.clientX, e.clientY, drag.end === 'to' ? (it.data.from as End).id : (it.data.to as End).id)
      hover.value = target
      it.data[drag.end] = { x: w.x, y: w.y }
      store.markDirty(it.id)
      break
    }
    case 'marquee': {
      const r = normRect(drag.start, w)
      marquee.value = r
      const hits = store.sorted.value.filter((it) => {
        const ir = itemRect(it)
        // Frames only when fully enclosed, so dragging inside one selects its contents.
        return ir && (it.kind === 'frame' ? contains(r, ir) : intersects(r, ir))
      })
      select([...drag.additive, ...hits.map((i) => i.id)])
      break
    }
  }
}

function finishDrag(e?: PointerEvent) {
  const d = drag
  drag = null
  panning.value = false
  marquee.value = null
  if (!d) return
  switch (d.type) {
    case 'move': {
      store.end()
      store.unlock([...d.origins.keys(), ...d.ends.keys()])
      break
    }
    case 'resize':
      store.end()
      store.unlock([d.id])
      break
    case 'create': {
      const it = store.items.get(d.id)
      if (it && (it.w < 8 || it.h < 8)) {
        const [w, h] = d.kind === 'frame' ? [480, 320] : [160, 110]
        Object.assign(it, { x: Math.round(d.start.x - w / 2), y: Math.round(d.start.y - h / 2), w, h })
      }
      store.end()
      store.unlock([d.id])
      tool.value = 'select'
      break
    }
    case 'connect': {
      const it = store.items.get(d.id)
      hover.value = null
      if (it && e) {
        const other = (it.data[d.end === 'to' ? 'from' : 'to'] as End).id
        const target = itemAt(e.clientX, e.clientY, other)
        if (target) it.data[d.end] = { id: target }
        const pts = connectorPoints(it, store.items)
        // A click with the arrow tool (no drag, no target) makes nothing.
        if (pts && Math.hypot(pts[0].x - pts[1].x, pts[0].y - pts[1].y) < 12 && !target) {
          store.remove([d.id])
          select([])
        }
      }
      store.end()
      store.unlock([d.id])
      tool.value = 'select'
      break
    }
  }
}

function onPointerUp(e: PointerEvent) {
  if (!pointers.has(e.pointerId)) return
  pointers.delete(e.pointerId)
  const d = drag
  const tap = down && !down.moved && pointers.size === 0
  if (d?.type === 'pinch') {
    if (pointers.size === 0) drag = null
    down = null
    return
  }
  finishDrag(e)
  if (tap && d) {
    // Taps: select on touch, clear on empty canvas, double-tap to edit or add text.
    const target = d.type === 'pan' ? (d.tapItem ?? null) : d.type === 'move' ? selection.value[0] ?? null : null
    if (d.type === 'pan' && d.tapItem) select([d.tapItem])
    else if (d.type === 'pan' && !d.tapItem && tool.value === 'select') select([])
    const now = performance.now()
    const id = (e.target as HTMLElement).closest?.('[data-item]')?.getAttribute('data-item') ?? target
    if (lastTap && now - lastTap.t < 350 && Math.hypot(e.clientX - lastTap.x, e.clientY - lastTap.y) < 24 && lastTap.target === id) {
      lastTap = null
      onDoubleTap(id, e)
    } else lastTap = { t: now, x: e.clientX, y: e.clientY, target: id }
  }
  down = null
}

function onDoubleTap(id: string | null, e: PointerEvent) {
  if (id) {
    select([id])
    startEdit(id)
  } else if (tool.value === 'select') {
    const w = toWorld(e.clientX, e.clientY)
    const it = newItem('text', { x: w.x, y: w.y - 16 }, { text: '', size: 'md' }, 260, 32)
    store.put(it)
    select([it.id])
    startEdit(it.id)
  }
}

function onWheel(e: WheelEvent) {
  if ((e.target as HTMLElement).closest('[data-ui]')) return
  e.preventDefault()
  const r = rect()
  if (e.ctrlKey || e.metaKey) {
    // Trackpad pinch arrives as ctrl+wheel.
    zoomAt({ x: e.clientX - r.left, y: e.clientY - r.top }, view.zoom * Math.exp(-e.deltaY * (e.deltaMode === 1 ? 0.05 : 0.0025)))
  } else {
    const k = e.deltaMode === 1 ? 16 : 1
    view.x -= (e.shiftKey ? e.deltaY : e.deltaX) * k
    view.y -= (e.shiftKey ? 0 : e.deltaY) * k
  }
}

// ---- Editing text ---------------------------------------------------------------
const editable = (it: BoardItem | null | undefined) => !!it && ['sticky', 'text', 'shape', 'frame', 'connector'].includes(it.kind)
const textField = (it: BoardItem) => (it.kind === 'frame' ? 'title' : it.kind === 'connector' ? 'label' : 'text')

function startEdit(id: string) {
  const it = store.items.get(id)
  if (!editable(it)) return
  editing.value = id
  editText.value = String(it!.data[textField(it!)] ?? '')
  store.locked.add(id)
  nextTick(() => {
    const el = viewport.value?.querySelector<HTMLTextAreaElement>('[data-editing] textarea')
    el?.focus({ preventScroll: true })
    el?.select()
  })
}
function commitEdit() {
  const id = editing.value
  if (!id) return
  editing.value = null
  const it = store.items.get(id)
  store.locked.delete(id)
  if (!it) return
  const text = editText.value.slice(0, 5000)
  // Empty text boxes disappear, like in every other canvas tool.
  if (it.kind === 'text' && !text.trim()) {
    store.remove([id])
    select([])
    return
  }
  const patch: Partial<BoardItem> = {}
  if (it.kind === 'text') {
    const el = viewport.value?.querySelector<HTMLElement>(`[data-item="${CSS.escape(id)}"]`)
    if (el) patch.h = Math.max(24, Math.round(el.offsetHeight))
  }
  if (text !== (it.data[textField(it)] ?? '') || patch.h !== undefined) store.update(id, patch, { [textField(it)]: text })
  store.unlock([id])
}
function onEditorKey(e: KeyboardEvent) {
  if (e.key === 'Escape' || (e.key === 'Enter' && (e.metaKey || e.ctrlKey))) {
    e.preventDefault()
    commitEdit()
  }
  e.stopPropagation()
}
function autoGrow(e: Event) {
  const el = e.target as HTMLTextAreaElement
  el.style.height = 'auto'
  el.style.height = `${el.scrollHeight}px`
}

// ---- Commands ---------------------------------------------------------------------
const lastSwatch = ref<Swatch>('yellow')
const lastShape = ref<'rect' | 'ellipse' | 'diamond'>('rect')

/** Deletes items along with connectors attached to them. */
function deleteSelection() {
  const ids = new Set(selection.value)
  for (const it of store.items.values()) {
    if (it.kind !== 'connector') continue
    const f = (it.data.from as End).id
    const t = (it.data.to as End).id
    if ((f && ids.has(f)) || (t && ids.has(t))) ids.add(it.id)
  }
  store.remove(ids)
  select([])
}

let clipboard: BoardItem[] = []
function copy() {
  clipboard = selection.value.map((id) => store.items.get(id)).filter(Boolean).map((i) => clone(i!)) as BoardItem[]
}
function paste(offset = 32) {
  if (!clipboard.length) return
  const ids = new Map(clipboard.map((i) => [i.id, uid()]))
  const top = store.maxZ()
  const out: string[] = []
  store.change(() => {
    for (const src of clipboard) {
      const it = clone(src)
      it.id = ids.get(src.id)!
      it.x += offset
      it.y += offset
      it.z = it.kind === 'frame' ? it.z : top + 1 + out.length
      delete it.rev
      if (it.kind === 'connector') {
        for (const end of ['from', 'to'] as const) {
          const e = it.data[end] as End
          if (e.id) e.id = ids.get(e.id) ?? (store.items.has(e.id) ? e.id : undefined)
          else it.data[end] = { x: (e.x ?? 0) + offset, y: (e.y ?? 0) + offset }
          if (!(it.data[end] as End).id && (it.data[end] as End).x === undefined) it.data[end] = { x: it.x, y: it.y }
        }
      }
      store.put(it)
      out.push(it.id)
    }
  })
  clipboard = out.map((id) => clone(store.items.get(id)!))
  select(out)
}
function duplicate() {
  copy()
  paste()
}
function setData(data: Record<string, unknown>, filter: (it: BoardItem) => boolean = () => true) {
  store.change(() => {
    for (const id of selection.value) {
      const it = store.items.get(id)
      if (it && filter(it)) store.update(id, {}, data)
    }
  })
}
function setColor(c: Swatch) {
  lastSwatch.value = c
  setData({ color: c }, (it) => it.kind !== 'embed')
}
function setShape(s: 'rect' | 'ellipse' | 'diamond') {
  lastShape.value = s
  setData({ shape: s }, (it) => it.kind === 'shape')
}
function reorder(front: boolean) {
  const list = selection.value.map((id) => store.items.get(id)!).filter(Boolean).sort((a, b) => a.z - b.z)
  store.change(() => {
    if (front) {
      const top = store.maxZ()
      list.forEach((it, i) => store.update(it.id, { z: top + 1 + i }))
    } else {
      const bottom = store.minZ()
      list.reverse().forEach((it, i) => store.update(it.id, { z: bottom - 1 - i }))
    }
  })
}
function nudge(dx: number, dy: number) {
  store.change(() => {
    for (const id of selection.value) {
      const it = store.items.get(id)
      if (it && it.kind !== 'connector') store.update(id, { x: it.x + dx, y: it.y + dy })
    }
  })
}

const selKinds = computed(() => new Set(selection.value.map((id) => store.items.get(id)?.kind)))
const selData = computed(() => single.value?.data ?? store.items.get(selection.value[0] ?? '')?.data ?? {})
const embedLink = computed(() => {
  const l = single.value?.kind === 'embed' || single.value?.kind === 'frame' ? single.value.data.link : null
  return typeof l === 'string' && l.startsWith('/') && !l.startsWith('//') ? l : null
})

// ---- Keyboard -----------------------------------------------------------------------
function onKey(e: KeyboardEvent) {
  const t = e.target as HTMLElement
  if (t.closest('input, textarea, select, [contenteditable="true"]') || document.querySelector('[aria-modal="true"]')) return
  const mod = e.metaKey || e.ctrlKey
  const k = e.key.toLowerCase()
  if (e.key === ' ' && !e.repeat) {
    spaceDown.value = true
    e.preventDefault()
    return
  }
  if (mod && k === 'z') {
    e.preventDefault()
    e.shiftKey ? store.redo() : store.undo()
  } else if (mod && k === 'y') {
    e.preventDefault()
    store.redo()
  } else if (mod && k === 'a') {
    e.preventDefault()
    select([...store.items.keys()])
  } else if (mod && k === 'd') {
    e.preventDefault()
    duplicate()
  } else if (mod && k === 'c') copy()
  else if (mod && k === 'x') {
    copy()
    deleteSelection()
  } else if (mod && k === 'v') paste()
  else if (mod && (k === '=' || k === '+')) {
    e.preventDefault()
    zoomBy(1.2)
  } else if (mod && k === '-') {
    e.preventDefault()
    zoomBy(1 / 1.2)
  } else if (mod) return
  else if (e.key === 'Delete' || e.key === 'Backspace') {
    e.preventDefault()
    deleteSelection()
  } else if (e.key === 'Escape') {
    select([])
    tool.value = 'select'
    pickingId.value = null
  } else if (e.key === 'Enter' && single.value) {
    e.preventDefault()
    startEdit(single.value.id)
  } else if (e.key.startsWith('Arrow') && selection.value.length) {
    e.preventDefault()
    const s = e.shiftKey ? 10 : 1
    nudge(e.key === 'ArrowLeft' ? -s : e.key === 'ArrowRight' ? s : 0, e.key === 'ArrowUp' ? -s : e.key === 'ArrowDown' ? s : 0)
  } else if (e.shiftKey && k === '1') fit()
  else if (e.shiftKey && k === '2' && selection.value.length) fit(selection.value)
  else if (k === '0' || (e.shiftKey && k === '0')) zoomAt({ x: rect().width / 2, y: rect().height / 2 }, 1)
  else {
    const t2 = tools.find((x) => x.key.toLowerCase() === k)
    if (t2) tool.value = t2.id
    else if (k === 'o') {
      lastShape.value = 'ellipse'
      tool.value = 'shape'
    } else if (k === 'd') {
      lastShape.value = 'diamond'
      tool.value = 'shape'
    } else if (k === 'n') tool.value = 'sticky'
  }
}
function onKeyUp(e: KeyboardEvent) {
  if (e.key === ' ') spaceDown.value = false
}

onMounted(() => {
  viewport.value?.addEventListener('wheel', onWheel, { passive: false })
  window.addEventListener('keydown', onKey)
  window.addEventListener('keyup', onKeyUp)
})
onBeforeUnmount(() => {
  viewport.value?.removeEventListener('wheel', onWheel)
  window.removeEventListener('keydown', onKey)
  window.removeEventListener('keyup', onKeyUp)
  if (viewTimer) clearTimeout(viewTimer)
  saveView()
})

// First paint: focus a just-sent item, else restore this viewer's last view, else fit.
let placed = false
watch(
  () => store.status.value,
  async (s) => {
    if (placed || s === 'loading' || s === 'missing') return
    placed = true
    await nextTick()
    if (props.focus && store.items.has(props.focus)) {
      const it = store.items.get(props.focus)!
      fit([it.id])
      select([it.id])
      return
    }
    try {
      const saved = JSON.parse(localStorage.getItem(viewKey.value) ?? 'null')
      if (saved && Number.isFinite(saved.zoom)) {
        Object.assign(view, { x: saved.x, y: saved.y, zoom: clampZoom(saved.zoom) })
        return
      }
    } catch {
      // ignore
    }
    fit()
  },
  { immediate: true },
)

defineExpose({ fit, select })

const cursorStyle = computed(() => {
  if (panning.value) return 'grabbing'
  if (tool.value === 'hand' || spaceDown.value) return 'grab'
  if (tool.value !== 'select') return 'crosshair'
  return 'default'
})
const handles = ['nw', 'ne', 'sw', 'se', 'n', 's', 'e', 'w']
const handleStyle = (h: string, r: Rect): Record<string, string> => {
  const x = h.includes('w') ? 0 : h.includes('e') ? r.w : r.w / 2
  const y = h.includes('n') ? 0 : h.includes('s') ? r.h : r.h / 2
  return { left: `${x}px`, top: `${y}px`, cursor: `${h}-resize` }
}
const sideDots = (r: Rect) => [
  { side: 'n', x: r.w / 2, y: 0 },
  { side: 'e', x: r.w, y: r.h / 2 },
  { side: 's', x: r.w / 2, y: r.h },
  { side: 'w', x: 0, y: r.h / 2 },
]
</script>

<template>
  <div
    ref="viewport"
    class="wb-viewport relative size-full touch-none overflow-hidden bg-bg select-none"
    :style="{ ...gridStyle, cursor: cursorStyle }"
    @pointerdown="onPointerDown"
    @pointermove="onPointerMove"
    @pointerup="onPointerUp"
    @pointercancel="onPointerUp"
    @pointerleave="store.cursor.value = null"
    @contextmenu.prevent
  >
    <div class="wb-world absolute top-0 left-0 origin-top-left" :style="{ transform: `translate(${view.x}px, ${view.y}px) scale(${view.zoom})`, '--z': 1 / view.zoom }">
      <!-- Frames sit underneath everything. -->
      <div
        v-for="it in frames"
        :key="it.id"
        :data-item="it.id"
        class="wb-item absolute rounded-xl border"
        :class="{ 'is-selected': selected.has(it.id), 'is-target': hover === it.id }"
        :style="{ left: `${it.x}px`, top: `${it.y}px`, width: `${it.w}px`, height: `${it.h}px`, background: tint(it.data.color ?? 'gray', 5), borderColor: tint(it.data.color ?? 'gray', 30), '--peer': peerSelected.get(it.id) }"
      >
        <div class="absolute bottom-full left-0 mb-1.5 flex max-w-full items-center gap-1.5 text-[13px] font-medium text-muted" :style="{ fontSize: `calc(13px * min(var(--z), 4))` }">
          <div v-if="editing === it.id" data-editing class="min-w-40">
            <textarea v-model="editText" rows="1" class="wb-editor w-full font-medium" @keydown="onEditorKey" @blur="commitEdit" />
          </div>
          <span v-else class="truncate">{{ it.data.title || 'Frame' }}</span>
        </div>
      </div>

      <!-- Connectors -->
      <svg class="pointer-events-none absolute top-0 left-0 overflow-visible" width="1" height="1">
        <g v-for="l in connectors" :key="l.c.id" :data-item="l.c.id" class="pointer-events-auto" :style="{ color: swatchVar[swatchOf(l.c.data.color ?? 'gray')] }">
          <line :x1="l.a.x" :y1="l.a.y" :x2="l.b.x" :y2="l.b.y" stroke="transparent" :stroke-width="Math.max(14, 14 / view.zoom)" class="cursor-pointer" />
          <line
            :x1="l.a.x"
            :y1="l.a.y"
            :x2="l.b.x"
            :y2="l.b.y"
            stroke="currentColor"
            :stroke-width="selected.has(l.c.id) ? 2.5 : 1.75"
            :stroke-dasharray="l.c.data.dashed ? '7 6' : undefined"
            stroke-linecap="round"
          />
          <polygon v-if="l.c.data.style !== 'line'" :points="arrowHead(l.a, l.b)" fill="currentColor" />
          <polygon v-if="l.c.data.style === 'both'" :points="arrowHead(l.b, l.a)" fill="currentColor" />
          <line v-if="selected.has(l.c.id)" :x1="l.a.x" :y1="l.a.y" :x2="l.b.x" :y2="l.b.y" stroke="var(--accent)" stroke-opacity="0.25" :stroke-width="8 / view.zoom" />
        </g>
      </svg>

      <div
        v-for="it in others"
        :key="it.id"
        :data-item="it.id"
        class="wb-item absolute"
        :class="[`wb-${it.kind}`, { 'is-selected': selected.has(it.id), 'is-target': hover === it.id }]"
        :style="{
          left: `${it.x}px`,
          top: `${it.y}px`,
          width: `${it.w}px`,
          height: it.kind === 'text' ? 'auto' : `${it.h}px`,
          '--peer': peerSelected.get(it.id),
        }"
      >
        <!-- Sticky note -->
        <div
          v-if="it.kind === 'sticky'"
          class="size-full overflow-hidden rounded-md p-3.5 text-[15px] leading-snug shadow-[0_1px_2px_rgb(0_0_0/0.08),0_6px_16px_-8px_rgb(0_0_0/0.25)]"
          :style="stickyStyle(it.data.color)"
        >
          <div v-if="editing === it.id" data-editing class="size-full">
            <textarea v-model="editText" class="wb-editor size-full" placeholder="Write something…" @keydown="onEditorKey" @blur="commitEdit" />
          </div>
          <div v-else class="break-words whitespace-pre-wrap">
            <template v-if="it.data.text">{{ it.data.text }}</template>
            <span v-else class="opacity-45">Double-click to write</span>
          </div>
        </div>

        <!-- Text -->
        <div
          v-else-if="it.kind === 'text'"
          class="min-h-6 leading-snug font-medium break-words whitespace-pre-wrap"
          :style="{ fontSize: `${textSizes[(it.data.size as TextSize) ?? 'md'] ?? 18}px`, color: it.data.color ? swatchVar[swatchOf(it.data.color)] : 'var(--fg)' }"
        >
          <div v-if="editing === it.id" data-editing>
            <textarea v-model="editText" rows="1" class="wb-editor w-full" placeholder="Type something" @keydown="onEditorKey" @blur="commitEdit" @input="autoGrow" />
          </div>
          <template v-else>{{ it.data.text }}</template>
        </div>

        <!-- Shape -->
        <div v-else-if="it.kind === 'shape'" class="relative size-full">
          <svg class="absolute inset-0 size-full overflow-visible" :viewBox="`0 0 ${Math.max(it.w, 1)} ${Math.max(it.h, 1)}`" preserveAspectRatio="none">
            <rect v-if="(it.data.shape ?? 'rect') === 'rect'" x="1" y="1" :width="Math.max(it.w - 2, 0)" :height="Math.max(it.h - 2, 0)" rx="10" :fill="tint(it.data.color, 18)" :stroke="swatchVar[swatchOf(it.data.color)]" stroke-width="2" />
            <ellipse v-else-if="it.data.shape === 'ellipse'" :cx="it.w / 2" :cy="it.h / 2" :rx="Math.max(it.w / 2 - 1, 0)" :ry="Math.max(it.h / 2 - 1, 0)" :fill="tint(it.data.color, 18)" :stroke="swatchVar[swatchOf(it.data.color)]" stroke-width="2" />
            <polygon v-else :points="`${it.w / 2},1 ${it.w - 1},${it.h / 2} ${it.w / 2},${it.h - 1} 1,${it.h / 2}`" :fill="tint(it.data.color, 18)" :stroke="swatchVar[swatchOf(it.data.color)]" stroke-width="2" stroke-linejoin="round" />
          </svg>
          <div class="absolute inset-0 grid place-items-center overflow-hidden p-3 text-center text-[14px] leading-snug font-medium" :class="it.data.shape === 'diamond' && 'px-[22%]'">
            <div v-if="editing === it.id" data-editing class="w-full">
              <textarea v-model="editText" rows="2" class="wb-editor w-full text-center" @keydown="onEditorKey" @blur="commitEdit" />
            </div>
            <span v-else class="break-words whitespace-pre-wrap">{{ it.data.text }}</span>
          </div>
        </div>

        <!-- Telemetry snapshot -->
        <EmbedCard
          v-else-if="it.kind === 'embed'"
          :item="it"
          :active="selected.has(it.id) && selection.length === 1"
          :picking="pickingId === it.id"
          @update:picking="(p: boolean) => (pickingId = p ? it.id : null)"
          @patch="(data) => store.update(it.id, {}, data)"
        />
      </div>

      <!-- Connector labels -->
      <div
        v-for="l in connectors.filter((x) => x.c.data.label || editing === x.c.id)"
        :key="`label-${l.c.id}`"
        :data-item="l.c.id"
        class="absolute -translate-x-1/2 -translate-y-1/2 rounded-md border border-border bg-surface px-1.5 py-0.5 text-[11.5px] font-medium whitespace-nowrap text-muted tabular-nums shadow-(--shadow-sm)"
        :style="{ left: `${l.mid.x}px`, top: `${l.mid.y}px` }"
      >
        <div v-if="editing === l.c.id" data-editing>
          <textarea v-model="editText" rows="1" class="wb-editor w-32 text-center" @keydown="onEditorKey" @blur="commitEdit" />
        </div>
        <template v-else>{{ l.c.data.label }}</template>
      </div>

      <!-- Selection handles -->
      <template v-if="single && single.kind !== 'connector' && !editing && tool === 'select'">
        <div class="pointer-events-none absolute" :style="{ left: `${single.x}px`, top: `${single.y}px`, width: `${single.w}px`, height: `${single.kind === 'text' ? single.h : single.h}px` }">
          <div v-for="h in handles.filter((h) => single!.kind !== 'text' || h === 'e' || h === 'w')" :key="h" :data-item="single.id" :data-handle="h" class="wb-handle pointer-events-auto" :class="h.length === 1 && 'wb-handle-edge'" :style="handleStyle(h, single)" />
          <div v-if="single.kind !== 'frame'">
            <div v-for="d in sideDots(single)" :key="d.side" :data-item="single.id" :data-dot="d.side" class="wb-dot pointer-events-auto" :class="`wb-dot-${d.side}`" :style="{ left: `${d.x}px`, top: `${d.y}px` }" title="Drag to connect" />
          </div>
        </div>
      </template>
      <template v-if="single && single.kind === 'connector' && !editing">
        <template v-for="l in connectors.filter((x) => x.c.id === single!.id)" :key="l.c.id">
          <div :data-item="l.c.id" data-end="from" class="wb-end" :style="{ left: `${l.a.x}px`, top: `${l.a.y}px` }" />
          <div :data-item="l.c.id" data-end="to" class="wb-end" :style="{ left: `${l.b.x}px`, top: `${l.b.y}px` }" />
        </template>
      </template>

      <!-- Other people's cursors -->
      <div
        v-for="p in peerList.filter((p) => p.cursor)"
        :key="p.client"
        class="pointer-events-none absolute transition-transform duration-100 ease-linear"
        :style="{ transform: `translate(${p.cursor![0]}px, ${p.cursor![1]}px) scale(var(--z))`, transformOrigin: '0 0' }"
      >
        <svg width="18" height="18" viewBox="0 0 18 18" class="drop-shadow-sm"><path d="M2 2 L16 8 L9 10 L7 16 Z" :fill="peerColor(p.user.id)" stroke="white" stroke-width="1.5" stroke-linejoin="round" /></svg>
        <span class="absolute top-4 left-3 rounded-md px-1.5 py-0.5 text-[11px] font-medium whitespace-nowrap text-white" :style="{ background: peerColor(p.user.id) }">{{ p.user.name }}</span>
      </div>

      <div v-if="marquee" class="pointer-events-none absolute rounded-sm border border-accent bg-accent/8" :style="{ left: `${marquee.x}px`, top: `${marquee.y}px`, width: `${marquee.w}px`, height: `${marquee.h}px`, borderWidth: 'calc(1px * var(--z))' }" />
    </div>

    <!-- Empty board -->
    <div v-if="store.status.value !== 'loading' && store.items.size === 0" class="pointer-events-none absolute inset-0 grid place-items-center p-6">
      <div class="max-w-sm text-center">
        <div class="mx-auto mb-3 grid size-11 place-items-center rounded-xl border border-border bg-surface text-muted shadow-(--shadow-sm)"><StickyNote class="size-5" /></div>
        <div class="text-[15px] font-medium">An empty board</div>
        <p class="mt-1 text-[13px] leading-relaxed text-muted">
          Pick a tool below, or double-click anywhere to write. Use <span class="font-medium text-fg">Send to whiteboard</span> on charts, issues, sessions and traces to bring evidence here.
        </p>
      </div>
    </div>

    <!-- Selection inspector -->
    <div
      v-if="selection.length && !editing"
      data-ui
      class="wb-bar absolute left-1/2 z-10 flex max-w-[calc(100%-1.5rem)] -translate-x-1/2 items-center gap-0.5 overflow-x-auto p-1 max-sm:bottom-[4.5rem] sm:top-3"
      @pointerdown.stop
    >
      <template v-if="[...selKinds].some((k) => k !== 'embed')">
        <button
          v-for="c in swatches"
          :key="c"
          class="grid size-9 shrink-0 place-items-center rounded-md hover:bg-surface-2"
          :aria-label="`Color: ${c}`"
          :aria-pressed="selData.color === c"
          @click="setColor(c)"
        >
          <span class="size-4.5 rounded-full border" :style="{ background: swatchVar[c], borderColor: swatchVar[c], boxShadow: selData.color === c ? `0 0 0 2px var(--surface), 0 0 0 3.5px ${swatchVar[c]}` : undefined }" />
        </button>
        <span class="wb-sep" />
      </template>
      <template v-if="selKinds.has('shape')">
        <button v-for="s in ([['rect', Square], ['ellipse', Circle], ['diamond', Diamond]] as const)" :key="s[0]" class="wb-btn" :aria-pressed="selData.shape === s[0]" :aria-label="s[0]" @click="setShape(s[0])">
          <component :is="s[1]" class="size-4" />
        </button>
        <span class="wb-sep" />
      </template>
      <template v-if="selKinds.has('text')">
        <button v-for="s in (['sm', 'md', 'lg', 'xl'] as const)" :key="s" class="wb-btn text-xs font-semibold uppercase" :aria-pressed="(selData.size ?? 'md') === s" :aria-label="`Text size ${s}`" @click="setData({ size: s }, (i) => i.kind === 'text')">
          {{ s === 'md' ? 'M' : s === 'sm' ? 'S' : s === 'lg' ? 'L' : 'XL' }}
        </button>
        <span class="wb-sep" />
      </template>
      <template v-if="selKinds.has('connector')">
        <button class="wb-btn" :aria-pressed="(selData.style ?? 'arrow') === 'arrow'" aria-label="Arrow" title="Arrow" @click="setData({ style: 'arrow' }, (i) => i.kind === 'connector')"><MoveRight class="size-4" /></button>
        <button class="wb-btn" :aria-pressed="selData.style === 'line'" aria-label="Line" title="Line" @click="setData({ style: 'line' }, (i) => i.kind === 'connector')"><Minus class="size-4" /></button>
        <button class="wb-btn text-[11px] font-semibold" :aria-pressed="selData.style === 'both'" aria-label="Arrows on both ends" title="Both ends" @click="setData({ style: 'both' }, (i) => i.kind === 'connector')">↔</button>
        <button class="wb-btn text-[11px] font-semibold" :aria-pressed="!!selData.dashed" aria-label="Dashed" title="Dashed" @click="setData({ dashed: !selData.dashed }, (i) => i.kind === 'connector')">┄</button>
        <span class="wb-sep" />
      </template>
      <button v-if="editable(single)" class="wb-btn" :aria-label="single?.kind === 'connector' ? 'Edit label' : 'Edit text'" :title="single?.kind === 'connector' ? 'Edit label' : 'Edit text'" @click="startEdit(single!.id)"><Pencil class="size-4" /></button>
      <RouterLink v-if="embedLink" :to="embedLink" class="wb-btn" aria-label="Open original" title="Open where this came from"><ExternalLink class="size-4" /></RouterLink>
      <button class="wb-btn" aria-label="Bring to front" title="Bring to front" @click="reorder(true)"><BringToFront class="size-4" /></button>
      <button class="wb-btn" aria-label="Send to back" title="Send to back" @click="reorder(false)"><SendToBack class="size-4" /></button>
      <button class="wb-btn" aria-label="Duplicate" title="Duplicate (Ctrl+D)" @click="duplicate"><Copy class="size-4" /></button>
      <button class="wb-btn hover:text-danger" aria-label="Delete" title="Delete" @click="deleteSelection"><Trash2 class="size-4" /></button>
    </div>

    <!-- Undo / redo -->
    <div data-ui class="wb-bar absolute top-3 left-3 z-10 flex p-1" @pointerdown.stop>
      <button class="wb-btn" :disabled="!store.canUndo.value" aria-label="Undo" title="Undo (Ctrl+Z)" @click="store.undo()"><Undo2 class="size-4" /></button>
      <button class="wb-btn" :disabled="!store.canRedo.value" aria-label="Redo" title="Redo (Ctrl+Shift+Z)" @click="store.redo()"><Redo2 class="size-4" /></button>
    </div>

    <!-- Zoom -->
    <div data-ui class="wb-bar absolute z-10 flex items-center p-1 max-sm:top-3 max-sm:right-3 sm:right-3 sm:bottom-3" @pointerdown.stop>
      <button class="wb-btn" aria-label="Zoom out" @click="zoomBy(1 / 1.25)"><Minus class="size-4" /></button>
      <button class="h-9 min-w-12 rounded-md px-1 text-xs text-muted tabular-nums hover:bg-surface-2 hover:text-fg" title="Reset to 100%" @click="zoomAt({ x: rect().width / 2, y: rect().height / 2 }, 1)">{{ Math.round(view.zoom * 100) }}%</button>
      <button class="wb-btn" aria-label="Zoom in" @click="zoomBy(1.25)"><Plus class="size-4" /></button>
      <button class="wb-btn" aria-label="Fit everything" title="Fit everything (Shift+1)" @click="fit()"><Maximize class="size-4" /></button>
    </div>

    <!-- Tools -->
    <div data-ui role="toolbar" aria-label="Tools" class="wb-bar absolute bottom-3 left-1/2 z-10 flex -translate-x-1/2 items-center p-1" @pointerdown.stop>
      <button
        v-for="t in tools"
        :key="t.id"
        class="wb-btn wb-tool"
        :aria-pressed="tool === t.id"
        :aria-label="t.label"
        :title="`${t.label} (${t.key})`"
        @click="tool = t.id"
      >
        <component :is="t.id === 'shape' ? { rect: Square, ellipse: Circle, diamond: Diamond }[lastShape] : t.icon" class="size-[18px]" />
      </button>
      <span class="wb-sep max-sm:hidden" />
      <span class="hidden items-center gap-1 px-2 text-[11px] text-faint sm:flex"><ArrowUpRight class="size-3.5" /> Drag the dots to connect</span>
    </div>
  </div>
</template>

<style scoped>
.wb-item.is-selected {
  outline: calc(2px * var(--z)) solid var(--accent);
  outline-offset: calc(3px * var(--z));
  border-radius: 8px;
}
.wb-item:not(.is-selected)[style*='--peer'] {
  outline: calc(2px * var(--z)) solid var(--peer);
  outline-offset: calc(3px * var(--z));
}
.wb-item.is-target {
  outline: calc(2px * var(--z)) dashed var(--accent);
  outline-offset: calc(4px * var(--z));
}
.wb-handle {
  position: absolute;
  width: calc(11px * var(--z));
  height: calc(11px * var(--z));
  transform: translate(-50%, -50%);
  border: calc(1.5px * var(--z)) solid var(--accent);
  background: var(--surface);
  border-radius: calc(3px * var(--z));
}
.wb-handle-edge {
  opacity: 0;
}
.wb-dot,
.wb-end {
  position: absolute;
  width: calc(12px * var(--z));
  height: calc(12px * var(--z));
  border-radius: 999px;
  background: var(--accent);
  border: calc(2px * var(--z)) solid var(--surface);
  cursor: crosshair;
}
.wb-dot {
  opacity: 0.85;
}
.wb-dot-n {
  transform: translate(-50%, calc(-50% - 18px * var(--z)));
}
.wb-dot-s {
  transform: translate(-50%, calc(-50% + 18px * var(--z)));
}
.wb-dot-e {
  transform: translate(calc(-50% + 18px * var(--z)), -50%);
}
.wb-dot-w {
  transform: translate(calc(-50% - 18px * var(--z)), -50%);
}
.wb-end {
  transform: translate(-50%, -50%);
  cursor: move;
}
/* Bigger grab areas for fingers (the visible dot stays small). */
@media (pointer: coarse) {
  .wb-handle,
  .wb-dot,
  .wb-end {
    width: calc(22px * var(--z));
    height: calc(22px * var(--z));
  }
}
.wb-editor {
  display: block;
  resize: none;
  background: transparent;
  outline: none;
  border: 0;
  padding: 0;
  font: inherit;
  color: inherit;
  line-height: inherit;
  overflow: hidden;
}
.wb-editor::placeholder {
  color: currentColor;
  opacity: 0.45;
}
</style>
