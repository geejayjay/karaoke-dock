<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { Live } from '../shared/live'
import { formatTime } from '../shared/types'
import type { CurrentSong, LyricsDoc, Network, PlaybackSettings, PlayerState, QueueItem, ServerEvent } from '../shared/types'
import { SmoothClock } from './clock'
import { LyricsEngine, VOICE_COLORS } from './lyrics-engine'
import { ProgressBar, buildSegments } from './progress'
import { Halftone, decode, loadVisual } from './halftone'
import BrandMark from '../shared/BrandMark.vue'

const started = ref(false)
const connected = ref(false)
const joinUrl = ref('')
const qrStamp = ref(0)
const network = ref<Network>({ online: false, mediaOk: true })
const playback = ref<PlaybackSettings>({ animationLevel: 'auto', countdown: true, countdownSeconds: 10 })
const upNext = ref<QueueItem[]>([])
const state = shallowRef<PlayerState>({ current: null, paused: false, positionMs: 0, seekSeq: 0, seekMs: 0, audioMode: '' })
const autoLite = ref(false)
const lite = computed(() => playback.value.animationLevel === 'lite' || (playback.value.animationLevel === 'auto' && autoLite.value))

const lyricsHost = ref<HTMLElement>()
const progressHost = ref<HTMLElement>()
const audio = ref<HTMLAudioElement>()
const video = ref<HTMLVideoElement>()
const halftoneHost = ref<HTMLElement>()
const reactionsHost = ref<HTMLElement>()

const IDLE_SONG = 'karaokedock'
const titleSmall = ref(false)
const prepLeft = ref(0)
const timeLeft = ref('')
const breakLabel = ref('')
const nextOn = ref(false)
const voices = ref<LyricsDoc['voices']>([])

const current = computed(() => state.value.current)
// With our lyrics on screen the video stays hidden, because it may show its own.
const isVideo = computed(() => current.value?.source.kind === 'video' && !current.value.hasLyrics)
const accent = computed(() => current.value?.accentColor || '#ff4d8d')
const comingUp = computed(() => upNext.value.filter((q) => q.song.state !== 'failed'))
const nextItem = computed(() => comingUp.value[0])

let engine: LyricsEngine | null = null
let bar: ProgressBar | null = null
let halftone: Halftone | null = null
let barLevels: Uint8Array | null = null
let barDoc: LyricsDoc | null = null
let live: Live | null = null
let raf = 0
let lastCorrect = 0
let lastReport = 0
let lastSecond = -1
let lastSeek = 0
let frameLast = 0
let slowFrames = 0
let loadedFor = ''
let loadedTrim = 0
let prepTimer = 0

function master(): HTMLMediaElement | null {
  const src = current.value?.source
  if (!src) return null
  if (src.audioUrl) return audio.value ?? null
  return video.value ?? null
}

const clock = new SmoothClock(() => (master()?.currentTime ?? 0) * 1000)

function durationMs(): number {
  const m = master()
  const d = m && Number.isFinite(m.duration) ? m.duration * 1000 : 0
  return d || current.value?.durationMs || 0
}

function playing(): boolean {
  const m = master()
  return !!m && !m.paused && !state.value.paused
}

/** Lyrics are timed to the uncut song, so they move up by any silence cut from this audio. */
function forPlayback(doc: LyricsDoc, c: CurrentSong): LyricsDoc {
  return { ...doc, offsetMs: doc.offsetMs - (c.source.trimStartMs ?? 0) }
}

async function loadLyrics(atNextLine = false) {
  const c = current.value
  if (!c || !engine) return
  if (atNextLine && c.hasLyrics) {
    const res = await fetch(`/api/songs/${c.songId}/lyrics`, { cache: 'no-cache' }).catch(() => null)
    if (res?.ok && current.value?.songId === c.songId) engine.queueDoc(forPlayback((await res.json()) as LyricsDoc, c))
    return
  }
  if (!c.hasLyrics) {
    engine.setDoc(null, accent.value)
    voices.value = []
    redrawBar(null)
    return
  }
  const id = c.songId
  const res = await fetch(`/api/songs/${id}/lyrics`, { cache: 'no-cache' }).catch(() => null)
  if (!res?.ok || current.value?.songId !== id) return
  const doc = forPlayback((await res.json()) as LyricsDoc, c)
  engine.setDoc(doc, accent.value)
  voices.value = doc.voices.filter((v) => doc.lines.some((l) => l.voice === v.id))
  redrawBar(doc)
  const pos = clock.now()
  engine.sync(pos, playing())
}

/** The analysis matches the finished audio, so quick mode drifts on its own. */
async function loadBackground() {
  const c = current.value
  if (!c || !halftone) return
  const [data, palette] = await loadVisual(c.songId, c.coverId, c.source.kind === 'audio' && !c.source.quickMode)
  if (current.value?.itemId !== c.itemId) return
  halftone.setSong(c.songId, data, palette)
  barLevels = data?.level ? decode(data.level) : null
  redrawBar(barDoc)
}

function redrawBar(doc: LyricsDoc | null) {
  if (!bar) return
  barDoc = doc
  const d = durationMs()
  bar.draw(doc ? buildSegments(doc.lines, d, doc.offsetMs) : [], accent.value, barLevels)
  bar.run(clock.now(), d, playing())
}

/** Loads the sources for the current song, only when they change. */
function applySource() {
  const c = current.value
  const a = audio.value
  const v = video.value
  if (!a || !v) return
  if (!c) {
    a.pause()
    v.pause()
    a.removeAttribute('src')
    v.removeAttribute('src')
    a.load()
    v.load()
    loadedFor = ''
    return
  }
  const key = `${c.itemId}|${c.source.audioUrl ?? ''}|${c.source.videoUrl ?? ''}`
  const switching = loadedFor !== '' && loadedFor.split('|')[0] === String(c.itemId)
  // Switching between cut and uncut audio keeps the same moment of the song.
  const trim = c.source.trimStartMs ?? 0
  const resumeAt = switching ? Math.max(clock.now() + loadedTrim - trim, 0) / 1000 : 0
  if (key === loadedFor) {
    if (v) v.muted = !!c.source.videoMuted || !!c.source.audioUrl
    return
  }
  loadedFor = key
  loadedTrim = trim
  v.muted = !!c.source.videoMuted || !!c.source.audioUrl
  setSrc(a, c.source.audioUrl)
  setSrc(v, c.source.videoUrl)
  const m = master()
  if (!m) return
  m.addEventListener(
    'loadedmetadata',
    () => {
      if (switching) {
        m.currentTime = resumeAt
        if (c.source.videoUrl && c.source.audioUrl) v.currentTime = resumeAt
      }
      redrawBar(null)
      void loadLyrics()
      void loadBackground()
    },
    { once: true },
  )
  void play()
}

function setSrc(el: HTMLMediaElement, url?: string) {
  if (url) {
    el.src = url
    el.load()
  } else {
    el.pause()
    el.removeAttribute('src')
    el.load()
  }
}

async function play() {
  const c = current.value
  if (!c || state.value.paused || !started.value || prepLeft.value > 0) return
  const m = master()
  try {
    await m?.play()
    if (c.source.videoUrl && c.source.audioUrl) await video.value?.play()
  } catch (err) {
    if ((err as DOMException).name !== 'AbortError') console.warn('playback blocked', err)
  }
}

function applyPause() {
  const m = master()
  if (!m) return
  if (state.value.paused) {
    m.pause()
    video.value?.pause()
  } else {
    void play()
  }
}

function seek(ms: number) {
  const m = master()
  if (!m) return
  m.currentTime = ms / 1000
  const c = current.value
  if (c?.source.videoUrl && c.source.audioUrl && video.value) video.value.currentTime = ms / 1000
  clock.resync()
  engine?.sync(ms, playing())
  bar?.resync(ms)
}

function endGetReady() {
  window.clearInterval(prepTimer)
  prepLeft.value = 0
  titleSmall.value = true
}

/**
 * Holds a new song for a few seconds with its title on screen, so the singer
 * can get ready and the title is out of the way before any lyrics appear.
 * A song already under way, as after a TV reload, carries straight on.
 */
function startGetReady(positionMs: number) {
  window.clearInterval(prepTimer)
  const { countdown, countdownSeconds } = playback.value
  if (positionMs > 1000 || !countdown || countdownSeconds <= 0) {
    endGetReady()
    return
  }
  titleSmall.value = false
  prepLeft.value = countdownSeconds
  prepTimer = window.setInterval(() => {
    if (!started.value || state.value.paused) return
    prepLeft.value--
    if (prepLeft.value <= 0) {
      endGetReady()
      void play()
    }
  }, 1000)
}

function frame(ts: number) {
  raf = requestAnimationFrame(frame)
  if (frameLast && document.visibilityState === 'visible') {
    const dt = ts - frameLast
    slowFrames = dt > 26 ? slowFrames + 1 : Math.max(0, slowFrames - 0.25)
    if (slowFrames > 90 && !autoLite.value) autoLite.value = true
  }
  frameLast = ts
  const c = current.value
  if (!c) {
    halftone?.update(0, true, ts) // keeps moving when nothing is queued
    return
  }
  const isPlaying = playing()
  if (ts - lastCorrect > 250) {
    lastCorrect = ts
    if (clock.correct()) {
      const p = clock.now()
      engine?.sync(p, isPlaying)
      bar?.resync(p)
    }
    // Audio is the clock. Only the video moves to match it.
    if (c.source.videoUrl && c.source.audioUrl && video.value && audio.value) {
      if (Math.abs(video.value.currentTime - audio.value.currentTime) > 0.15) video.value.currentTime = audio.value.currentTime
    }
  }
  const pos = clock.now()
  engine?.tick(pos, isPlaying)
  if (!isVideo.value) halftone?.update(pos, isPlaying || prepLeft.value > 0, ts)
  const d = durationMs()
  const sec = Math.floor(pos / 1000)
  if (sec !== lastSecond) {
    lastSecond = sec
    timeLeft.value = d ? `-${formatTime(d - pos)}` : ''
    const brk = engine?.breakInfo(pos)
    breakLabel.value = brk ? `Music break ${formatTime(brk.remainingMs)}` : ''
    nextOn.value = !!nextItem.value && d > 0 && d - pos < 30000
  }
  if (ts - lastReport > 1000) {
    lastReport = ts
    live?.send({ type: 'position', ms: Math.round(pos) })
  }
}

function onMediaPlay() {
  clock.start()
  const p = clock.now()
  engine?.setPlaying(p, true)
  bar?.run(p, durationMs(), true)
}

function onMediaPause() {
  clock.stop()
  const p = clock.now()
  engine?.setPlaying(p, false)
  bar?.run(p, durationMs(), false)
}

function onMediaSeeked() {
  clock.resync()
}

function onEnded() {
  const c = current.value
  if (c) live?.send({ type: 'ended', itemId: c.itemId })
}

function onError(e: Event) {
  const c = current.value
  const el = e.target as HTMLMediaElement
  if (c && el.getAttribute('src')) live?.send({ type: 'error', itemId: c.itemId })
}

function isMaster(e: Event): boolean {
  return e.target === master()
}

const MAX_REACTIONS = 24

function floatReaction(emoji: string) {
  const host = reactionsHost.value
  if (!host || lite.value || host.childElementCount >= MAX_REACTIONS) return
  const el = document.createElement('span')
  el.className = 'reaction'
  el.textContent = emoji
  el.style.left = `${6 + Math.random() * 88}%`
  el.style.animationDuration = `${3 + Math.random() * 1.5}s`
  el.addEventListener('animationend', () => el.remove(), { once: true })
  host.appendChild(el)
}

function handle(e: ServerEvent) {
  switch (e.type) {
    case 'snapshot':
      joinUrl.value = e.data.joinUrl
      network.value = e.data.network
      playback.value = e.data.playback
      upNext.value = e.data.upNext
      qrStamp.value++
      setState(e.data.player)
      break
    case 'player.state':
      setState(e.data)
      break
    case 'queue.updated':
      upNext.value = e.data.upNext
      break
    case 'reaction':
      floatReaction(e.data.emoji)
      break
    case 'network.status':
      network.value = e.data
      qrStamp.value++
      break
    case 'song.lyrics':
      if (current.value?.songId === e.data.songId) void loadLyrics()
      break
    case 'song.state':
      upNext.value = upNext.value.map((q) => (q.song.id === e.data.song.id ? { ...q, song: e.data.song } : q))
      // New word timing applies as the lyrics reload, from the next line.
      if (current.value?.songId === e.data.song.id && (e.data.song.state === 'complete' || e.data.song.state === 'playable')) {
        void loadLyrics(true)
      }
      break
    case 'settings.playback':
      playback.value = e.data.playback
      joinUrl.value = e.data.joinUrl
      qrStamp.value++
      engine?.setCountdown(e.data.playback.countdown)
      break
  }
}

function setState(next: PlayerState) {
  const prev = state.value
  state.value = next
  const newItem = prev.current?.itemId !== next.current?.itemId
  if (newItem) {
    engine?.setDoc(null, next.current?.accentColor ?? '')
    bar?.clear()
    barLevels = null
    barDoc = null
    voices.value = []
    if (next.current) startGetReady(next.positionMs)
    else {
      endGetReady()
      halftone?.setSong(IDLE_SONG, null, null, true)
    }
  }
  applySource()
  if (!newItem && prev.paused !== next.paused) applyPause()
  if (next.seekSeq !== lastSeek) {
    lastSeek = next.seekSeq
    if (!newItem) seek(next.seekMs)
  }
}

function begin() {
  started.value = true
  // A user gesture unlocks both elements, and they stay alive between songs.
  void nextTick(() => {
    for (const el of [audio.value, video.value]) {
      el?.play().catch(() => {})
      if (prepLeft.value > 0) el?.pause() // unlocked, but the song waits its turn
    }
    applySource()
    void play()
  })
}

watch(lite, (v) => {
  engine?.setLite(v)
  halftone?.setLite(v)
})

onMounted(() => {
  engine = new LyricsEngine(lyricsHost.value!, { countdown: true })
  bar = new ProgressBar(progressHost.value!)
  halftone = new Halftone(halftoneHost.value!, lite.value)
  halftone.setSong(IDLE_SONG, null, null, true)
  engine.setLite(lite.value)
  live = new Live('/ws?client=tv', handle, (up) => (connected.value = up))
  raf = requestAnimationFrame(frame)
})

onBeforeUnmount(() => {
  cancelAnimationFrame(raf)
  window.clearInterval(prepTimer)
  engine?.destroy()
  halftone?.destroy()
  live?.close()
})
</script>

<template>
  <div class="stage" :class="{ lite }">
    <!-- Background -->
    <div v-show="!isVideo" ref="halftoneHost" class="layer bg-plain" />

    <!-- Song video, while processing -->
    <video
      ref="video"
      v-show="current && isVideo"
      class="song-video"
      playsinline
      @play="isMaster($event) && onMediaPlay()"
      @pause="isMaster($event) && onMediaPause()"
      @seeked="isMaster($event) && onMediaSeeked()"
      @ended="isMaster($event) && onEnded()"
      @error="isMaster($event) && onError($event)"
    />
    <audio
      ref="audio"
      @play="isMaster($event) && onMediaPlay()"
      @pause="isMaster($event) && onMediaPause()"
      @seeked="isMaster($event) && onMediaSeeked()"
      @ended="isMaster($event) && onEnded()"
      @error="isMaster($event) && onError($event)"
    />

    <div v-show="current" class="dim" />

    <!-- Song screen -->
    <div v-show="current" class="layer">
      <div class="title-card" :class="{ small: titleSmall }">
        <img v-if="current?.coverId" :src="`/covers/${current.coverId}`" alt="" class="title-art" />
        <div class="title-text">
          <h1>{{ current?.title }}</h1>
          <p>{{ current?.artist }}<template v-if="current?.artist"> · </template><span class="guest">{{ current?.singer }}</span></p>
        </div>
      </div>
      <div v-if="prepLeft > 0" class="get-ready">
        <span class="get-ready-label">Get ready{{ current?.singer ? `, ${current.singer}` : '' }}</span>
        <span :key="prepLeft" class="get-ready-count">{{ prepLeft }}</span>
      </div>
      <div v-if="prepLeft > 0" class="get-ready-qr">
        <div class="qr-big"><img :src="`/api/qr.png?v=${qrStamp}`" alt="QR code to join" /></div>
        <div class="get-ready-url">{{ joinUrl }}</div>
      </div>
      <div class="lyrics-box" :class="{ waiting: prepLeft > 0 }"><div ref="lyricsHost" /></div>
      <div class="music-break" :class="{ on: breakLabel && prepLeft === 0 }">{{ breakLabel }}</div>
      <div v-if="voices.length > 1" class="legend">
        <span v-for="v in voices" :key="v.id"><i :style="{ background: VOICE_COLORS[v.id] }" />{{ v.label }}</span>
      </div>
      <div class="progress-wrap">
        <div class="progress-row">
          <span class="next-up" :class="{ on: nextOn }"><template v-if="nextItem">Next: {{ nextItem.song.title }}, {{ nextItem.singer }}</template></span>
          <span class="time-left">{{ timeLeft }}</span>
        </div>
        <div ref="progressHost" />
      </div>
      <div v-show="prepLeft === 0" class="corner-right">
        <div class="qr-small"><img :src="`/api/qr.png?v=${qrStamp}`" alt="" /></div>
        <div v-if="comingUp.length" class="up-next">
          <b>Up next</b>
          <div v-for="q in comingUp.slice(0, 3)" :key="q.id">{{ q.song.title }}</div>
        </div>
      </div>
    </div>

    <!-- Idle -->
    <div v-if="!current && started" class="idle">
      <div class="idle-left">
        <BrandMark stacked :still="lite" />
        <div class="idle-hint">Scan to add a song</div>
      </div>
      <div class="idle-qr">
        <div class="qr-big"><img :src="`/api/qr.png?v=${qrStamp}`" alt="QR code to join" /></div>
        <div class="get-ready-url">{{ joinUrl }}</div>
      </div>
    </div>

    <!-- One press unlocks sound on TV browsers -->
    <div v-if="!started" class="layer start-veil">
      <div class="center">
        <BrandMark :still="lite" />
        <button class="start-btn" autofocus @click="begin">Press OK to start</button>
      </div>
    </div>

    <div ref="reactionsHost" class="reactions" />

    <div v-if="!connected" class="lost">Reconnecting…</div>
    <div v-else-if="!network.mediaOk" class="lost">Music drive unavailable</div>
  </div>
</template>
