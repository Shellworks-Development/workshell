// App-wide dialogs and toasts. Mount <OverlayHost /> once; call these anywhere.
import { reactive } from 'vue'

export interface ConfirmOptions {
  title: string
  message?: string
  confirmLabel?: string
  cancelLabel?: string
  danger?: boolean
  /** Require typing this exact text before the confirm button enables. */
  typeToConfirm?: string
}

interface PendingDialog extends ConfirmOptions {
  id: number
  resolve: (ok: boolean) => void
}

export type ToastTone = 'success' | 'error' | 'info'

export interface Toast {
  id: number
  message: string
  description?: string
  tone: ToastTone
  duration: number
  action?: ToastAction
}

export interface ToastAction {
  label: string
  run: () => void
}

let seq = 0

export const overlay = reactive({
  dialog: null as PendingDialog | null,
  toasts: [] as Toast[],
})

/** Shows a confirmation dialog; resolves true if the user confirms. */
export function confirmDialog(opts: ConfirmOptions): Promise<boolean> {
  overlay.dialog?.resolve(false)
  return new Promise((resolve) => {
    overlay.dialog = { ...opts, id: ++seq, resolve }
  })
}

export function closeDialog(ok: boolean) {
  const d = overlay.dialog
  if (!d) return
  overlay.dialog = null
  d.resolve(ok)
}

type ToastOptions = { tone?: ToastTone; description?: string; duration?: number; action?: ToastAction }

/** toast("Saved") · toast("Saved", "details") · toast("Saved", { tone, description, duration }) */
export function toast(message: string, optsOrDescription: ToastOptions | string = {}) {
  const opts = typeof optsOrDescription === 'string' ? { description: optsOrDescription } : optsOrDescription
  const t: Toast = {
    id: ++seq,
    message,
    description: opts.description,
    tone: opts.tone ?? 'success',
    duration: opts.duration ?? (opts.tone === 'error' ? 6000 : 4000),
    action: opts.action,
  }
  overlay.toasts.push(t)
  if (overlay.toasts.length > 4) overlay.toasts.shift()
  return t.id
}

toast.error = (message: string, description?: string) => toast(message, { tone: 'error', description })
toast.info = (message: string, description?: string) => toast(message, { tone: 'info', description })

export function dismissToast(id: number) {
  const i = overlay.toasts.findIndex((t) => t.id === id)
  if (i >= 0) overlay.toasts.splice(i, 1)
}
