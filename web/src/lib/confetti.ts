// A short confetti burst for resolving issues: canvas-drawn, ~0.9 s, no
// dependencies. Skipped entirely for people who prefer reduced motion.
import { activeTheme } from './state'

interface Piece {
  x: number
  y: number
  vx: number
  vy: number
  rot: number
  vr: number
  w: number
  h: number
  color: string
  life: number
}

export function confetti(origin: { x: number; y: number }, count = 70) {
  if (matchMedia('(prefers-reduced-motion: reduce)').matches) return
  const dpr = Math.min(window.devicePixelRatio || 1, 2)
  const canvas = document.createElement('canvas')
  canvas.width = innerWidth * dpr
  canvas.height = innerHeight * dpr
  Object.assign(canvas.style, { position: 'fixed', inset: '0', width: '100vw', height: '100vh', pointerEvents: 'none', zIndex: '120' })
  document.body.append(canvas)
  const ctx = canvas.getContext('2d')!
  ctx.scale(dpr, dpr)

  const colors = activeTheme.value.p.chart
  const pieces: Piece[] = Array.from({ length: count }, () => {
    // Mostly upward, fanning out: a cone around straight up.
    const angle = -Math.PI / 2 + (Math.random() - 0.5) * Math.PI * 0.9
    const speed = 7 + Math.random() * 7
    return {
      x: origin.x,
      y: origin.y,
      vx: Math.cos(angle) * speed,
      vy: Math.sin(angle) * speed,
      rot: Math.random() * Math.PI,
      vr: (Math.random() - 0.5) * 0.4,
      w: 5 + Math.random() * 4,
      h: 8 + Math.random() * 6,
      color: colors[Math.floor(Math.random() * colors.length)]!,
      life: 0,
    }
  })

  const duration = 900 // ms; pieces fade over the last third
  const start = performance.now()
  let last = start
  function frame(now: number) {
    const dt = Math.min((now - last) / 16.67, 3)
    last = now
    const t = (now - start) / duration
    ctx.clearRect(0, 0, innerWidth, innerHeight)
    for (const p of pieces) {
      p.vy += 0.38 * dt // gravity
      p.vx *= 0.985 ** dt // air drag
      p.x += p.vx * dt
      p.y += p.vy * dt
      p.rot += p.vr * dt
      ctx.save()
      ctx.globalAlpha = t < 0.66 ? 1 : Math.max(0, 1 - (t - 0.66) / 0.34)
      ctx.translate(p.x, p.y)
      ctx.rotate(p.rot)
      ctx.fillStyle = p.color
      // Squash horizontally over time so pieces look like they're flipping.
      ctx.scale(Math.cos(p.rot * 3), 1)
      ctx.fillRect(-p.w / 2, -p.h / 2, p.w, p.h)
      ctx.restore()
    }
    if (t < 1) requestAnimationFrame(frame)
    else canvas.remove()
  }
  requestAnimationFrame(frame)
}

/** Center of the element that triggered an event (falls back to screen center). */
export function originOf(e?: Event): { x: number; y: number } {
  const el = e?.currentTarget as HTMLElement | null
  const r = el?.getBoundingClientRect()
  return r ? { x: r.left + r.width / 2, y: r.top + r.height / 2 } : { x: innerWidth / 2, y: innerHeight / 2 }
}
