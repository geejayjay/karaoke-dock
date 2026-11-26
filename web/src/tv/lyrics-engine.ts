import type { LyricLine, LyricWord, LyricsDoc, Voice } from '../shared/types'

const CJK = /[぀-ヿ㐀-鿿가-힯]/

export const VOICE_COLORS: Record<string, string> = {
  a: '#5cc8ff',
  b: '#ff7ac6',
  both: '#ffd166',
  c: '#8ef0a7',
  d: '#ffa35c',
}

/** Keeps a colour's hue but makes it bright and vivid enough to read as a highlight. */
export function readableColor(hex: string): string {
  const n = parseInt(hex.replace('#', ''), 16)
  if (Number.isNaN(n)) return hex
  const r = ((n >> 16) & 255) / 255
  const g = ((n >> 8) & 255) / 255
  const b = (n & 255) / 255
  const max = Math.max(r, g, b)
  const min = Math.min(r, g, b)
  const d = max - min
  let h = 0
  if (d > 0) h = max === r ? ((g - b) / d + 6) % 6 : max === g ? (b - r) / d + 2 : (r - g) / d + 4
  const light = Math.max((max + min) / 2, 0.68)
  const chroma = (1 - Math.abs(2 * light - 1)) * (d > 0.04 ? 1 : 0)
  const x = chroma * (1 - Math.abs((h % 2) - 1))
  const m = light - chroma / 2
  const [a, c, e] = [[chroma, x, 0], [x, chroma, 0], [0, chroma, x], [0, x, chroma], [x, 0, chroma], [chroma, 0, x]][Math.floor(h) % 6]
  const part = (v: number) => Math.round((v + m) * 255).toString(16).padStart(2, '0')
  return `#${part(a)}${part(c)}${part(e)}`
}

/** Index of the line that should show first at a given time. */
export function lineIndexAt(lines: LyricLine[], ms: number): number {
  let lo = 0
  let hi = lines.length - 1
  let ans = 0
  while (lo <= hi) {
    const mid = (lo + hi) >> 1
    if (lines[mid].startMs <= ms) {
      ans = mid
      lo = mid + 1
    } else {
      hi = mid - 1
    }
  }
  return ans
}

/** Closes gaps under 120 ms so the wipe doesn't flicker between words. */
export function closeGaps(words: LyricWord[]): LyricWord[] {
  return words.map((w, i) => {
    const next = words[i + 1]
    const endMs = next && next.startMs - w.endMs < 120 ? next.startMs : w.endMs
    return { ...w, endMs: Math.max(endMs, w.startMs + 1) }
  })
}

/** Marks the words inside brackets, which are backing vocals. */
export function backingWords(words: LyricWord[]): boolean[] {
  const all = words.map((w) => w.text).join('')
  // A close with no open means the bracket began on the row before.
  let depth = (all.match(/[)）]/g) ?? []).length > (all.match(/[(（]/g) ?? []).length ? 1 : 0
  return words.map((w) => {
    const opens = (w.text.match(/[(（]/g) ?? []).length
    const closes = (w.text.match(/[)）]/g) ?? []).length
    const backing = depth > 0 || opens > 0
    depth = Math.max(depth + opens - closes, 0)
    return backing
  })
}

export function needsSpace(a: string, b: string): boolean {
  return !(CJK.test(a[a.length - 1] ?? '') || CJK.test(b[0] ?? ''))
}

interface RenderedWord {
  word: LyricWord
  el: HTMLElement
  anims: Animation[]
}

interface Slot {
  root: HTMLElement
  line: LyricLine | null
  index: number
  words: RenderedWord[]
}

/**
 * Draws two lyric lines and animates the wipe. Each word is drawn twice, and
 * the colored copy is revealed by two opposing transforms inside overflow
 * hidden, so nothing repaints. This code stays outside Vue reactivity.
 */
export class LyricsEngine {
  private doc: LyricsDoc | null = null
  private slots: [Slot, Slot]
  private current = -1
  private dots: HTMLElement
  private lite = false
  private accent = '#ff4d8d'
  private pending: LyricsDoc | null = null

  constructor(
    private host: HTMLElement,
    private opts: { countdown: boolean },
  ) {
    host.classList.add('lyrics')
    const mk = (): Slot => {
      const root = document.createElement('div')
      root.className = 'lyric-line'
      host.appendChild(root)
      return { root, line: null, index: -1, words: [] }
    }
    this.slots = [mk(), mk()]
    this.dots = document.createElement('div')
    this.dots.className = 'lyric-dots'
    this.dots.innerHTML = '<i></i><i></i><i></i>'
    host.appendChild(this.dots)
  }

  setDoc(doc: LyricsDoc | null, accent: string) {
    this.cancelAll()
    this.pending = null
    this.doc = doc
    this.accent = readableColor(accent || this.accent)
    this.host.style.setProperty('--lyric-accent', this.accent)
    this.current = -1
    for (const s of this.slots) this.clearSlot(s)
    this.dots.classList.remove('show')
  }

  setLite(on: boolean) {
    this.lite = on
    this.host.classList.toggle('lite', on)
  }

  setCountdown(on: boolean) {
    this.opts.countdown = on
  }

  get voices(): Voice[] {
    return this.doc?.voices ?? []
  }

  private offset(): number {
    return this.doc?.offsetMs ?? 0
  }

  private color(line: LyricLine): string {
    return line.voice ? (VOICE_COLORS[line.voice] ?? this.accent) : this.accent
  }

  private clearSlot(s: Slot) {
    for (const w of s.words) w.anims.forEach((a) => a.cancel())
    s.root.textContent = ''
    s.root.classList.remove('show', 'done', 'active')
    s.words = []
    s.line = null
    s.index = -1
  }

  private cancelAll() {
    for (const s of this.slots) for (const w of s.words) w.anims.forEach((a) => a.cancel())
  }

  private build(s: Slot, index: number) {
    const lines = this.doc!.lines
    const line = lines[index]
    this.clearSlot(s)
    s.line = line
    s.index = index
    s.root.style.setProperty('--word-color', this.color(line))
    const words = closeGaps(line.words)
    const backing = line.plain ? words.map(() => false) : line.backingOnly ? words.map(() => true) : backingWords(line.words)
    const parts = [...words.map((word, i) => ({ word, isBacking: backing[i] })), ...closeGaps(line.backing ?? []).map((word) => ({ word, isBacking: true }))]
    // The main words and the backing words each get their own row.
    const mainRow = document.createElement('div')
    const backingRow = document.createElement('div')
    backingRow.className = 'backing-row'
    const previous: [string, string] = ['', '']
    const frag = document.createDocumentFragment()
    parts.forEach(({ word, isBacking }) => {
      const row = isBacking ? backingRow : mainRow
      const last = previous[isBacking ? 1 : 0]
      if (last && needsSpace(last, word.text)) row.appendChild(document.createTextNode(' '))
      previous[isBacking ? 1 : 0] = word.text
      const el = document.createElement('span')
      el.className = isBacking ? 'word backing' : 'word'
      const base = document.createElement('span')
      base.textContent = word.text
      const clip = document.createElement('span')
      clip.className = 'clip'
      const fill = document.createElement('span')
      fill.className = 'fill'
      fill.textContent = word.text
      clip.appendChild(fill)
      el.append(base, clip)
      row.appendChild(el)
      s.words.push({ word, el, anims: [] })
    })
    if (mainRow.childNodes.length) frag.appendChild(mainRow)
    if (backingRow.childNodes.length) frag.appendChild(backingRow)
    s.root.appendChild(frag)
    this.fit(s)
  }

  /** Shrinks a long line to one row while it's still hidden. */
  private fit(s: Slot) {
    s.root.style.fontSize = ''
    const max = this.host.clientWidth * 0.92
    const width = s.root.scrollWidth
    if (width > max && width > 0) s.root.style.fontSize = `${Math.floor((max / width) * 100)}%`
  }

  private animate(s: Slot, posMs: number, playing: boolean) {
    for (const r of s.words) {
      r.anims.forEach((a) => a.cancel())
      const dur = Math.max(r.word.endMs - r.word.startMs, 1)
      const delay = r.word.startMs
      const timing: KeyframeAnimationOptions = { duration: dur, delay, fill: 'both', easing: 'linear' }
      const clip = r.el.children[1] as HTMLElement
      const fill = clip.firstElementChild as HTMLElement
      r.anims = [
        clip.animate([{ transform: 'translateX(-101%)' }, { transform: 'translateX(0)' }], timing),
        fill.animate([{ transform: 'translateX(101%)' }, { transform: 'translateX(0)' }], timing),
      ]
      if (!this.lite && !r.el.classList.contains('backing')) {
        r.anims.push(
          r.el.animate(
            [{ transform: 'scale(1)' }, { transform: 'scale(1.015)', offset: 0.3 }, { transform: 'scale(1)' }],
            { duration: Math.max(dur, 240), delay, fill: 'none', easing: 'ease-out' },
          ),
        )
      }
      for (const a of r.anims) {
        a.currentTime = posMs
        if (!playing) a.pause()
      }
    }
  }

  /** Positions everything for a time. Called on seek, pause and new lines. */
  sync(posRawMs: number, playing: boolean) {
    if (!this.doc || this.doc.lines.length === 0) return
    const posMs = posRawMs - this.offset()
    const idx = lineIndexAt(this.doc.lines, posMs)
    this.show(idx, posMs, playing, true)
  }

  private show(idx: number, posMs: number, playing: boolean, force: boolean) {
    if (idx === this.current && !force) return
    const lines = this.doc!.lines
    this.current = idx
    // Lines alternate between the two rows, so the next line is always ready.
    const a = this.slots[idx % 2]
    const b = this.slots[(idx + 1) % 2]
    if (a.index !== idx) this.build(a, idx)
    if (idx + 1 < lines.length) {
      if (b.index !== idx + 1) this.build(b, idx + 1)
    } else if (b.index !== -1) {
      this.clearSlot(b)
    }
    a.root.classList.add('show', 'active')
    a.root.classList.remove('done')
    b.root.classList.remove('active', 'done')
    b.root.classList.toggle('show', idx + 1 < lines.length)
    this.animate(a, posMs, playing)
    if (b.line) this.animate(b, posMs, playing)
  }

  /** Lightweight per-frame check, which does no layout unless the line changes. */
  tick(posRawMs: number, playing: boolean) {
    if (!this.doc || this.doc.lines.length === 0) return
    const posMs = posRawMs - this.offset()
    let lines = this.doc.lines
    let idx = lineIndexAt(lines, posMs)
    if (this.pending && idx !== this.current) {
      const doc = this.pending
      this.setDoc(doc, this.accent)
      this.pending = null
      lines = doc.lines
      idx = lineIndexAt(lines, posMs)
    }
    if (idx !== this.current) this.show(idx, posMs, playing, false)
    const cur = lines[this.current]
    const next = lines[this.current + 1]
    const first = lines[0]
    const before = posMs < first.startMs ? first.startMs - posMs : -1
    const gap = next && posMs > cur.endMs ? next.startMs - posMs : -1
    const countdown = (before > 0 && before < 3000) || (gap > 0 && gap < 3000 && next!.startMs - cur.endMs > 4000)
    this.dots.classList.toggle('show', this.opts.countdown && countdown)
    if (countdown) {
      const remaining = before > 0 ? before : gap
      this.dots.dataset.n = String(Math.ceil(remaining / 1000))
    }
    const a = this.slots[this.current % 2]
    if (posMs > cur.endMs + 300 && !a.root.classList.contains('done')) a.root.classList.add('done')
  }

  setPlaying(posRawMs: number, playing: boolean) {
    this.sync(posRawMs, playing)
  }

  /** Time until the next sung line, used by the music-break label. */
  breakInfo(posRawMs: number): { remainingMs: number } | null {
    if (!this.doc || this.doc.lines.length === 0) return null
    const posMs = posRawMs - this.offset()
    const lines = this.doc.lines
    const first = lines[0]
    if (posMs < first.startMs) {
      return first.startMs >= 8000 && first.startMs - posMs > 3000 ? { remainingMs: first.startMs - posMs } : null
    }
    const i = lineIndexAt(lines, posMs)
    const next = lines[i + 1]
    if (!next) return null
    const gap = next.startMs - lines[i].endMs
    if (gap < 8000 || posMs < lines[i].endMs || next.startMs - posMs <= 3000) return null
    return { remainingMs: next.startMs - posMs }
  }

  destroy() {
    this.cancelAll()
    this.host.textContent = ''
  }

  /** Holds new timing until the next line, so the current line isn't redrawn. */
  queueDoc(doc: LyricsDoc) {
    if (!this.doc || this.doc.lines.length === 0) this.setDoc(doc, this.accent)
    else this.pending = doc
  }
}
