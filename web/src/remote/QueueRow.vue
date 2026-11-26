<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import SongCard from './SongCard.vue'
import Icon from './Icon.vue'
import { queueDrag, shiftFor } from './queueDrag'
import type { QueueItem } from '../shared/types'

const props = defineProps<{ item: QueueItem; index: number; count: number; canRemove: boolean; canMove: boolean; rowHeight: number }>()
const emit = defineEmits<{ remove: []; moveTo: [index: number]; playNext: [] }>()

const SWIPE_START = 10 // px before a touch counts as a swipe
const SWIPE_DONE = 90 // px to remove or play next on release
const HOLD_MS = 350 // press and hold to pick a song up
const EDGE = 64 // px from the list's edge where it starts scrolling
const EDGE_SPEED = 9 // px per frame at the very edge

const root = ref<HTMLElement>()
const swipeX = ref(0)
const dragY = ref(0)
const swiping = ref(false)
const dragging = ref(false)
const pastSwipe = computed(() => Math.abs(swipeX.value) >= SWIPE_DONE)
let startX = 0
let startY = 0
let tracking = false
let holdTimer = 0
let scroller: HTMLElement | null = null
let startScroll = 0
let lastY = 0
let edgeFrame = 0

const shift = computed(() => (dragging.value ? 0 : shiftFor(props.index, props.item.id) * props.rowHeight))
const style = computed(() => ({
  transform: `translate3d(${swipeX.value}px, ${dragging.value ? dragY.value : shift.value}px, 0) scale(${dragging.value ? 1.03 : 1})`,
  transition: swiping.value || dragging.value ? 'none' : 'transform .2s cubic-bezier(.2,.8,.2,1)',
  touchAction: 'pan-y',
}))

function buzz(ms = 12) {
  navigator.vibrate?.(ms)
}

// ---- Swipe sideways: left removes, right plays next ----

function press(e: PointerEvent) {
  if (e.button > 0) return
  tracking = props.canRemove || props.canMove
  startX = e.clientX
  startY = e.clientY
  const el = e.currentTarget as HTMLElement
  // Holding still picks the song up, for the host.
  if (props.canMove) holdTimer = window.setTimeout(() => pickUp(e.pointerId, el, startY), HOLD_MS)
}

function pressMove(e: PointerEvent) {
  if (dragging.value) return carry(e)
  if (!tracking) return
  const dx = e.clientX - startX
  if (!swiping.value) {
    if (Math.abs(e.clientY - startY) > SWIPE_START || Math.abs(dx) > SWIPE_START) window.clearTimeout(holdTimer)
    if (Math.abs(e.clientY - startY) > SWIPE_START) tracking = false
    else if (Math.abs(dx) > SWIPE_START && (props.canRemove || dx > 0)) {
      swiping.value = true
      ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
    }
    return
  }
  const was = pastSwipe.value
  // Only the host can play a song next; others can only swipe left.
  swipeX.value = props.canMove ? (props.canRemove ? dx : Math.max(dx, 0)) : Math.min(dx, 0)
  if (pastSwipe.value !== was) buzz(8)
}

function release() {
  window.clearTimeout(holdTimer)
  if (dragging.value) return drop()
  tracking = false
  if (!swiping.value) return
  swiping.value = false
  if (swipeX.value <= -SWIPE_DONE) {
    swipeX.value = -(root.value?.clientWidth ?? 400)
    buzz()
    emit('remove')
    // If the server refuses, the row comes back.
    window.setTimeout(() => (swipeX.value = 0), 1500)
    return
  }
  if (swipeX.value >= SWIPE_DONE && props.canMove) {
    buzz()
    emit('playNext')
  }
  swipeX.value = 0
}

// ---- Drag up and down to reorder (host) ----

// While carrying, the page must not scroll under the finger.
function blockScroll(e: TouchEvent) {
  if (dragging.value && e.cancelable) e.preventDefault()
}

function pickUp(pointerId: number, el: HTMLElement, y: number) {
  tracking = false
  swiping.value = false
  swipeX.value = 0
  dragging.value = true
  startY = y
  lastY = y
  scroller = root.value?.closest('.overflow-y-auto') ?? null
  startScroll = scroller?.scrollTop ?? 0
  const row = root.value?.parentElement
  if (row) row.style.zIndex = '20' // a row sits under the rows after it
  queueDrag.id = props.item.id
  queueDrag.from = queueDrag.to = props.index
  try {
    el.setPointerCapture(pointerId)
  } catch {
    // the finger has already lifted
  }
  buzz(15)
  edgeFrame = requestAnimationFrame(edgeScroll)
}

function handleDown(e: PointerEvent) {
  pickUp(e.pointerId, e.currentTarget as HTMLElement, e.clientY)
}

function carry(e: PointerEvent) {
  if (!dragging.value) return
  lastY = e.clientY
  place()
}

function place() {
  const scrolled = (scroller?.scrollTop ?? 0) - startScroll
  dragY.value = lastY - startY + scrolled
  const to = Math.min(Math.max(props.index + Math.round(dragY.value / props.rowHeight), 0), props.count - 1)
  if (to !== queueDrag.to) {
    queueDrag.to = to
    buzz(5)
  }
}

/** Scrolls the list while a song is held near its top or bottom. */
function edgeScroll() {
  if (!dragging.value) return
  if (scroller) {
    const box = scroller.getBoundingClientRect()
    const up = lastY - box.top
    const down = box.bottom - lastY
    const step = up < EDGE ? -EDGE_SPEED * (1 - up / EDGE) : down < EDGE ? EDGE_SPEED * (1 - down / EDGE) : 0
    if (step) {
      scroller.scrollTop += step
      place()
    }
  }
  edgeFrame = requestAnimationFrame(edgeScroll)
}

function drop() {
  cancelAnimationFrame(edgeFrame)
  if (!dragging.value) return
  const to = queueDrag.to
  dragging.value = false
  dragY.value = 0
  const row = root.value?.parentElement
  if (row) row.style.zIndex = ''
  queueDrag.id = 0
  queueDrag.from = queueDrag.to = -1
  if (to >= 0 && to !== props.index) emit('moveTo', to)
}

document.addEventListener('touchmove', blockScroll, { passive: false })
onBeforeUnmount(() => {
  document.removeEventListener('touchmove', blockScroll)
  window.clearTimeout(holdTimer)
  if (dragging.value) drop()
})
</script>

<template>
  <div ref="root" class="relative h-full min-w-0 select-none">
    <div
      v-if="canRemove && swipeX < 0"
      class="absolute inset-y-1 inset-x-0 flex items-center justify-end gap-2 rounded-xl px-5 text-sm font-bold text-white transition-colors"
      :class="pastSwipe ? 'bg-red-500' : 'bg-red-500/60'"
    >
      {{ pastSwipe ? 'Release to remove' : 'Remove' }}<Icon name="close" :size="18" />
    </div>
    <div
      v-if="canMove && swipeX > 0"
      class="absolute inset-y-1 inset-x-0 flex items-center gap-2 rounded-xl px-5 text-sm font-bold text-white transition-colors"
      :class="pastSwipe ? 'bg-emerald-600' : 'bg-emerald-600/60'"
    >
      <Icon name="up" :size="18" />{{ pastSwipe ? 'Release to play next' : 'Play next' }}
    </div>
    <div
      class="relative flex h-full min-w-0 items-center gap-2 rounded-xl bg-ink-950"
      :class="dragging && 'bg-ink-800 shadow-2xl shadow-black/70 ring-1 ring-brand/60'"
      :style="style"
      @pointerdown="press"
      @pointermove="pressMove"
      @pointerup="release"
      @pointercancel="release"
      @contextmenu.prevent
    >
      <div class="min-w-0 flex-1"><SongCard :song="item.song" :subtitle="item.singer" /></div>
      <button
        v-if="canMove"
        class="grid h-11 w-10 shrink-0 cursor-grab place-items-center text-ink-300 active:cursor-grabbing"
        style="touch-action: none"
        aria-label="Drag to reorder"
        @pointerdown.stop="handleDown"
        @pointermove.stop="carry"
        @pointerup.stop="drop"
        @pointercancel.stop="drop"
      ><Icon name="queue" :size="22" /></button>
    </div>
  </div>
</template>
