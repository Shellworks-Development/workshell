<script setup lang="ts">
import { Eye, EyeOff } from 'lucide-vue-next'
import { computed, ref } from 'vue'
import CopyButton from './CopyButton.vue'

/*
 * Code snippet with syntax highlighting (Luau or shell) and optional secrets
 * that stay blurred until revealed. Copy always copies the real text.
 * Rendered as plain text spans — no HTML injection.
 */
const props = withDefaults(defineProps<{ code: string; lang?: 'luau' | 'sh'; secrets?: string[]; title?: string }>(), { lang: 'luau', secrets: () => [] })
const revealed = ref(false)

type Kind = 'plain' | 'comment' | 'string' | 'number' | 'keyword' | 'func' | 'builtin' | 'prop' | 'var' | 'flag' | 'secret'
interface Tok {
  t: string
  k: Kind
}

const LUAU_KEYWORDS = new Set(
  'and break do else elseif end false for function if in local nil not or repeat return then true until while continue export type typeof'.split(' '),
)
const LUAU_BUILTINS = new Set('game workspace script require print warn error pcall xpcall task math string table Instance Enum'.split(' '))

function luau(src: string): Tok[] {
  const out: Tok[] = []
  const re =
    /(--\[\[[\s\S]*?\]\]|--[^\n]*)|("(?:[^"\\\n]|\\.)*"|'(?:[^'\\\n]|\\.)*'|\[\[[\s\S]*?\]\])|(\b\d+(?:\.\d+)?\b)|([A-Za-z_][A-Za-z0-9_]*)|(\s+|[^\sA-Za-z0-9_"'-]+|-)/g
  let m: RegExpExecArray | null
  while ((m = re.exec(src))) {
    if (m[1]) out.push({ t: m[1], k: 'comment' })
    else if (m[2]) out.push({ t: m[2], k: 'string' })
    else if (m[3]) out.push({ t: m[3], k: 'number' })
    else if (m[4]) {
      const w = m[4]
      const prev = src[m.index - 1]
      const next = src.slice(re.lastIndex).match(/^\s*([(:"{])/)
      if (LUAU_KEYWORDS.has(w)) out.push({ t: w, k: 'keyword' })
      else if (next && (prev !== '.' || next[1] === '(')) out.push({ t: w, k: 'func' })
      else if (prev === '.' || prev === ':') out.push({ t: w, k: 'prop' })
      else if (LUAU_BUILTINS.has(w)) out.push({ t: w, k: 'builtin' })
      else out.push({ t: w, k: 'plain' })
    } else out.push({ t: m[0], k: 'plain' })
  }
  return out
}

function shell(src: string): Tok[] {
  const out: Tok[] = []
  const re = /(#[^\n]*)|("(?:[^"\\]|\\.)*"|'[^']*'|`[^`]*`)|(\b[A-Z_][A-Z0-9_]*(?==))|(\s-{1,2}[A-Za-z][\w-]*)|(^\s*[a-z][\w-]*|(?<=\|\s*)[a-z][\w-]*)|(\s+|\S)/gm
  let m: RegExpExecArray | null
  while ((m = re.exec(src))) {
    if (m[1]) out.push({ t: m[1], k: 'comment' })
    else if (m[2]) out.push({ t: m[2], k: 'string' })
    else if (m[3]) out.push({ t: m[3], k: 'var' })
    else if (m[4]) out.push({ t: m[4], k: 'flag' })
    else if (m[5]) out.push({ t: m[5], k: 'func' })
    else out.push({ t: m[0], k: 'plain' })
  }
  // Merge consecutive plain single characters to keep the DOM small.
  return out.reduce<Tok[]>((acc, tok) => {
    const last = acc[acc.length - 1]
    if (last && last.k === 'plain' && tok.k === 'plain') last.t += tok.t
    else acc.push({ ...tok })
    return acc
  }, [])
}

// Split tokens so each secret occurrence becomes its own 'secret' token.
function markSecrets(toks: Tok[], secrets: string[]): Tok[] {
  const list = secrets.filter((s) => s.length >= 8)
  if (!list.length) return toks
  const out: Tok[] = []
  for (const tok of toks) {
    let rest = tok.t
    while (rest) {
      let at = -1
      let hit = ''
      for (const s of list) {
        const i = rest.indexOf(s)
        if (i >= 0 && (at < 0 || i < at)) {
          at = i
          hit = s
        }
      }
      if (at < 0) {
        out.push({ t: rest, k: tok.k })
        break
      }
      if (at > 0) out.push({ t: rest.slice(0, at), k: tok.k })
      out.push({ t: hit, k: 'secret' })
      rest = rest.slice(at + hit.length)
    }
  }
  return out
}

const tokens = computed(() => markSecrets(props.lang === 'sh' ? shell(props.code) : luau(props.code), props.secrets))
const hasSecrets = computed(() => tokens.value.some((t) => t.k === 'secret'))

const cls: Record<Kind, string> = {
  plain: '',
  comment: 'text-faint italic',
  string: 'text-success',
  number: 'text-warning',
  keyword: 'text-accent font-medium',
  func: 'text-info',
  builtin: 'text-warning',
  prop: 'text-fg',
  var: 'text-accent',
  flag: 'text-warning',
  secret: '',
}
</script>

<template>
  <div class="overflow-hidden rounded-lg border border-border bg-surface-2">
    <div class="flex items-center gap-2 border-b border-border px-3 py-1.5">
      <span class="min-w-0 flex-1 truncate font-mono text-[11.5px] text-faint">{{ title ?? (lang === 'sh' ? 'shell' : 'luau') }}</span>
      <button
        v-if="hasSecrets"
        type="button"
        class="btn btn-ghost btn-sm h-6 shrink-0"
        :aria-pressed="revealed"
        :aria-label="revealed ? 'Hide keys' : 'Show keys'"
        @click="revealed = !revealed"
      >
        <EyeOff v-if="revealed" class="size-3.5" />
        <Eye v-else class="size-3.5" />
        <span class="max-sm:hidden">{{ revealed ? 'Hide keys' : 'Show keys' }}</span>
      </button>
      <CopyButton :text="code" class="h-6 shrink-0" />
    </div>
    <pre class="overflow-x-auto p-3 font-mono text-[12.5px] leading-relaxed"><code><span
      v-for="(t, i) in tokens"
      :key="i"
      :class="t.k === 'secret' ? (revealed ? 'rounded bg-warning-soft' : 'rounded bg-surface-3 blur-[4px] select-none transition-[filter] duration-200') : cls[t.k]"
      :aria-label="t.k === 'secret' && !revealed ? 'hidden secret' : undefined"
    >{{ t.t }}</span></code></pre>
  </div>
</template>
