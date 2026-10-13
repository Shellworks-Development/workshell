import { computed } from 'vue'
import { activeTheme } from './state'

// Chart colors come from the active theme so charts match Catppuccin, Nord, etc.
export const palette = computed(() => activeTheme.value.p.chart)

export const levelColors = computed(() => {
  const p = activeTheme.value.p
  return { debug: p.faint, info: p.blue, warn: activeTheme.value.dark ? p.yellow : p.orange, error: p.red }
})

export const severityColors = computed(() => {
  const p = activeTheme.value.p
  return { low: p.faint, medium: activeTheme.value.dark ? p.yellow : p.orange, high: p.red }
})

export const axisColors = computed(() => {
  const p = activeTheme.value.p
  return { text: p.faint, line: p.border, tooltipBg: p.surface, tooltipBorder: p.borderStrong, tooltipText: p.fg }
})
