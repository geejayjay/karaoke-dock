import type { LyricLine } from '../shared/types'
import { VOICE_COLORS } from './lyrics-engine'

export interface Segment {
  from: number // 0..1
  to: number
  color: string | null
}

/** Merges lyric lines into sung stretches, split where the part changes. */
export function buildSegments(lines: LyricLine[], durationMs: number, offsetMs = 0): Segment[] {
  if (durationMs <= 0) return []
  const out: Segment[] = []
  for (const l of lines) {
    const from = Math.max(0, (l.startMs + offsetMs) / durationMs)
    const to = Math.min(1, (l.endMs + offsetMs) / durationMs)
    if (to <= from) continue
    const color = l.voice ? (VOICE_COLORS[l.voice] ?? null) : null
    const last = out[out.length - 1]
    if (last && last.color === color && (from - last.to) * durationMs < 4000) last.to = to
    else out.push({ from, to, color })
  }
  return out
}

const MAX_DOTS = 12

/**
 * How tall each column is, from 0 to 1. Uses the song's loudness, spread over
 * its own quiet and loud so the shape is easy to read. Before the analysis
 * exists, sung stretches stand a little taller.
 */
export function columnHeights(columns: number, levels: Uint8Array | null, segments: Segment[]): number[] {
  if (!levels || levels.length === 0) {
    return Array.from({ length: columns }, (_, c) => {
      const at = (c + 0.5) / columns
      return segments.some((s) => at >= s.from && at <= s.to) ? 0.3 : 0.08
    })
  }
  const raw = Array.from({ length: columns }, (_, c) => {
    const from = Math.floor((c / columns) * levels.length)
    const to = Math.max(Math.floor(((c + 1) / columns) * levels.length), from + 1)
    let sum = 0
    for (let i = from; i < to; i++) sum += levels[Math.min(i, levels.length - 1)]
    return sum / (to - from)
  })
  const low = Math.min(...raw)
  const span = Math.max(...raw) - low
  return raw.map((v) => (span > 0 ? ((v - low) / span) ** 1.3 : 0.3))
}

/**
 * The styled song bar. The song's shape is drawn once, then only transforms
 * change, so it costs no repaints during a song.
 */
export class ProgressBar {
  private shape: HTMLCanvasElement
  private fillClip: HTMLElement
  private fillInner: HTMLElement
  private fillShape: HTMLCanvasElement
  private tip: HTMLElement
  private width = 0
  private anims: Animation[] = []

  constructor(host: HTMLElement) {
    host.classList.add('progress')
    this.shape = document.createElement('canvas')
    this.fillClip = document.createElement('div')
    this.fillClip.className = 'progress-fill'
    this.fillInner = document.createElement('div')
    this.fillInner.className = 'progress-fill-inner'
    this.fillShape = document.createElement('canvas')
    this.tip = document.createElement('div')
    this.tip.className = 'progress-tip'
    this.fillInner.appendChild(this.fillShape)
    this.fillClip.appendChild(this.fillInner)
    host.append(this.shape, this.fillClip, this.tip)
    this.host = host
  }

  private host: HTMLElement

  /** Draws the song's shape once, as columns of tiny dots: more dots where it's louder. */
  draw(segments: Segment[], accent: string, levels: Uint8Array | null = null) {
    this.width = this.host.clientWidth
    const h = this.host.clientHeight
    const dpr = Math.min(window.devicePixelRatio || 1, 2)
    const step = Math.max(h / MAX_DOTS, 2)
    const radius = step * 0.3
    const columns = Math.max(Math.floor(this.width / step), 1)
    const heights = columnHeights(columns, levels, segments)
    const paint = (c: HTMLCanvasElement, alpha: number, dimAlpha: number) => {
      c.width = this.width * dpr
      c.height = h * dpr
      c.style.width = `${this.width}px`
      c.style.height = `${h}px`
      const g = c.getContext('2d')!
      g.scale(dpr, dpr)
      for (let col = 0; col < columns; col++) {
        const at = (col + 0.5) / columns
        const seg = segments.find((s) => at >= s.from && at <= s.to)
        const sung = !!seg || segments.length === 0
        const dots = Math.max(1, Math.round(heights[col] * MAX_DOTS))
        g.globalAlpha = sung ? alpha : dimAlpha
        g.fillStyle = sung ? (seg?.color ?? accent) : '#fff'
        g.beginPath()
        for (let d = 0; d < dots; d++) {
          const x = (col + 0.5) * step
          const y = h - (d + 0.5) * step
          g.moveTo(x + radius, y)
          g.arc(x, y, radius, 0, Math.PI * 2)
        }
        g.fill()
      }
    }
    paint(this.shape, 0.32, 0.22)
    paint(this.fillShape, 1, 0.7)
    this.host.style.setProperty('--bar-accent', accent)
  }

  /** Starts the single long animation for the whole song. */
  run(positionMs: number, durationMs: number, playing: boolean) {
    this.anims.forEach((a) => a.cancel())
    if (durationMs <= 0 || this.width === 0) return
    const timing: KeyframeAnimationOptions = { duration: durationMs, fill: 'both', easing: 'linear' }
    const w = this.width
    this.anims = [
      this.fillClip.animate([{ transform: `translateX(${-w}px)` }, { transform: 'translateX(0)' }], timing),
      this.fillInner.animate([{ transform: `translateX(${w}px)` }, { transform: 'translateX(0)' }], timing),
      this.tip.animate([{ transform: 'translateX(0)' }, { transform: `translateX(${w}px)` }], timing),
    ]
    for (const a of this.anims) {
      a.currentTime = positionMs
      if (!playing) a.pause()
    }
  }

  /** Nudges the bar back to the audio clock. */
  resync(positionMs: number) {
    for (const a of this.anims) {
      if (Math.abs(Number(a.currentTime ?? 0) - positionMs) > 120) a.currentTime = positionMs
    }
  }

  clear() {
    this.anims.forEach((a) => a.cancel())
    this.anims = []
  }
}
