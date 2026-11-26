<script setup lang="ts">
import { computed } from 'vue'
import type { Part } from './status'

const props = defineProps<{ audio: Part; lyrics: Part; timedLines?: number; lyricLines?: number }>()

const timing = computed(() => {
  const timed = props.timedLines ?? 0
  const total = props.lyricLines ?? 0
  if (!total) return null
  if (!timed) return { label: 'Not timed from video', tone: 'bg-ink-800 text-ink-300' }
  const share = timed / total
  const tone = share >= 0.8 ? 'bg-emerald-500/15 text-emerald-300' : share >= 0.5 ? 'bg-amber-500/15 text-amber-200' : 'bg-red-500/15 text-red-300'
  return { label: `From video: ${timed} of ${total} lines`, tone }
})

const labels: Record<Part, string> = {
  waiting: 'Waiting',
  working: 'Working',
  done: 'Done',
  none: 'None found',
  failed: 'Failed',
}

function tone(p: Part) {
  if (p === 'done') return 'bg-emerald-500/15 text-emerald-300'
  if (p === 'working') return 'bg-amber-500/15 text-amber-200'
  if (p === 'failed' || p === 'none') return 'bg-red-500/15 text-red-300'
  return 'bg-ink-800 text-ink-300'
}

const chip = 'whitespace-nowrap rounded-full px-2.5 py-0.5 text-xs font-medium'
</script>

<template>
  <span :class="[chip, tone(audio)]">Audio: {{ labels[audio] }}</span>
  <span :class="[chip, tone(lyrics)]">Lyrics: {{ labels[lyrics] }}</span>
  <span v-if="timing" :class="[chip, timing.tone]">{{ timing.label }}</span>
</template>
