<script setup lang="ts">
import { computed, onActivated, onBeforeUnmount, onDeactivated, ref, watch } from 'vue'
import { post } from './api'
import { useParty } from './party'
import Art from './Art.vue'
import Icon from './Icon.vue'
import BrandMark from '../shared/BrandMark.vue'
import { formatTime } from '../shared/types'
import type { LyricsDoc, Voice } from '../shared/types'

const SKIP_MS = 10000
const NUDGE_MS = 100
const HOLD_DELAY_MS = 400
const HOLD_REPEAT_MS = 120

const party = useParty()
const pinInput = ref('')
const error = ref('')
const current = computed(() => party.player?.current ?? null)
const paused = computed(() => party.player?.paused ?? false)
const audioMode = computed(() => party.player?.audioMode ?? '')
const offsetMs = ref(0)
const doc = ref<LyricsDoc | null>(null)
const editing = ref(false)
const dragging = ref<number | null>(null)
const shownPosition = computed(() => dragging.value ?? party.positionMs)

/** A small buzz, so the buttons feel like a real remote. */
function buzz() {
  navigator.vibrate?.(8)
}

async function unlock() {
  error.value = ''
  party.setPin(pinInput.value)
  try {
    await post('/api/host/verify')
    pinInput.value = ''
  } catch (e) {
    party.setPin('')
    error.value = (e as Error).message
  }
}

function lock() {
  party.setPin('')
}

// A saved PIN may have been changed in Admin since.
onActivated(() => {
  if (party.pin) post('/api/host/verify').catch(() => party.setPin(''))
})

async function act(action: string, body: object = {}) {
  buzz()
  try {
    await post(`/api/player/${action}`, body)
  } catch (e) {
    party.say((e as Error).message)
  }
}

function jump(deltaMs: number) {
  const max = current.value?.durationMs ?? 0
  void act('seek', { ms: Math.min(Math.max(party.positionMs + deltaMs, 0), max || Infinity) })
}

function dragTo(e: Event) {
  dragging.value = Number((e.target as HTMLInputElement).value)
}

function dropAt() {
  if (dragging.value !== null) void act('seek', { ms: dragging.value })
  dragging.value = null
}

/** Sound key: switches between the two options this song has. */
const soundKey = computed(() => {
  if (audioMode.value === 'quick') return { label: 'Less vocals', next: 'original' }
  if (audioMode.value === 'original') return { label: 'Original', next: 'quick' }
  if (current.value?.hasGuide) return audioMode.value === 'guide' ? { label: 'Guide on', next: 'instrumental' } : { label: 'Guide off', next: 'guide' }
  return null
})

async function loadDoc() {
  doc.value = null
  const id = current.value?.songId
  if (!id || !current.value?.hasLyrics || party.pin === '') return
  const res = await fetch(`/api/songs/${id}/lyrics`, { cache: 'no-cache' }).catch(() => null)
  if (res?.ok) {
    doc.value = (await res.json()) as LyricsDoc
    offsetMs.value = doc.value.offsetMs
  }
}

watch([() => current.value?.songId, () => party.pin], loadDoc)
onActivated(loadDoc) // the lyric timing may have changed while on another tab

async function nudge(delta: number) {
  const id = current.value?.songId
  if (!id) return
  buzz()
  offsetMs.value += delta
  try {
    await post(`/api/songs/${id}/offset`, { offsetMs: offsetMs.value })
  } catch (e) {
    party.say((e as Error).message)
  }
}

// Holding a lyric key keeps nudging, like holding volume on a remote.
let holdDelay = 0
let holdRepeat = 0

function stopHold() {
  window.clearTimeout(holdDelay)
  window.clearInterval(holdRepeat)
}

function startHold(delta: number) {
  stopHold()
  void nudge(delta)
  holdDelay = window.setTimeout(() => {
    holdRepeat = window.setInterval(() => void nudge(delta), HOLD_REPEAT_MS)
  }, HOLD_DELAY_MS)
}

onBeforeUnmount(stopHold)
onDeactivated(stopHold)

const DEFAULT_VOICES: Voice[] = [
  { id: 'a', label: 'Boy', source: 'host' },
  { id: 'b', label: 'Girl', source: 'host' },
  { id: 'both', label: 'Both', source: 'host' },
]
const voices = computed(() => (doc.value?.voices.length ? doc.value.voices : DEFAULT_VOICES))

function cycleVoice(index: number) {
  const line = doc.value?.lines[index]
  if (!line) return
  const ids = ['', ...voices.value.map((v) => v.id)]
  line.voice = ids[(ids.indexOf(line.voice ?? '') + 1) % ids.length]
}

function swapLabels() {
  const v = voices.value
  if (v.length < 2 || !doc.value) return
  const copy = v.map((x) => ({ ...x }))
  ;[copy[0].label, copy[1].label] = [copy[1].label, copy[0].label]
  doc.value.voices = copy
}

async function saveParts() {
  const d = doc.value
  const id = current.value?.songId
  if (!d || !id) return
  try {
    await post(`/api/songs/${id}/voices`, {
      voices: voices.value,
      lines: d.lines.map((l, i) => ({ from: i, to: i, voice: l.voice ?? '' })),
    })
    editing.value = false
    party.say('Saved')
  } catch (e) {
    party.say((e as Error).message)
  }
}

async function flipKaraoke() {
  const c = current.value
  if (!c) return
  buzz()
  try {
    await post(`/api/songs/${c.songId}/karaoke`, { karaoke: !c.karaoke })
    party.say('Updating this song')
  } catch (e) {
    party.say((e as Error).message)
  }
}

function voiceLabel(id?: string) {
  return voices.value.find((v) => v.id === id)?.label ?? 'All'
}

const btn = 'rounded-xl bg-ink-800 py-3 font-semibold transition-transform active:scale-95'
const pad = 'absolute grid place-items-center text-white transition-transform active:scale-90'
const padLabel = 'text-[10px] font-semibold text-ink-300'
const key = 'flex h-14 flex-col items-center justify-center rounded-2xl bg-ink-700 text-xs font-semibold text-white shadow-[0_3px_0_rgba(0,0,0,.45)] transition-transform active:translate-y-0.5 active:shadow-none'
</script>

<template>
  <div class="flex h-full flex-col">
    <header class="px-4 py-3">
      <BrandMark class="text-xl" />
    </header>

    <form v-if="!party.pin" class="mx-auto flex w-full max-w-xs flex-1 flex-col justify-center gap-4 p-6" @submit.prevent="unlock">
      <div class="text-center">
        <h2 class="text-2xl font-extrabold text-white">Host remote</h2>
        <p class="mt-1 text-sm text-ink-300">Enter the PIN to take control.</p>
      </div>
      <input v-model="pinInput" type="tel" inputmode="numeric" autocomplete="off" autofocus aria-label="Host PIN" placeholder="••••" class="w-full rounded-2xl bg-ink-800 px-4 py-4 text-center text-3xl font-bold tracking-[.4em] outline-none placeholder:text-ink-500 focus:ring-2 focus:ring-brand" />
      <p v-if="error" class="text-center text-sm text-red-300">{{ error }}</p>
      <button class="w-full rounded-2xl bg-brand py-4 text-lg font-bold text-white">Unlock</button>
    </form>

    <div v-else-if="editing && doc" class="flex min-h-0 flex-1 flex-col">
      <h2 class="px-4 text-lg font-bold">Who sings each line?</h2>
      <p class="px-4 text-sm text-ink-300">Tap a line to change it.</p>
      <div class="flex gap-2 p-3">
        <button :class="btn" class="flex-1" @click="editing = false">Cancel</button>
        <button :class="btn" class="flex-1" @click="swapLabels">Swap names</button>
        <button class="flex-1 rounded-xl bg-brand py-3 font-bold text-white" @click="saveParts">Save</button>
      </div>
      <ul class="min-h-0 flex-1 divide-y divide-ink-800 overflow-y-auto">
        <li v-for="(l, i) in doc.lines" :key="i">
          <button class="flex w-full items-center gap-3 px-4 py-3 text-left" @click="cycleVoice(i)">
            <span class="w-14 shrink-0 text-xs font-bold text-brand">{{ voiceLabel(l.voice) }}</span>
            <span class="min-w-0 flex-1 truncate">{{ l.words.map((w) => w.text).join(' ') }}</span>
          </button>
        </li>
      </ul>
    </div>

    <div v-else-if="!current" class="flex flex-1 flex-col items-center justify-center gap-3 p-10 text-center text-ink-300">
      <Icon name="music" :size="40" />
      <p class="text-lg font-semibold text-white">Nothing's playing</p>
      <p class="text-sm">Add a song and the remote will wake up.</p>
      <button class="mt-3 flex items-center gap-1.5 rounded-full bg-ink-800 px-4 py-2 text-sm font-medium text-ink-100" @click="lock"><Icon name="lock" :size="16" />Lock remote</button>
    </div>

    <div v-else class="no-scrollbar min-h-0 flex-1 overflow-y-auto px-4 pb-5">
      <div class="mx-auto max-w-sm rounded-[2.5rem] border border-ink-700 bg-gradient-to-b from-ink-800 to-ink-900 p-5 shadow-2xl shadow-black/60">
        <div class="flex items-center gap-3">
          <Art :src="current.coverId ? `/covers/${current.coverId}` : undefined" :label="current.title" class="h-12 w-12 shrink-0" />
          <div class="min-w-0">
            <p class="truncate font-bold text-white">{{ current.title }}</p>
            <p class="truncate text-xs font-semibold text-brand">{{ current.singer }}</p>
          </div>
        </div>
        <input type="range" min="0" :max="current.durationMs || 1" :value="shownPosition" step="500" aria-label="Song position" class="mt-3 w-full accent-brand" @input="dragTo" @change="dropAt" />
        <div class="flex justify-between text-[11px] tabular-nums text-ink-300">
          <span>{{ formatTime(shownPosition) }}</span><span>{{ formatTime(current.durationMs) }}</span>
        </div>

        <div class="relative mx-auto mt-4 h-60 w-60 rounded-full bg-ink-950 shadow-inner shadow-black">
          <button :class="pad" class="left-1/2 top-0 h-[4.5rem] w-24 -translate-x-1/2" aria-label="Start over" @click="act('seek', { ms: 0 })"><span class="flex flex-col items-center"><Icon name="restart" :size="22" /><span :class="padLabel">Restart</span></span></button>
          <button :class="pad" class="bottom-0 left-1/2 h-[4.5rem] w-24 -translate-x-1/2" aria-label="Skip song" @click="act('skip')"><span class="flex flex-col items-center"><Icon name="skip" :size="22" /><span :class="padLabel">Skip</span></span></button>
          <button :class="pad" class="left-0 top-1/2 h-24 w-[4.5rem] -translate-y-1/2" aria-label="Back 10 seconds" @click="jump(-SKIP_MS)"><span class="flex flex-col items-center text-base font-bold">-10<span :class="padLabel">sec</span></span></button>
          <button :class="pad" class="right-0 top-1/2 h-24 w-[4.5rem] -translate-y-1/2" aria-label="Forward 10 seconds" @click="jump(SKIP_MS)"><span class="flex flex-col items-center text-base font-bold">+10<span :class="padLabel">sec</span></span></button>
          <button class="absolute left-1/2 top-1/2 grid h-24 w-24 -translate-x-1/2 -translate-y-1/2 place-items-center rounded-full bg-brand text-white shadow-lg shadow-brand/40 transition-transform active:scale-90" :aria-label="paused ? 'Play' : 'Pause'" @click="act(paused ? 'play' : 'pause')"><Icon :name="paused ? 'play' : 'pause'" :size="38" /></button>
        </div>

        <div class="mt-5 grid grid-cols-[4.5rem_1fr] gap-3">
          <div v-if="doc" class="flex flex-col overflow-hidden rounded-full bg-ink-700 shadow-[0_3px_0_rgba(0,0,0,.45)]">
            <button class="grid flex-1 place-items-center py-2 text-white active:bg-ink-500/40" aria-label="Show lyrics later" @pointerdown="startHold(NUDGE_MS)" @pointerup="stopHold" @pointerleave="stopHold" @pointercancel="stopHold"><Icon name="up" :size="22" /></button>
            <div class="py-1 text-center">
              <p class="text-[10px] font-bold uppercase tracking-wide text-ink-300">Lyrics</p>
              <p class="text-xs font-bold tabular-nums text-white">{{ offsetMs > 0 ? '+' : '' }}{{ (offsetMs / 1000).toFixed(1) }}s</p>
            </div>
            <button class="grid flex-1 place-items-center py-2 text-white active:bg-ink-500/40" aria-label="Show lyrics earlier" @pointerdown="startHold(-NUDGE_MS)" @pointerup="stopHold" @pointerleave="stopHold" @pointercancel="stopHold"><Icon name="down" :size="22" /></button>
          </div>
          <div :class="doc ? '' : 'col-span-2'" class="grid grid-cols-2 content-start gap-2.5">
            <button v-if="soundKey" :class="key" @click="act('vocal-mode', { mode: soundKey.next })"><span class="text-[10px] text-ink-300">Sound</span>{{ soundKey.label }}</button>
            <button v-if="doc" :class="key" @click="editing = true"><span class="text-[10px] text-ink-300">Singers</span>Parts</button>
            <button :class="key" @click="flipKaraoke"><span class="text-[10px] text-ink-300">This video</span>{{ current.karaoke ? 'Has vocals' : 'Is karaoke' }}</button>
            <button :class="key" @click="lock"><Icon name="lock" :size="16" />Lock</button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
