// A whiteboard's live state: items, undo/redo, batched saving, and syncing
// with everyone else viewing the board over the project's live stream.
import { computed, onBeforeUnmount, reactive, ref, watch, type Ref } from 'vue'
import { api, type BoardChange, type BoardItem, type BoardPresence, type UserRef, type Whiteboard } from './api'
import { useLive } from './live'
import { uid } from './whiteboard'

type Snapshot = Map<string, BoardItem | null>
interface Step {
  before: Snapshot
  after: Snapshot
}

export interface Peer {
  client: string
  user: UserRef
  cursor: [number, number] | null
  selected: string[]
  seen: number
}

// Items are plain JSON; a JSON copy also unwraps Vue's reactive proxies
// (which structuredClone refuses).
export const clone = <T>(v: T): T => (v === null || v === undefined ? v : (JSON.parse(JSON.stringify(v)) as T))

export function useBoard(pid: Ref<number>, bid: Ref<number>) {
  const board = ref<Whiteboard | null>(null)
  const items = reactive(new Map<string, BoardItem>())
  const status = ref<'loading' | 'saved' | 'saving' | 'error' | 'missing'>('loading')
  const error = ref('')
  const client = uid()

  // ---- Undo / redo -------------------------------------------------------------
  const undoStack = ref<Step[]>([])
  const redoStack = ref<Step[]>([])
  let tx: Snapshot | null = null
  let txDepth = 0

  /** Groups every change until the matching end() into one undo step. */
  function begin() {
    if (txDepth++ === 0) tx = new Map()
  }
  function end() {
    if (txDepth === 0 || --txDepth > 0) return
    const before = tx!
    tx = null
    if (before.size === 0) return
    const after: Snapshot = new Map()
    let changed = false
    for (const [id, b] of before) {
      const a = clone(items.get(id) ?? null)
      after.set(id, a)
      if (JSON.stringify(a) !== JSON.stringify(b)) changed = true
    }
    if (!changed) return
    undoStack.value = [...undoStack.value.slice(-199), { before, after }]
    redoStack.value = []
  }
  function touch(id: string) {
    if (tx && !tx.has(id)) tx.set(id, clone(items.get(id) ?? null))
  }
  /** Runs fn as one undoable step. */
  function change(fn: () => void) {
    begin()
    try {
      fn()
    } finally {
      end()
    }
  }

  // ---- Mutations ---------------------------------------------------------------
  const dirty = new Set<string>()
  const deleted = new Set<string>()
  /** Items being dragged or edited here: remote updates to them wait. */
  const locked = new Set<string>()

  function put(item: BoardItem) {
    change(() => {
      touch(item.id)
      items.set(item.id, item)
      deleted.delete(item.id)
      dirty.add(item.id)
    })
    schedule()
  }
  function update(id: string, patch: Partial<BoardItem>, data?: Record<string, unknown>) {
    const it = items.get(id)
    if (!it) return
    change(() => {
      touch(id)
      Object.assign(it, patch)
      if (data) it.data = { ...it.data, ...data }
      dirty.add(id)
    })
    schedule()
  }
  function remove(ids: Iterable<string>) {
    change(() => {
      for (const id of ids) {
        if (!items.has(id)) continue
        touch(id)
        items.delete(id)
        dirty.delete(id)
        deleted.add(id)
      }
    })
    schedule()
  }

  function apply(snap: Snapshot) {
    for (const [id, it] of snap) {
      if (it) {
        items.set(id, clone(it))
        deleted.delete(id)
        dirty.add(id)
      } else if (items.has(id)) {
        items.delete(id)
        dirty.delete(id)
        deleted.add(id)
      }
    }
    schedule()
  }
  function undo() {
    const step = undoStack.value.at(-1)
    if (!step) return
    undoStack.value = undoStack.value.slice(0, -1)
    redoStack.value = [...redoStack.value, step]
    apply(step.before)
  }
  function redo() {
    const step = redoStack.value.at(-1)
    if (!step) return
    redoStack.value = redoStack.value.slice(0, -1)
    undoStack.value = [...undoStack.value, step]
    apply(step.after)
  }

  // ---- Saving --------------------------------------------------------------------
  let timer: ReturnType<typeof setTimeout> | null = null
  let saving: Promise<void> | null = null
  let retryDelay = 1000

  function schedule(delay = 350) {
    if (status.value !== 'loading') status.value = 'saving'
    if (timer) clearTimeout(timer)
    timer = setTimeout(flush, delay)
  }

  async function flush(): Promise<void> {
    timer = null
    if (saving) {
      await saving
      return flush()
    }
    if (dirty.size === 0 && deleted.size === 0) {
      if (status.value === 'saving') status.value = 'saved'
      return
    }
    // Items still being dragged are saved when the drag ends.
    const ids = [...dirty].filter((id) => !locked.has(id) && items.has(id)).slice(0, 400)
    const gone = [...deleted].slice(0, Math.max(0, 400 - ids.length))
    if (ids.length === 0 && gone.length === 0) return
    for (const id of ids) dirty.delete(id)
    for (const id of gone) deleted.delete(id)
    const upsert = ids.map((id) => items.get(id)!)
    saving = (async () => {
      try {
        const res = await api.post<{ items: BoardItem[] }>(`/projects/${pid.value}/whiteboards/${bid.value}/items`, { client, upsert, delete: gone })
        for (const it of res.items) {
          const cur = items.get(it.id)
          if (cur) cur.rev = it.rev
        }
        retryDelay = 1000
        error.value = ''
        status.value = dirty.size || deleted.size ? 'saving' : 'saved'
      } catch (e) {
        // Put them back and retry with backoff (offline, server restart…).
        for (const id of ids) dirty.add(id)
        for (const id of gone) if (!items.has(id)) deleted.add(id)
        error.value = (e as Error).message
        status.value = 'error'
        retryDelay = Math.min(retryDelay * 2, 30_000)
        if (timer) clearTimeout(timer)
        timer = setTimeout(flush, retryDelay)
      } finally {
        saving = null
      }
    })()
    await saving
    if (dirty.size || deleted.size) schedule(50)
  }

  function unlock(ids: Iterable<string>) {
    for (const id of ids) locked.delete(id)
    schedule(0)
  }

  // ---- Loading -------------------------------------------------------------------
  async function load() {
    try {
      const res = await api.get<{ board: Whiteboard; items: BoardItem[] }>(`/projects/${pid.value}/whiteboards/${bid.value}`)
      board.value = res.board
      const seen = new Set<string>()
      for (const it of res.items) {
        seen.add(it.id)
        // Keep local edits that haven't reached the server yet.
        if (!dirty.has(it.id) && !locked.has(it.id)) items.set(it.id, it)
      }
      for (const id of [...items.keys()]) if (!seen.has(id) && !dirty.has(id)) items.delete(id)
      if (status.value === 'loading' || status.value === 'missing') status.value = 'saved'
    } catch (e) {
      error.value = (e as Error).message
      status.value = 'missing'
    }
  }

  // ---- Collaboration -----------------------------------------------------------
  const peers = reactive(new Map<string, Peer>())
  const onRemoteDelete = ref<(() => void) | null>(null)

  function applyRemote(c: BoardChange) {
    if (c.board !== bid.value || c.client === client) return
    if (c.deletedBoard) {
      onRemoteDelete.value?.()
      return
    }
    if (board.value && c.title !== undefined) {
      board.value.title = c.title
      board.value.description = c.description ?? board.value.description
    }
    if (c.reload) return void load()
    for (const it of c.items ?? []) {
      if (locked.has(it.id) || dirty.has(it.id)) continue
      const cur = items.get(it.id)
      if (!cur || (cur.rev ?? 0) < (it.rev ?? 0)) items.set(it.id, it)
    }
    for (const id of c.deleted ?? []) if (!locked.has(id)) items.delete(id)
  }

  function applyPresence(p: BoardPresence) {
    if (p.board !== bid.value || p.client === client) return
    if (p.leaving) {
      peers.delete(p.client)
      return
    }
    const known = peers.has(p.client)
    peers.set(p.client, { client: p.client, user: p.user, cursor: p.cursor, selected: p.selected, seen: Date.now() })
    // Someone new: say hello so they see us without waiting for a heartbeat.
    if (!known) sendPresence(true)
  }

  const { connected } = useLive(pid, (m) => {
    if (m.t === 'wb') applyRemote(m.d)
    else if (m.t === 'wb_presence') applyPresence(m.d)
  })
  // Messages may be missed while disconnected: catch up when back.
  let wasConnected = false
  watch(connected, (c) => {
    if (c && wasConnected) load()
    if (c) sendPresence(true)
    wasConnected ||= c
  })

  const cursor = ref<[number, number] | null>(null)
  const selected = ref<string[]>([])
  let lastSent = 0
  let presenceTimer: ReturnType<typeof setTimeout> | null = null
  function sendPresence(force = false) {
    const now = Date.now()
    // Cursor moves are only worth sending when someone is watching.
    if (!force && peers.size === 0) return
    const wait = 120 - (now - lastSent)
    if (!force && wait > 0) {
      presenceTimer ??= setTimeout(() => {
        presenceTimer = null
        sendPresence()
      }, wait)
      return
    }
    lastSent = now
    api.post(`/projects/${pid.value}/whiteboards/${bid.value}/presence`, { client, cursor: cursor.value, selected: selected.value.slice(0, 50) }).catch(() => {})
  }
  const heartbeat = setInterval(() => sendPresence(true), 10_000)
  const sweep = setInterval(() => {
    for (const [k, p] of peers) if (Date.now() - p.seen > 25_000) peers.delete(k)
  }, 5_000)

  function leave() {
    fetch(`/api/projects/${pid.value}/whiteboards/${bid.value}/presence`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ client, leaving: true }),
      keepalive: true,
      credentials: 'same-origin',
    }).catch(() => {})
  }
  function onPageHide() {
    leave()
    if (dirty.size || deleted.size) {
      const upsert = [...dirty].map((id) => items.get(id)).filter(Boolean)
      fetch(`/api/projects/${pid.value}/whiteboards/${bid.value}/items`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ client, upsert, delete: [...deleted] }),
        keepalive: true,
        credentials: 'same-origin',
      }).catch(() => {})
    }
  }
  window.addEventListener('pagehide', onPageHide)
  onBeforeUnmount(() => {
    clearInterval(heartbeat)
    clearInterval(sweep)
    if (presenceTimer) clearTimeout(presenceTimer)
    window.removeEventListener('pagehide', onPageHide)
    leave()
    if (timer) {
      clearTimeout(timer)
      flush()
    }
  })

  async function rename(title: string) {
    if (!board.value) return
    const b = await api.patch<Whiteboard>(`/projects/${pid.value}/whiteboards/${bid.value}`, { title })
    board.value.title = b.title
  }

  const sorted = computed(() => [...items.values()].sort((a, b) => a.z - b.z || (a.id < b.id ? -1 : 1)))
  const maxZ = () => Math.max(0, ...[...items.values()].map((i) => i.z))
  const minZ = () => Math.min(0, ...[...items.values()].map((i) => i.z))

  return {
    board,
    items,
    sorted,
    status,
    error,
    client,
    connected,
    peers,
    cursor,
    selected,
    onRemoteDelete,
    load,
    put,
    update,
    remove,
    change,
    begin,
    end,
    touch,
    locked,
    unlock,
    undo,
    redo,
    canUndo: computed(() => undoStack.value.length > 0),
    canRedo: computed(() => redoStack.value.length > 0),
    flush,
    sendPresence,
    rename,
    maxZ,
    minZ,
    markDirty: (id: string) => {
      dirty.add(id)
      schedule()
    },
  }
}

export type BoardStore = ReturnType<typeof useBoard>
