<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'

const props = defineProps<{ stacked?: boolean; still?: boolean }>()

const root = ref<HTMLElement>()
const canvas = ref<HTMLCanvasElement>()
const WORD = 'Dock'
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

onMounted(() => {
  const el = canvas.value!
  const style = getComputedStyle(root.value!)
  const size = parseFloat(style.fontSize)
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
  g.fillText(WORD, 0, (height - (ascent + descent)) / 2 + ascent)
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
})

onBeforeUnmount(() => cancelAnimationFrame(raf))
</script>

<template>
  <div ref="root" class="brand" :class="{ stacked }" role="img" aria-label="KaraokeDock">
    <span>Karaoke</span>
    <canvas ref="canvas" />
  </div>
</template>

<style>
.brand { display: flex; align-items: center; font-weight: 900; line-height: 1.1; letter-spacing: -.03em; }
.brand canvas { display: block; }
.brand.stacked { flex-direction: column; align-items: flex-start; }
</style>
