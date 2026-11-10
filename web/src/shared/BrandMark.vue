<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import logo from './logo-right.svg' // from assets/make_logo.py, leaning right
import '@fontsource/herr-von-muellerhoff/latin-400.css' // bundled, so it works offline
import fallbackOutlines from '@fontsource/herr-von-muellerhoff/files/herr-von-muellerhoff-latin-400-normal.woff?url'

const props = defineProps<{ stacked?: boolean; still?: boolean }>()

const words = ref<HTMLElement>()
const canvas = ref<HTMLCanvasElement>()
const script = ref<HTMLElement>()
const trace = ref<SVGGElement>()
const SCRIPT_WORD = 'dock'
const TRACE_DRAW_MS = 16000 // one pass of a light round every letter
const TRACE_REST_MS = 2000 // pause before a light starts again
const TRACE_LIGHTS = 2 // lights following each other, evenly spaced along the pass
let traceAnimations: Animation[] = []

type Outlines = { getPath(text: string, x: number, y: number, size: number): { toPathData(decimals: number): string } }
let outlines: Promise<Outlines | null> | undefined

/** Reads the letter shapes of the font "dock" is shown in: the optional one, else the built-in one. */
function loadOutlines() {
  return (outlines ??= (async () => {
    const { parse } = await import('opentype.js')
    for (const url of ['/fonts/script', fallbackOutlines]) {
      try {
        const res = await fetch(url)
        if (res.ok) return parse(await res.arrayBuffer()) as Outlines
      } catch {
        // try the next one
      }
    }
    return null
  })())
}

/**
 * Sends soft lights along the edges of "dock", as if a pen were writing it.
 * Each outline gets its turn, left to right, for a time that matches its
 * length, so a light keeps an even speed. The lights follow each other, spaced
 * evenly through the pass, so they never meet.
 */
async function startTrace(baseline: number, px: number) {
  const group = trace.value
  if (!group || props.still || matchMedia('(prefers-reduced-motion: reduce)').matches) return
  const font = await loadOutlines()
  if (!font || !trace.value) return
  stopTrace()
  const svg = 'http://www.w3.org/2000/svg'
  const shapes = font.getPath(SCRIPT_WORD, 0, baseline, px).toPathData(2).split(/(?=M)/).filter(Boolean)
  const paths = shapes.map((d) => {
    const el = document.createElementNS(svg, 'path')
    el.setAttribute('d', d)
    group.appendChild(el)
    return { el, length: el.getTotalLength(), left: el.getBBox().x }
  })
  const visible = paths.filter((p) => p.length > px * 0.05).sort((a, b) => a.left - b.left)
  const total = visible.reduce((sum, p) => sum + p.length, 0)
  if (!total) return
  const cycle = TRACE_DRAW_MS + TRACE_REST_MS
  const light = px * 0.35
  let done = 0
  for (const { el, length } of visible) {
    const from = ((done / total) * TRACE_DRAW_MS) / cycle
    done += length
    const to = ((done / total) * TRACE_DRAW_MS) / cycle
    const keyframes = [
      { strokeDashoffset: light, offset: 0 },
      { strokeDashoffset: light, offset: from },
      { strokeDashoffset: -length, offset: to },
      { strokeDashoffset: -length, offset: 1 },
    ]
    for (let n = 0; n < TRACE_LIGHTS; n++) {
      // Each light is its own copy of the shape, so each shows as one dash.
      const copy = n === 0 ? el : (el.cloneNode() as SVGPathElement)
      if (n > 0) group.appendChild(copy)
      copy.style.strokeDasharray = `${light} ${length + light}`
      copy.style.strokeDashoffset = `${light}`
      const behind = (TRACE_DRAW_MS / TRACE_LIGHTS) * n
      traceAnimations.push(copy.animate(keyframes, { duration: cycle, iterations: Infinity, easing: 'linear', delay: behind ? -(cycle - behind) : 0 }))
    }
  }
}

function stopTrace() {
  traceAnimations.forEach((a) => a.cancel())
  traceAnimations = []
  trace.value?.replaceChildren()
}
const WORD = 'Karaoke'
const FPS = 24
let raf = 0

/** Finds the dot positions that fall inside the letters, on a staggered grid. */
function dotsInside(mask: ImageData, cell: number): [number, number][] {
  const dots: [number, number][] = []
  const rowStep = cell * 0.87
  for (let row = 0; row * rowStep < mask.height; row++) {
    const y = row * rowStep
    for (let x = row % 2 ? cell / 2 : 0; x < mask.width; x += cell) {
      const alpha = mask.data[(Math.floor(y) * mask.width + Math.floor(x)) * 4 + 3]
      if (alpha > 128) dots.push([x, y])
    }
  }
  return dots
}

/**
 * Sets the bottom of the script word's round letters ("o" and "c") level with
 * the bottom of the drawn word's letters. Strokes like the tail of "k" can then
 * dip below, as handwriting does. `gap` is how far that sits above the bottom of the
 * drawing, in CSS pixels.
 */
async function alignScript(gap: number) {
  const el = script.value
  if (!el) return
  const style = getComputedStyle(el)
  const px = parseFloat(style.fontSize)
  const font = `400 ${px}px ${style.fontFamily}`
  await document.fonts?.load(font, 'dock').catch(() => {})
  const g = document.createElement('canvas').getContext('2d')
  if (!g) return
  g.font = font
  const m = g.measureText('dock')
  const ascent = m.fontBoundingBoxAscent ?? px * 0.8
  const descent = m.fontBoundingBoxDescent ?? px * 0.2
  // Script fonts reserve a lot of room above and below, so the line stays one
  // font size tall, with the font's room shared evenly above and below it.
  const baselineFromBottom = (px - (ascent + descent)) / 2 + descent
  // Measured from the ink of the round letters, which can sit off the baseline.
  const round = g.measureText('oc')
  const inkFromBottom = baselineFromBottom - (round.actualBoundingBoxDescent ?? 0)
  void startTrace(px - baselineFromBottom, px)
  if (!props.stacked) el.style.marginBottom = `${gap - inkFromBottom}px`
}

function setup() {
  const el = canvas.value!
  const style = getComputedStyle(words.value!)
  const size = parseFloat(style.fontSize)
  // A page opened before its window has a size has no text size yet.
  if (!(size > 0)) {
    raf = requestAnimationFrame(setup)
    return
  }
  const dpr = Math.min(window.devicePixelRatio || 1, 2)
  const font = `900 ${size * dpr}px ${style.fontFamily}`
  const g = el.getContext('2d', { willReadFrequently: true })
  if (!g) return
  g.font = font
  const metrics = g.measureText(WORD)
  const width = Math.ceil(metrics.width + size * dpr * 0.06)
  const height = Math.ceil(size * dpr * parseFloat(style.lineHeight === 'normal' ? '1.2' : String(parseFloat(style.lineHeight) / size)))
  el.width = width
  el.height = height
  el.style.width = `${width / dpr}px`
  el.style.height = `${height / dpr}px`
  // Lines the letters up with the word beside them.
  const ascent = metrics.fontBoundingBoxAscent ?? size * dpr * 0.8
  const descent = metrics.fontBoundingBoxDescent ?? size * dpr * 0.2
  g.font = font
  g.textBaseline = 'alphabetic'
  const baseline = (height - (ascent + descent)) / 2 + ascent
  g.fillText(WORD, 0, baseline)
  const inkBottom = baseline + (metrics.actualBoundingBoxDescent ?? 0)
  void alignScript((height - inkBottom) / dpr)
  // A small logo needs relatively bigger dots to stay readable.
  const cell = Math.max(Math.round((size * dpr) / (size < 60 ? 14 : 34)), 2)
  const dots = dotsInside(g.getImageData(0, 0, width, height), cell)
  const fill = g.createLinearGradient(0, 0, width, height)
  fill.addColorStop(0, '#ff4d8d')
  fill.addColorStop(1, '#8f6bff')

  const draw = (t: number) => {
    g.clearRect(0, 0, width, height)
    g.fillStyle = fill
    g.beginPath()
    for (const [x, y] of dots) {
      const wave = Math.sin(x * 0.018 + y * 0.03 - t * 1.6) * 0.5 + Math.sin(x * 0.007 - y * 0.012 + t * 0.7) * 0.5
      const r = cell * (0.46 + 0.13 * wave)
      g.moveTo(x + r, y)
      g.arc(x, y, r, 0, Math.PI * 2)
    }
    g.fill()
  }
  draw(0)
  if (props.still) return
  let last = 0
  const loop = (ts: number) => {
    raf = requestAnimationFrame(loop)
    if (ts - last < 1000 / FPS) return
    last = ts
    draw(ts / 1000)
  }
  raf = requestAnimationFrame(loop)
}

onMounted(setup)

onBeforeUnmount(() => {
  cancelAnimationFrame(raf)
  stopTrace()
})
</script>

<template>
  <div class="brand" :class="{ stacked }" role="img" aria-label="KaraokeDock">
    <div ref="words" class="brand-words">
      <canvas ref="canvas" />
      <span ref="script" class="brand-script" :class="{ still }"
        >dock<svg class="brand-trace" aria-hidden="true"><g ref="trace" /></svg></span
      >
    </div>
    <img class="brand-logo" :src="logo" alt="" draggable="false" />
  </div>
</template>

<style>
/* An optional font from the server's data folder, with the built-in one as a fallback. */
@font-face { font-family: 'KD Script'; src: url('/fonts/script') format('opentype'); font-display: swap; }
.brand { display: flex; align-items: center; gap: .2em; font-weight: 900; line-height: 1.1; letter-spacing: -.03em; }
.brand-logo { display: block; flex-shrink: 0; height: 1.35em; width: auto; }
.brand-words { display: flex; align-items: flex-end; }
/* A quiet handwritten "dock": smaller and softer, so "Karaoke" stays the star.
   Its colour is a pale tint of the brand violet (#8f6bff). */
.brand-script {
  margin-left: .06em; padding-right: .08em;
  font-family: 'KD Script', 'Herr Von Muellerhoff', cursive; font-size: 1.1em; font-weight: 400; letter-spacing: 0; line-height: 1;
  color: rgba(203, 188, 255, .8);
  position: relative;
}
/* Soft lights writing along the edges of "dock", drawn by startTrace. */
.brand-trace { position: absolute; left: 0; top: 0; width: 100%; height: 100%; overflow: visible; pointer-events: none; }
.brand-trace path {
  fill: none; stroke: rgba(232, 225, 255, .65); stroke-width: .024em; stroke-linecap: round;
  filter: drop-shadow(0 0 .06em rgba(143, 107, 255, .7));
}
.brand-script.still .brand-trace { display: none; }
@media (prefers-reduced-motion: reduce) { .brand-trace { display: none; } }
.brand.stacked .brand-script { align-self: flex-end; margin: -.2em 0 0; font-size: .85em; }
.brand canvas { display: block; }
.brand.stacked .brand-words { flex-direction: column; align-items: flex-start; }
.brand.stacked .brand-logo { height: 2.2em; }
</style>
