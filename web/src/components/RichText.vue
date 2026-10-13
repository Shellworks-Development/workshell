<script lang="ts">
import { defineComponent, h, type VNode } from 'vue'
import { RouterLink } from 'vue-router'

/*
 * Renders comment text with a small, safe Markdown subset: ```code blocks```,
 * `inline code`, **bold**, *italic*, links, @mentions and line breaks.
 * Everything is built as VNodes — no HTML strings — so user text can never
 * inject markup or scripts.
 */
function inline(text: string, mentionPath?: (handle: string) => string): (VNode | string)[] {
  const out: (VNode | string)[] = []
  const re = /`([^`\n]+)`|\*\*([^*\n]+)\*\*|\*([^*\n]+)\*|(https?:\/\/[^\s<>()]+[^\s<>().,;:!?'"])|(^|[^\w@])@([a-z0-9][a-z0-9_-]{1,31})/g
  let last = 0
  let m: RegExpExecArray | null
  while ((m = re.exec(text))) {
    let start = m.index
    if (m[6] !== undefined) start += m[5]!.length
    if (start > last) out.push(text.slice(last, start))
    if (m[1] !== undefined) out.push(h('code', { class: 'rounded bg-surface-2 px-1 py-px font-mono text-[12.5px]' }, m[1]))
    else if (m[2] !== undefined) out.push(h('strong', { class: 'font-semibold' }, m[2]))
    else if (m[3] !== undefined) out.push(h('em', m[3]))
    else if (m[4] !== undefined) out.push(h('a', { href: m[4], target: '_blank', rel: 'noopener noreferrer nofollow', class: 'link break-all' }, m[4]))
    else if (m[6] !== undefined) {
      const handle = m[6]
      out.push(
        mentionPath
          ? h(RouterLink, { to: mentionPath(handle), class: 'rounded bg-accent-soft px-0.5 font-medium text-accent' }, () => '@' + handle)
          : h('span', { class: 'rounded bg-accent-soft px-0.5 font-medium text-accent' }, '@' + handle),
      )
    }
    last = start + (m[6] !== undefined ? m[6].length + 1 : m[0].length)
    re.lastIndex = last
  }
  if (last < text.length) out.push(text.slice(last))
  return out
}

export default defineComponent({
  props: { text: { type: String, required: true }, mentionPath: { type: Function, default: undefined } },
  setup(props) {
    return () => {
      const blocks: VNode[] = []
      const parts = props.text.split(/```(?:\w+)?\n?([\s\S]*?)```/g)
      parts.forEach((part, i) => {
        if (i % 2 === 1) {
          blocks.push(h('pre', { class: 'my-2 overflow-x-auto rounded-md border border-border bg-surface-2 p-3 font-mono text-[12.5px] leading-relaxed' }, part.replace(/\n$/, '')))
          return
        }
        for (const para of part.split(/\n{2,}/)) {
          if (!para.trim()) continue
          const lines = para.split('\n')
          const children: (VNode | string)[] = []
          lines.forEach((line, j) => {
            if (j > 0) children.push(h('br'))
            children.push(...inline(line, props.mentionPath as ((h: string) => string) | undefined))
          })
          blocks.push(h('p', { class: 'my-1.5 leading-relaxed break-words' }, children))
        }
      })
      return h('div', { class: 'text-[13.5px] [&>*:first-child]:mt-0 [&>*:last-child]:mb-0' }, blocks)
    }
  },
})
</script>
