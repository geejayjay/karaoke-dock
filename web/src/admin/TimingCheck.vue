<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { api, send } from './api'
import { formatTime } from '../shared/types'
import type { LyricsDoc } from '../shared/types'
import { SmoothClock } from '../tv/clock'
import { LyricsEngine, lineIndexAt } from '../tv/lyrics-engine'
import { Halftone, loadVisual } from '../tv/halftone'
import './result-preview.css'

const props = defineProps<{ song: { id: string; title: string; artist?: string; coverId?: string } }>()
defineEmits<{ close: [] }>()

type Mode = 'loading' | 'video' | 'audio'
const mode = ref<Mode>('loading')
const hear = ref<'video' | 'result'>('video')
const note = ref('')
const doc = ref<LyricsDoc | null>(null)
const accent = ref('#ff4d8d')
const loadError = ref('')
const activeLine = ref(-1)
const video = ref<HTMLVideoElement>()
const audio = ref<HTMLAudioElement>()
const list = ref<HTMLElement>()
const lyricsHost = ref<HTMLElement>()
const barFill = ref<HTMLElement>()
const timeText = ref<HTMLElement>()
const halftoneHost = ref<HTMLElement>()
let engine: LyricsEngine | null = null
let halftone: Halftone | null = null
let fetched = false
let raf = 0
let lastSecond = -1

const offset = computed(() => doc.value?.offsetMs ?? 0)
const lines = computed(() => doc.value?.lines ?? [])
const sourceLabel = computed(() => ({ loading: 'Getting the video…', video: 'Video', audio: 'No video' })[mode.value])

function master() {
  return mode.value === 'video' ? video.value : mode.value === 'audio' ? audio.value : undefined
}

/** Silence cut from the saved audio. Everything here runs on the uncut song's time. */
const trimStart = ref(0)
const trimEnd = ref(0)
const inCut = ref(false)
const trimLabel = computed(() => {
  const part = (ms: number, where: string) => (ms ? `${(ms / 1000).toFixed(1)}s cut from the ${where}` : '')
  return [part(trimStart.value, 'start'), part(trimEnd.value, 'end')].filter(Boolean).join(', ')
})

/** The saved audio's own clock is shorter, so its time needs the cut added back. */
const addedBack = () => (mode.value === 'audio' ? trimStart.value : 0)

const clock = new SmoothClock(() => (master()?.currentTime ?? 0) * 1000 + addedBack())

function playing() {
  const m = master()
  return !!m && !m.paused
}

/** In video mode the result's audio follows the video. */
function following() {
  return mode.value === 'video' ? audio.value : undefined
}

function isMaster(e: Event) {
  return e.target === master()
}

/** Where the saved audio should be for the video's current moment, in seconds. */
function audioTarget() {
  return (video.value?.currentTime ?? 0) - trimStart.value / 1000
}

function placeResult() {
  const a = following()
  if (a) a.currentTime = Math.max(audioTarget(), 0)
}

function onPlay(e: Event) {
  if (!isMaster(e)) return
  clock.start()
  engine?.setPlaying(clock.now(), true)
  placeResult()
}

function onPause(e: Event) {
  if (!isMaster(e)) return
  clock.stop()
  engine?.setPlaying(clock.now(), false)
  following()?.pause()
}

function onSeeked(e: Event) {
  if (!isMaster(e)) return
  clock.resync()
  engine?.sync(clock.now(), playing())
  placeResult()
}

function seek(ms: number) {
  const el = master()
  if (el) {
    el.currentTime = Math.max(0, ms - addedBack()) / 1000
    void el.play()
  }
}

function useAudio(reason: string) {
  mode.value = 'audio'
  note.value = reason
}

function drawProgress(pos: number) {
  const m = master()
  const own = m && Number.isFinite(m.duration) ? m.duration * 1000 : 0
  const d = own && mode.value === 'audio' ? own + trimStart.value + trimEnd.value : own
  if (barFill.value) barFill.value.style.transform = `scaleX(${d ? Math.min(pos / d, 1) : 0})`
  const second = Math.floor(pos / 1000)
  if (second !== lastSecond && timeText.value) {
    lastSecond = second
    timeText.value.textContent = `${formatTime(pos)} / ${formatTime(d)}`
  }
}

/** Keeps the saved audio with the video, and silent through the parts cut from it. */
function followVideo(isPlaying: boolean) {
  const a = following()
  if (!a) {
    if (inCut.value) inCut.value = false
    return
  }
  const want = audioTarget()
  const cut = want < 0 || (Number.isFinite(a.duration) && want >= a.duration)
  if (cut !== inCut.value) inCut.value = cut
  if (cut) {
    if (!a.paused) a.pause()
    return
  }
  if (!isPlaying) return
  if (a.paused) void a.play().catch(() => {})
  if (Math.abs(a.currentTime - want) > 0.25) a.currentTime = want
}

function tick() {
  raf = requestAnimationFrame(tick)
  const isPlaying = playing()
  if (clock.correct()) engine?.sync(clock.now(), isPlaying)
  const pos = clock.now()
  engine?.tick(pos, isPlaying)
  // The saved analysis follows the saved audio, which has the silence cut.
  halftone?.update(pos - trimStart.value, isPlaying && !inCut.value, performance.now())
  drawProgress(pos)
  followVideo(isPlaying)
  const shown = lines.value.length && pos - offset.value >= lines.value[0].startMs ? lineIndexAt(lines.value, pos - offset.value) : -1
  if (shown !== activeLine.value) activeLine.value = shown
}

watch(activeLine, async (i) => {
  await nextTick()
  const row = list.value?.querySelector<HTMLElement>(`[data-line="${i}"]`)
  if (row && list.value) list.value.scrollTo({ top: row.offsetTop - list.value.clientHeight / 2 + row.clientHeight / 2, behavior: 'smooth' })
})

onMounted(async () => {
  engine = new LyricsEngine(lyricsHost.value!, { countdown: true })
  halftone = new Halftone(halftoneHost.value!)
  void loadVisual(props.song.id, props.song.coverId, true).then(([data, palette]) => halftone?.setSong(props.song.id, data, palette))
  raf = requestAnimationFrame(tick)
  Promise.all([
    api<LyricsDoc>(`/api/songs/${props.song.id}/lyrics`),
    api<{ accentColor: string; trimStartMs: number; trimEndMs: number }>(`/api/admin/songs/${props.song.id}`).catch(() => ({ accentColor: '', trimStartMs: 0, trimEndMs: 0 })),
  ]).then(
    ([d, s]) => {
      doc.value = d
      accent.value = s.accentColor || accent.value
      trimStart.value = s.trimStartMs
      trimEnd.value = s.trimEndMs
      clock.resync()
      engine?.setDoc(d, accent.value)
      engine?.sync(clock.now(), playing())
    },
    (e: Error) => (loadError.value = e.message),
  )
  try {
    await send('POST', `/api/admin/songs/${props.song.id}/check-video`)
    fetched = true
    mode.value = 'video'
  } catch (e) {
    useAudio((e as Error).message)
  }
})

onBeforeUnmount(() => {
  cancelAnimationFrame(raf)
  engine?.destroy()
  halftone?.destroy()
  halftone = null
  audio.value?.pause()
  if (fetched) void send('DELETE', `/api/admin/songs/${props.song.id}/check-video`).catch(() => {})
})

const pill = 'rounded-full px-3 py-1 text-sm font-semibold'
</script>

<template>
  <div class="fixed inset-0 z-20 flex flex-col gap-3 bg-ink-950 p-3 sm:p-5">
    <header class="flex items-center justify-between gap-3">
      <div class="min-w-0">
        <h3 class="truncate text-lg font-bold">{{ song.title }}</h3>
        <p class="truncate text-sm text-ink-300">{{ sourceLabel }}<template v-if="doc"> · {{ doc.timing === 'word' ? 'Timed by word' : 'Timed by line' }}</template><template v-if="trimLabel"> · {{ trimLabel }}</template></p>
      </div>
      <div class="flex shrink-0 items-center gap-2">
        <template v-if="mode === 'video'">
          <span class="hidden text-sm text-ink-300 sm:inline">Hear</span>
          <button :class="[pill, hear === 'video' ? 'bg-brand text-white' : 'bg-ink-800 text-ink-100']" @click="hear = 'video'">Video</button>
          <button :class="[pill, hear === 'result' ? 'bg-brand text-white' : 'bg-ink-800 text-ink-100']" @click="hear = 'result'">Result</button>
        </template>
        <button class="rounded-lg bg-ink-700 px-4 py-2 font-medium" @click="$emit('close')">Close</button>
      </div>
    </header>
    <p v-if="note" class="text-sm text-amber-200">{{ note }}</p>

    <div class="grid shrink-0 gap-3 md:grid-cols-2">
      <div class="flex aspect-video items-center justify-center overflow-hidden rounded-xl bg-black">
        <p v-if="mode === 'loading'" class="text-ink-300">Getting the video…</p>
        <video
          v-else-if="mode === 'video'"
          ref="video"
          :src="`/media/${song.id}/video`"
          :muted="hear !== 'video'"
          controls
          playsinline
          class="h-full w-full"
          @play="onPlay"
          @pause="onPause"
          @seeked="onSeeked"
          @error="useAudio('The video would not play.')"
        />
        <audio v-show="mode === 'audio'" ref="audio" :src="`/media/${song.id}/audio`" :muted="mode === 'video' && hear === 'video'" controls class="m-6 w-full" @play="onPlay" @pause="onPause" @seeked="onSeeked" />
      </div>

      <div class="result-stage aspect-video rounded-xl">
        <div class="bg-plain" />
        <div ref="halftoneHost" class="layer" />
        <div class="dim" />
        <div class="result-title">
          <img v-if="song.coverId" :src="`/covers/${song.coverId}`" alt="" />
          <div class="result-title-text">
            <h4>{{ song.title }}</h4>
            <p v-if="song.artist">{{ song.artist }}</p>
          </div>
        </div>
        <div ref="lyricsHost" />
        <div v-if="inCut" class="result-cut">Silence cut from the result</div>
        <div ref="timeText" class="result-time" />
        <div class="result-bar"><div ref="barFill" :style="{ background: accent }" /></div>
      </div>
    </div>

    <div ref="list" class="relative min-h-0 flex-1 overflow-y-auto rounded-xl bg-ink-900 p-2">
      <p v-if="loadError" class="p-6 text-center text-ink-300">{{ loadError }}</p>
      <button
        v-for="(l, i) in lines"
        :key="i"
        :data-line="i"
        class="flex w-full items-baseline gap-3 rounded-lg px-3 py-1 text-left"
        :class="i === activeLine ? 'bg-brand/20' : ''"
        @click="seek(l.startMs + offset)"
      >
        <span class="w-11 shrink-0 font-mono text-xs text-ink-500">{{ formatTime(l.startMs + offset) }}</span>
        <span class="min-w-0" :class="i === activeLine ? 'font-semibold text-white' : 'text-ink-300'">{{ l.words.map((w) => w.text).join(' ') }}<span v-if="l.backing?.length" class="ml-2 text-sm italic text-ink-500">{{ l.backing.map((w) => w.text).join(' ') }}</span></span>
      </button>
    </div>
  </div>
</template>
