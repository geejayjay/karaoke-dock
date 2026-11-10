<script setup lang="ts">
import { computed, ref } from 'vue'
import SongCard from './SongCard.vue'
import Icon from './Icon.vue'
import type { QueueItem } from '../shared/types'

const props = defineProps<{ item: QueueItem; canRemove: boolean; canMove: boolean; rowHeight: number }>()
const emit = defineEmits<{ remove: []; move: [steps: number]; playNext: []; duet: [] }>()

const SWIPE_START = 10
const SWIPE_REMOVE = 90

const root = ref<HTMLElement>()
const swipeX = ref(0)
const dragY = ref(0)
const swiping = ref(false)
const dragging = ref(false)
let startX = 0
let startY = 0
let tracking = false

const style = computed(() => ({
  transform: `translate3d(${swipeX.value}px, ${dragY.value}px, 0)`,
  transition: swiping.value || dragging.value ? 'none' : 'transform .18s ease',
  touchAction: 'pan-y',
}))

function swipeStart(e: PointerEvent) {
  if (!props.canRemove) return
  tracking = true
  startX = e.clientX
  startY = e.clientY
}

function swipeMove(e: PointerEvent) {
  if (!tracking) return
  const dx = e.clientX - startX
  if (!swiping.value) {
    if (Math.abs(e.clientY - startY) > SWIPE_START) tracking = false
    else if (Math.abs(dx) > SWIPE_START) {
      swiping.value = true
      ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
    }
    return
  }
  swipeX.value = props.canMove ? dx : Math.min(dx, 0)
}

function swipeEnd() {
  tracking = false
  if (!swiping.value) return
  swiping.value = false
  if (swipeX.value <= -SWIPE_REMOVE) {
    swipeX.value = -(root.value?.clientWidth ?? 400)
    navigator.vibrate?.(12)
    emit('remove')
    // If the server refuses, the row comes back.
    window.setTimeout(() => (swipeX.value = 0), 1500)
  } else {
    if (swipeX.value >= SWIPE_REMOVE && props.canMove) {
      navigator.vibrate?.(12)
      emit('playNext')
    }
    swipeX.value = 0
  }
}

// A row sits under the rows after it, so lift it while it's carried.
function lift(on: boolean) {
  const row = root.value?.parentElement
  if (row) row.style.zIndex = on ? '20' : ''
}

function dragStart(e: PointerEvent) {
  dragging.value = true
  startY = e.clientY
  lift(true)
  navigator.vibrate?.(12)
  ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
}

function dragMove(e: PointerEvent) {
  if (dragging.value) dragY.value = e.clientY - startY
}

function dragEnd() {
  if (!dragging.value) return
  const steps = Math.round(dragY.value / props.rowHeight)
  dragging.value = false
  dragY.value = 0
  lift(false)
  if (steps !== 0) emit('move', steps)
}
</script>

<template>
  <div ref="root" class="relative h-full min-w-0">
    <div v-if="canRemove" class="absolute inset-y-1 right-0 flex items-center rounded-xl bg-red-500 px-5 text-sm font-bold text-white" :style="{ opacity: Math.min(-swipeX / 70, 1) }">Remove</div>
    <div v-if="canMove" class="absolute inset-y-1 left-0 flex items-center rounded-xl bg-emerald-600 px-5 text-sm font-bold text-white" :style="{ opacity: Math.min(swipeX / 70, 1) }">Play next</div>
    <div
      class="relative flex h-full min-w-0 items-center gap-2 bg-ink-950"
      :class="dragging && 'rounded-xl shadow-xl shadow-black/60'"
      :style="style"
      @pointerdown="swipeStart"
      @pointermove="swipeMove"
      @pointerup="swipeEnd"
      @pointercancel="swipeEnd"
    >
      <div class="min-w-0 flex-1"><SongCard :song="item.song" :subtitle="item.singer" /></div>
      <button v-if="canRemove" class="shrink-0 rounded-full bg-ink-800 px-3 py-1.5 text-xs font-semibold text-ink-100" @pointerdown.stop @click="emit('duet')">Duet</button>
      <button
        v-if="canMove"
        class="grid h-11 w-10 shrink-0 cursor-grab place-items-center text-ink-300"
        style="touch-action: none"
        aria-label="Drag to reorder"
        @pointerdown.stop="dragStart"
        @pointermove.stop="dragMove"
        @pointerup.stop="dragEnd"
        @pointercancel.stop="dragEnd"
      ><Icon name="queue" :size="22" /></button>
    </div>
  </div>
</template>
