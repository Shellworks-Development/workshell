// Color themes. Each theme sets the full token set used across the UI (see
// style.css). Third-party palettes use their official colors.

export type AccentKey = 'violet' | 'blue' | 'teal' | 'pink' | 'orange' | 'green'

export interface Theme {
  id: string
  label: string
  group: 'Workshell' | 'Catppuccin' | 'Rosé Pine' | 'Other'
  dark: boolean
  /** Raw palette; the token set is derived from it. */
  p: {
    bg: string // page
    surface: string // cards
    surface2: string // hover, wells
    surface3: string // selected
    border: string
    borderStrong: string
    fg: string
    muted: string
    faint: string
    red: string
    orange: string
    yellow: string
    green: string
    blue: string
    accents: Record<AccentKey, string>
    chart: string[]
  }
}

const cat = (
  dark: boolean,
  c: {
    base: string
    mantle: string
    crust: string
    surface0: string
    surface1: string
    surface2: string
    overlay0: string
    overlay1: string
    subtext0: string
    text: string
    red: string
    peach: string
    yellow: string
    green: string
    teal: string
    blue: string
    mauve: string
    pink: string
    sky: string
    lavender: string
  },
): Theme['p'] => ({
  bg: dark ? c.mantle : c.mantle,
  surface: c.base,
  surface2: dark ? c.surface0 : c.crust,
  surface3: dark ? c.surface1 : c.surface0,
  border: dark ? c.surface0 : c.crust,
  borderStrong: dark ? c.surface1 : c.surface0,
  fg: c.text,
  muted: c.subtext0,
  faint: dark ? c.overlay0 : c.overlay1,
  red: c.red,
  orange: c.peach,
  yellow: c.yellow,
  green: c.green,
  blue: c.blue,
  accents: { violet: c.mauve, blue: c.blue, teal: c.teal, pink: c.pink, orange: c.peach, green: c.green },
  chart: [c.mauve, c.teal, c.peach, c.blue, c.pink, c.green, c.yellow, c.lavender],
})

export const themes: Theme[] = [
  {
    id: 'light',
    label: 'Workshell Light',
    group: 'Workshell',
    dark: false,
    p: {
      bg: '#f6f6f3',
      surface: '#ffffff',
      surface2: '#f1f1ed',
      surface3: '#e7e6e1',
      border: '#e4e3de',
      borderStrong: '#d2d1cb',
      fg: '#1d1d20',
      muted: '#5f5f66',
      faint: '#8c8c93',
      red: '#d7333f',
      orange: '#c2620a',
      yellow: '#b7791f',
      green: '#2f855a',
      blue: '#2b6cdf',
      accents: { violet: '#6d4aff', blue: '#2b6cdf', teal: '#0f8f86', pink: '#d0457f', orange: '#d9620b', green: '#2f9e5b' },
      chart: ['#6d4aff', '#0f8f86', '#d9620b', '#2b6cdf', '#d0457f', '#5b9a2c', '#b7791f', '#7a7a85'],
    },
  },
  {
    id: 'dark',
    label: 'Workshell Dark',
    group: 'Workshell',
    dark: true,
    p: {
      bg: '#18181b',
      surface: '#1f1f23',
      surface2: '#26262b',
      surface3: '#303036',
      border: '#2c2c32',
      borderStrong: '#3c3c44',
      fg: '#e6e6ea',
      muted: '#a2a2ab',
      faint: '#74747d',
      red: '#f2727c',
      orange: '#f0a35e',
      yellow: '#e8c06a',
      green: '#6fcf97',
      blue: '#7aa7f5',
      accents: { violet: '#9d87ff', blue: '#7aa7f5', teal: '#5fd0c2', pink: '#f08bb8', orange: '#f0a35e', green: '#6fcf97' },
      chart: ['#9d87ff', '#5fd0c2', '#f0a35e', '#7aa7f5', '#f08bb8', '#9bd27a', '#e8c06a', '#a2a2ab'],
    },
  },
  {
    id: 'latte',
    label: 'Latte',
    group: 'Catppuccin',
    dark: false,
    p: cat(false, {
      base: '#eff1f5', mantle: '#e6e9ef', crust: '#dce0e8', surface0: '#ccd0da', surface1: '#bcc0cc', surface2: '#acb0be',
      overlay0: '#9ca0b0', overlay1: '#8c8fa1', subtext0: '#6c6f85', text: '#4c4f69', red: '#d20f39', peach: '#fe640b',
      yellow: '#df8e1d', green: '#40a02b', teal: '#179299', blue: '#1e66f5', mauve: '#8839ef', pink: '#ea76cb', sky: '#04a5e5', lavender: '#7287fd',
    }),
  },
  {
    id: 'frappe',
    label: 'Frappé',
    group: 'Catppuccin',
    dark: true,
    p: cat(true, {
      base: '#303446', mantle: '#292c3c', crust: '#232634', surface0: '#414559', surface1: '#51576d', surface2: '#626880',
      overlay0: '#737994', overlay1: '#838ba7', subtext0: '#a5adce', text: '#c6d0f5', red: '#e78284', peach: '#ef9f76',
      yellow: '#e5c890', green: '#a6d189', teal: '#81c8be', blue: '#8caaee', mauve: '#ca9ee6', pink: '#f4b8e4', sky: '#99d1db', lavender: '#babbf1',
    }),
  },
  {
    id: 'macchiato',
    label: 'Macchiato',
    group: 'Catppuccin',
    dark: true,
    p: cat(true, {
      base: '#24273a', mantle: '#1e2030', crust: '#181926', surface0: '#363a4f', surface1: '#494d64', surface2: '#5b6078',
      overlay0: '#6e738d', overlay1: '#8087a2', subtext0: '#a5adcb', text: '#cad3f5', red: '#ed8796', peach: '#f5a97f',
      yellow: '#eed49f', green: '#a6da95', teal: '#8bd5ca', blue: '#8aadf4', mauve: '#c6a0f6', pink: '#f5bde6', sky: '#91d7e3', lavender: '#b7bdf8',
    }),
  },
  {
    id: 'mocha',
    label: 'Mocha',
    group: 'Catppuccin',
    dark: true,
    p: cat(true, {
      base: '#1e1e2e', mantle: '#181825', crust: '#11111b', surface0: '#313244', surface1: '#45475a', surface2: '#585b70',
      overlay0: '#6c7086', overlay1: '#7f849c', subtext0: '#a6adc8', text: '#cdd6f4', red: '#f38ba8', peach: '#fab387',
      yellow: '#f9e2af', green: '#a6e3a1', teal: '#94e2d5', blue: '#89b4fa', mauve: '#cba6f7', pink: '#f5c2e7', sky: '#89dceb', lavender: '#b4befe',
    }),
  },
  {
    id: 'rose-pine-dawn',
    label: 'Dawn',
    group: 'Rosé Pine',
    dark: false,
    p: {
      bg: '#faf4ed', surface: '#fffaf3', surface2: '#f4ede8', surface3: '#dfdad9', border: '#f2e9e1', borderStrong: '#dfdad9',
      fg: '#575279', muted: '#797593', faint: '#9893a5', red: '#b4637a', orange: '#d7827e', yellow: '#ea9d34', green: '#286983', blue: '#56949f',
      accents: { violet: '#907aa9', blue: '#56949f', teal: '#286983', pink: '#b4637a', orange: '#d7827e', green: '#286983' },
      chart: ['#907aa9', '#56949f', '#ea9d34', '#286983', '#b4637a', '#d7827e', '#797593', '#9893a5'],
    },
  },
  {
    id: 'rose-pine',
    label: 'Rosé Pine',
    group: 'Rosé Pine',
    dark: true,
    p: {
      bg: '#191724', surface: '#1f1d2e', surface2: '#26233a', surface3: '#403d52', border: '#26233a', borderStrong: '#403d52',
      fg: '#e0def4', muted: '#908caa', faint: '#6e6a86', red: '#eb6f92', orange: '#ebbcba', yellow: '#f6c177', green: '#9ccfd8', blue: '#9ccfd8',
      accents: { violet: '#c4a7e7', blue: '#9ccfd8', teal: '#31748f', pink: '#eb6f92', orange: '#ebbcba', green: '#9ccfd8' },
      chart: ['#c4a7e7', '#9ccfd8', '#f6c177', '#31748f', '#eb6f92', '#ebbcba', '#908caa', '#6e6a86'],
    },
  },
  {
    id: 'nord',
    label: 'Nord',
    group: 'Other',
    dark: true,
    p: {
      bg: '#2e3440', surface: '#3b4252', surface2: '#434c5e', surface3: '#4c566a', border: '#434c5e', borderStrong: '#4c566a',
      fg: '#eceff4', muted: '#d8dee9', faint: '#8a93a5', red: '#bf616a', orange: '#d08770', yellow: '#ebcb8b', green: '#a3be8c', blue: '#81a1c1',
      accents: { violet: '#b48ead', blue: '#88c0d0', teal: '#8fbcbb', pink: '#b48ead', orange: '#d08770', green: '#a3be8c' },
      chart: ['#88c0d0', '#a3be8c', '#ebcb8b', '#b48ead', '#d08770', '#8fbcbb', '#81a1c1', '#bf616a'],
    },
  },
  {
    id: 'gruvbox',
    label: 'Gruvbox',
    group: 'Other',
    dark: true,
    p: {
      bg: '#1d2021', surface: '#282828', surface2: '#32302f', surface3: '#3c3836', border: '#3c3836', borderStrong: '#504945',
      fg: '#ebdbb2', muted: '#bdae93', faint: '#928374', red: '#fb4934', orange: '#fe8019', yellow: '#fabd2f', green: '#b8bb26', blue: '#83a598',
      accents: { violet: '#d3869b', blue: '#83a598', teal: '#8ec07c', pink: '#d3869b', orange: '#fe8019', green: '#b8bb26' },
      chart: ['#fabd2f', '#8ec07c', '#fe8019', '#83a598', '#d3869b', '#b8bb26', '#fb4934', '#a89984'],
    },
  },
]

export function themeById(id: string): Theme {
  return themes.find((t) => t.id === id) ?? themes[1]!
}

/** CSS custom properties for a theme with the chosen accent. */
export function themeVars(t: Theme, accent: AccentKey): Record<string, string> {
  const p = t.p
  const soft = (c: string) => `color-mix(in srgb, ${c} ${t.dark ? 16 : 11}%, ${p.surface})`
  const a = p.accents[accent] ?? p.accents.violet
  return {
    '--bg': p.bg,
    '--surface': p.surface,
    '--surface-2': p.surface2,
    '--surface-3': p.surface3,
    '--border': p.border,
    '--border-strong': p.borderStrong,
    '--fg': p.fg,
    '--muted': p.muted,
    '--faint': p.faint,
    '--inverse': p.surface,
    '--accent': a,
    '--accent-fg': t.dark ? p.bg : '#ffffff',
    '--accent-soft': soft(a),
    '--danger': p.red,
    '--danger-soft': soft(p.red),
    '--warning': t.dark ? p.yellow : p.orange,
    '--warning-soft': soft(t.dark ? p.yellow : p.orange),
    '--success': p.green,
    '--success-soft': soft(p.green),
    '--info': p.blue,
    '--info-soft': soft(p.blue),
  }
}
