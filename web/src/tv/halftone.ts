/**
 * The song background: a field of halftone dots driven by a saved analysis of
 * the song and coloured by its album. Nothing is analysed here, so the only cost
 * is drawing. Motion is integrated over time and steered by the song, so it
 * never loops, and each song and section picks its own patterns.
 */

export interface VisualData {
  version: number
  rate: number
  bass: string
  mid: string
  high: string
  level: string
  beats: number[]
  sections: { startMs: number; energy: number }[]
}

export interface Palette {
  background: string
  colors: string[]
}

type RGB = [number, number, number]

const DEFAULT_PALETTE: Palette = { background: '#0e0a1c', colors: ['#ff4d8d', '#7c5cff', '#38d6ff'] }
const PATTERNS = 5
const FPS = 30
const LITE_FPS = 15
const MAX_WIDTH = 960
const LITE_COLUMNS = 48
const BLEND_SECONDS = 1.6
const SONG_CALM = 0.88 // how far the dots fade behind the lyrics
const IDLE_CALM = 0.2
const IDLE_STRENGTH = 1.6

const VERTEX = `
attribute vec2 aPos;
void main() { gl_Position = vec4(aPos, 0.0, 1.0); }`

const FRAGMENT = `
#ifdef GL_FRAGMENT_PRECISION_HIGH
precision highp float;
#else
precision mediump float;
#endif
uniform vec2 uRes, uCenter;
uniform float uPhase, uLevel, uBass, uMid, uHigh, uPulse, uRing, uMix, uPatA, uPatB, uAngle, uCell, uSeed, uCalm, uStrength;
uniform vec3 uC0, uC1, uC2, uBg;

float hash(vec2 p) { return fract(sin(dot(p, vec2(127.1, 311.7)) + uSeed) * 43758.5453); }

float noise(vec2 p) {
  vec2 i = floor(p), f = fract(p);
  f = f * f * (3.0 - 2.0 * f);
  return mix(mix(hash(i), hash(i + vec2(1.0, 0.0)), f.x), mix(hash(i + vec2(0.0, 1.0)), hash(i + vec2(1.0, 1.0)), f.x), f.y);
}

float pattern(float k, vec2 p, float r) {
  if (k < 0.5) return 0.5 + 0.5 * sin(p.x * 5.0 + p.y * 2.0 + uPhase * 2.0);
  if (k < 1.5) return 0.5 + 0.5 * sin(r * 11.0 - uPhase * 3.0);
  if (k < 2.5) return 0.5 + 0.5 * sin((p.x + p.y) * 8.0 + sin(p.y * 3.0 + uPhase) * 2.0 + uPhase * 2.5);
  if (k < 3.5) return noise(p * 2.6 + vec2(uPhase * 0.5, -uPhase * 0.3));
  return 0.5 + 0.5 * sin(atan(p.y, p.x) * 5.0 + r * 6.0 - uPhase * 2.0);
}

void main() {
  vec2 uv = (gl_FragCoord.xy - 0.5 * uRes) / uRes.y;
  float c = cos(uAngle), s = sin(uAngle);
  vec2 q = mat2(c, -s, s, c) * uv;
  vec2 id = floor(q / uCell);
  vec2 center = (id + 0.5) * uCell;
  vec2 w = mat2(c, s, -s, c) * center;
  vec2 p = w - uCenter;
  float r = length(p);
  float v = mix(pattern(uPatA, p, r), pattern(uPatB, p, r), uMix);
  float n = noise(w * 3.5 + vec2(-uPhase * 0.4, uPhase * 0.25));
  v = v * 0.62 + n * 0.38;
  float ring = exp(-pow((r - uRing) * 7.0, 2.0)) * uPulse;
  float amount = clamp(v * (0.35 + uLevel * 0.9) + ring * 0.55 + uBass * 0.22 - 0.18 * (1.0 - uLevel), 0.0, 1.0);
  float sparkle = step(1.0 - uHigh * 0.16, hash(id + floor(uPhase * 9.0)));
  float radius = uCell * (0.5 * amount + 0.08 * sparkle);
  float edge = 1.5 / uRes.y;
  float d = 1.0 - smoothstep(radius - edge, radius + edge, length(q - center));
  vec3 col = mix(uC0, uC1, clamp(v * 1.2 + 0.1 * sin(uPhase), 0.0, 1.0));
  col = mix(col, uC2, clamp(n * uMid * 1.4 + ring, 0.0, 1.0));
  // The dots fade through the middle of the screen, so the lyrics stay clear.
  float calm = 1.0 - uCalm * exp(-pow(uv.y * 2.2, 2.0));
  gl_FragColor = vec4(mix(uBg * (1.0 + uLevel * 0.35), col, clamp(d * calm * (0.3 + amount * 0.3) * uStrength, 0.0, 1.0)), 1.0);
}`

function hexToRgb(hex: string): RGB {
  const n = parseInt(hex.replace('#', ''), 16)
  return [((n >> 16) & 255) / 255, ((n >> 8) & 255) / 255, (n & 255) / 255]
}

export function decode(b64: string): Uint8Array {
  const text = atob(b64)
  const out = new Uint8Array(text.length)
  for (let i = 0; i < text.length; i++) out[i] = text.charCodeAt(i)
  return out
}

function hashText(s: string): number {
  let h = 2166136261
  for (let i = 0; i < s.length; i++) h = Math.imul(h ^ s.charCodeAt(i), 16777619)
  return h >>> 0
}

/** The same seed always gives the same numbers. */
function randomFrom(seed: number): () => number {
  let a = seed
  return () => {
    a = (a + 0x6d2b79f5) | 0
    let t = Math.imul(a ^ (a >>> 15), 1 | a)
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

interface Bands {
  rate: number
  bass: Uint8Array
  mid: Uint8Array
  high: Uint8Array
  level: Uint8Array
  beats: number[]
  sections: { startMs: number; energy: number }[]
}

interface Look {
  pattern: number
  angle: number
  cell: number
  cx: number
  cy: number
}

type Painter = (s: Halftone['frame']) => void

export class Halftone {
  private canvas: HTMLCanvasElement | null = null
  private paint: Painter | null = null
  private bands: Bands | null = null
  private seed = 1
  private palette = DEFAULT_PALETTE
  private rgb = DEFAULT_PALETTE.colors.map(hexToRgb)
  private bg = hexToRgb(DEFAULT_PALETTE.background)
  private lastDraw = 0
  private lastPos = 0
  private nextBeat = 0
  private section = -1
  private from: Look
  private to: Look

  readonly frame = {
    phase: 0, level: 0.3, bass: 0, mid: 0, high: 0, pulse: 0, ring: 2, mix: 1,
    patA: 0, patB: 0, angle: 0.4, cell: 0.03, cx: 0, cy: 0, seed: 1, calm: SONG_CALM, strength: 1,
  }

  constructor(
    private host: HTMLElement,
    private lite = false,
  ) {
    this.from = this.lookFor(0)
    this.to = this.from
    this.mount()
  }

  private lookFor(index: number): Look {
    const r = randomFrom(this.seed + index * 7919)
    return {
      pattern: Math.floor(r() * PATTERNS),
      angle: r() * Math.PI,
      cell: 0.024 + r() * 0.016,
      cx: (r() - 0.5) * 0.8,
      cy: (r() - 0.5) * 0.5,
    }
  }

  setLite(on: boolean) {
    if (on === this.lite) return
    this.lite = on
    this.mount()
  }

  private mount() {
    this.canvas?.remove()
    const canvas = document.createElement('canvas')
    canvas.style.cssText = 'position:absolute;inset:0;width:100%;height:100%;display:block'
    this.host.appendChild(canvas)
    this.canvas = canvas
    this.paint = (!this.lite && this.webgl(canvas)) || this.simple(canvas)
  }

  /** Starts a song. With no analysis the dots drift gently by themselves. */
  setSong(id: string, data: VisualData | null, palette: Palette | null, idle = false) {
    this.frame.calm = idle ? IDLE_CALM : SONG_CALM
    this.frame.strength = idle ? IDLE_STRENGTH : 1
    this.seed = hashText(id)
    this.frame.seed = (this.seed % 1000) / 10
    this.bands = data && data.level
      ? { rate: data.rate, bass: decode(data.bass), mid: decode(data.mid), high: decode(data.high), level: decode(data.level), beats: data.beats, sections: data.sections }
      : null
    this.palette = palette?.colors?.length ? palette : DEFAULT_PALETTE
    this.rgb = this.palette.colors.map(hexToRgb)
    this.bg = hexToRgb(this.palette.background)
    this.section = -1
    this.nextBeat = 0
    this.lastPos = 0
    this.from = this.lookFor(0)
    this.to = this.from
    this.frame.mix = 1
  }

  /** Called every animation frame, and draws at its own lower rate. */
  update(posMs: number, playing: boolean, now: number) {
    if (now - this.lastDraw < 1000 / (this.lite ? LITE_FPS : FPS) - 2 || !this.canvas || !this.host.clientWidth) return
    const dt = Math.min((now - this.lastDraw) / 1000, 0.1)
    this.lastDraw = now
    this.follow(posMs, playing, dt, now)
    this.paint?.(this.frame)
  }

  private read(band: Uint8Array, at: number): number {
    const i = Math.floor(at)
    if (i < 0 || i >= band.length) return 0
    const next = i + 1 < band.length ? band[i + 1] : band[i]
    return (band[i] + (next - band[i]) * (at - i)) / 255
  }

  private follow(pos: number, playing: boolean, dt: number, now: number) {
    const f = this.frame
    const b = this.bands
    let level: number, bass: number, mid: number, high: number
    if (b && playing && pos > 0) {
      const at = (pos / 1000) * b.rate
      level = this.read(b.level, at)
      bass = this.read(b.bass, at)
      mid = this.read(b.mid, at)
      high = this.read(b.high, at)
      this.followBeats(b, pos, dt)
      this.followSections(b, pos)
    } else {
      const t = now / 1000
      level = playing ? 0.32 + 0.1 * Math.sin(t * 0.7) : 0.2
      bass = playing ? 0.12 + 0.08 * Math.sin(t * 1.3) : 0
      mid = 0.2
      high = 0
      if (playing) this.driftOnItsOwn(t)
    }
    // Quick to rise and slower to fall, like a level meter.
    const ease = (from: number, to: number) => from + (to - from) * (to > from ? 0.55 : 0.14)
    f.level = ease(f.level, level)
    f.bass = ease(f.bass, bass)
    f.mid = ease(f.mid, mid)
    f.high = ease(f.high, high)
    f.phase += dt * (playing ? 0.18 + f.level * 1.05 : 0.04)
    f.pulse *= Math.exp(-dt * 5)
    f.ring += dt * 1.25
    f.mix = Math.min(f.mix + dt / BLEND_SECONDS, 1)
    const blend = f.mix * f.mix * (3 - 2 * f.mix)
    f.patA = this.from.pattern
    f.patB = this.to.pattern
    f.angle = this.from.angle + (this.to.angle - this.from.angle) * blend + f.phase * 0.012
    f.cell = this.from.cell + (this.to.cell - this.from.cell) * blend
    f.cx = this.from.cx + (this.to.cx - this.from.cx) * blend
    f.cy = this.from.cy + (this.to.cy - this.from.cy) * blend
  }

  private followBeats(b: Bands, pos: number, dt: number) {
    if (Math.abs(pos - this.lastPos) > Math.max(dt * 2000, 500)) {
      let i = 0
      while (i < b.beats.length && b.beats[i] <= pos) i++
      this.nextBeat = i
    }
    this.lastPos = pos
    let hit = false
    while (this.nextBeat < b.beats.length && b.beats[this.nextBeat] <= pos) {
      this.nextBeat++
      hit = true
    }
    if (hit) {
      this.frame.pulse = 1
      this.frame.ring = 0
    }
  }

  private followSections(b: Bands, pos: number) {
    let i = 0
    while (i + 1 < b.sections.length && b.sections[i + 1].startMs <= pos) i++
    if (i !== this.section) this.changeLook(i, this.section === -1)
  }

  private driftOnItsOwn(t: number) {
    const i = Math.floor(t / 20)
    if (i !== this.section) this.changeLook(i, this.section === -1)
  }

  private changeLook(index: number, immediately: boolean) {
    const previous = this.to
    let next = this.lookFor(index)
    for (let tries = 1; next.pattern === previous.pattern && !immediately && tries < 6; tries++) next = this.lookFor(index + tries * 31)
    this.section = index
    this.from = immediately ? next : previous
    this.to = next
    this.frame.mix = immediately ? 1 : 0
  }

  private size(canvas: HTMLCanvasElement, wide: number): [number, number] {
    const w = Math.min(this.host.clientWidth, wide)
    const h = Math.round((w * this.host.clientHeight) / this.host.clientWidth)
    if (canvas.width !== w || canvas.height !== h) {
      canvas.width = w
      canvas.height = h
    }
    return [w, h]
  }

  private webgl(canvas: HTMLCanvasElement): Painter | null {
    const gl = canvas.getContext('webgl', { antialias: false, alpha: false, depth: false, stencil: false, powerPreference: 'low-power' })
    if (!gl) return null
    const shader = (type: number, source: string) => {
      const s = gl.createShader(type)!
      gl.shaderSource(s, source)
      gl.compileShader(s)
      return gl.getShaderParameter(s, gl.COMPILE_STATUS) ? s : null
    }
    const vs = shader(gl.VERTEX_SHADER, VERTEX)
    const fs = shader(gl.FRAGMENT_SHADER, FRAGMENT)
    if (!vs || !fs) return null
    const program = gl.createProgram()!
    gl.attachShader(program, vs)
    gl.attachShader(program, fs)
    gl.linkProgram(program)
    if (!gl.getProgramParameter(program, gl.LINK_STATUS)) return null
    gl.useProgram(program)
    gl.bindBuffer(gl.ARRAY_BUFFER, gl.createBuffer())
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 3, -1, -1, 3]), gl.STATIC_DRAW)
    const pos = gl.getAttribLocation(program, 'aPos')
    gl.enableVertexAttribArray(pos)
    gl.vertexAttribPointer(pos, 2, gl.FLOAT, false, 0, 0)
    const u = (name: string) => gl.getUniformLocation(program, name)
    const names = ['uPhase', 'uLevel', 'uBass', 'uMid', 'uHigh', 'uPulse', 'uRing', 'uMix', 'uPatA', 'uPatB', 'uAngle', 'uCell', 'uSeed', 'uCalm', 'uStrength'] as const
    const floats = names.map(u)
    const [res, center, c0, c1, c2, bg] = ['uRes', 'uCenter', 'uC0', 'uC1', 'uC2', 'uBg'].map(u)
    return (f) => {
      const [w, h] = this.size(canvas, MAX_WIDTH)
      gl.viewport(0, 0, w, h)
      gl.uniform2f(res, w, h)
      gl.uniform2f(center, f.cx, f.cy)
      const values = [f.phase, f.level, f.bass, f.mid, f.high, f.pulse, f.ring, f.mix, f.patA, f.patB, f.angle, f.cell, f.seed, f.calm, f.strength]
      floats.forEach((loc, i) => gl.uniform1f(loc, values[i]))
      gl.uniform3fv(c0, this.rgb[0])
      gl.uniform3fv(c1, this.rgb[1] ?? this.rgb[0])
      gl.uniform3fv(c2, this.rgb[2] ?? this.rgb[0])
      gl.uniform3fv(bg, this.bg)
      gl.drawArrays(gl.TRIANGLES, 0, 3)
    }
  }

  /** A small dot grid for TVs that can't run the shader. */
  private simple(canvas: HTMLCanvasElement): Painter {
    const g = canvas.getContext('2d')!
    return (f) => {
      const [w, h] = this.size(canvas, 640)
      g.fillStyle = this.palette.background
      g.fillRect(0, 0, w, h)
      const step = w / LITE_COLUMNS
      const rows = Math.ceil(h / step)
      const colors = this.palette.colors
      g.globalAlpha = Math.min(0.55 * f.strength, 1)
      for (let group = 0; group < colors.length; group++) {
        g.fillStyle = colors[group]
        g.beginPath()
        for (let y = 0; y <= rows; y++) {
          for (let x = 0; x <= LITE_COLUMNS; x++) {
            if ((x + y) % colors.length !== group) continue
            const u = x / LITE_COLUMNS - 0.5
            const v = y / rows - 0.5
            const wave = 0.5 + 0.5 * Math.sin(u * 9 + v * 4 + f.phase * 2 + Math.sin(v * 5 + f.phase) * 1.5)
            const calm = 1 - f.calm * Math.exp(-((v * 2.2) ** 2))
            const amount = Math.min(wave * (0.3 + f.level * 0.9) + f.bass * 0.2 + f.pulse * 0.15, 1) * calm
            const r = step * 0.42 * amount
            if (r < 0.4) continue
            const cx = x * step
            const cy = y * step
            g.moveTo(cx + r, cy)
            g.arc(cx, cy, r, 0, Math.PI * 2)
          }
        }
        g.fill()
      }
      g.globalAlpha = 1
    }
  }

  destroy() {
    this.canvas?.remove()
    this.canvas = null
    this.paint = null
  }
}

/** Fetches what the background needs. Either part may be missing. */
export async function loadVisual(songId: string, coverId: string | undefined, useAnalysis: boolean): Promise<[VisualData | null, Palette | null]> {
  const get = async <T,>(url: string): Promise<T | null> => {
    const res = await fetch(url).catch(() => null)
    return res?.ok ? ((await res.json().catch(() => null)) as T | null) : null
  }
  return Promise.all([
    useAnalysis ? get<VisualData>(`/media/${songId}/visual`) : Promise.resolve(null),
    coverId ? get<Palette>(`/covers/${coverId}/palette`) : Promise.resolve(null),
  ])
}
