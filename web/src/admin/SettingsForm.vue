<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useQueryClient } from '@tanstack/vue-query'
import { ApiError, send } from './api'

export interface Def {
  key: string
  page: string
  label: string
  kind: 'text' | 'bool' | 'choice' | 'number'
  choices?: string[]
  advanced?: boolean
}

interface Friendly {
  label?: string
  hint?: string
  choices?: Record<string, string>
  suffix?: string
}

/** Plain words for each setting. */
const FRIENDLY: Record<string, Friendly> = {
  QR_MODE: { label: 'Guests join using', choices: { lan: 'This Wi-Fi network', domain: 'Your web address', auto: 'Web address when online' } },
  PUBLIC_URL: { label: 'Web address', hint: 'Only if you have one, like https://karaoke.example.com' },
  LAN_IP: { label: 'Network address', hint: 'Leave empty to find it automatically.' },
  UNPROCESSED_AUDIO: { label: 'While a song is being prepared', choices: { quick: 'Reduce the vocals quickly', original: 'Play the original song' } },
  DEFAULT_VOCAL_MODE: { label: 'Guide vocals', hint: 'Plays the singer quietly to help people follow along.', choices: { off: 'Off', guide: 'On' } },
  ANIMATION_LEVEL: { label: 'Animations', choices: { auto: 'Automatic', full: 'Full', lite: 'Light, for slower TVs' } },
  COUNTDOWN: { label: 'Countdown before each song', hint: 'Shows the song and QR code so the singer can get ready.' },
  COUNTDOWN_SECONDS: { label: 'Countdown length', suffix: 'seconds' },
  PAUSE_IMPORTS: { label: 'Pause new songs', hint: 'Stops processing until you turn this off.' },
  ML_DEVICE: { label: 'Use for vocal removal', choices: { auto: 'Automatic', cuda: 'NVIDIA graphics', cpu: 'Main processor', openvino: 'Intel graphics' } },
  SEPARATION_MODEL: { label: 'Vocal removal model' },
  WHISPER_MODEL: { label: 'Lyric timing quality', choices: { tiny: 'Fastest', base: 'Balanced', small: 'Better', medium: 'Best, but slowest' } },
  VIDEO_ENCODER: { label: 'Video conversion', choices: { auto: 'Automatic', qsv: 'Intel Quick Sync', nvenc: 'NVIDIA graphics', x264: 'Main processor' } },
  ML_THREADS: { label: 'Processor threads', hint: '0 chooses automatically.' },
  MEDIA_ROOT: { label: 'Music folder', hint: 'Use "Move files" below to change this.' },
  TEMP_ROOT: { label: 'Temporary folder', hint: 'Used while songs are processing.' },
  AUDIO_CODEC: { label: 'Audio format', choices: { aac: 'AAC, plays everywhere', opus: 'Opus, smaller files' } },
  AUDIO_BITRATE: { label: 'Audio quality', suffix: 'kbps' },
  KEEP_GUIDE: { label: 'Keep guide vocals', hint: 'Uses more space, and allows guide vocals during songs.' },
  VIDEO_KEEP: { label: 'Keep song videos', choices: { off: 'No, save space', '480p': 'Yes, 480p', '720p': 'Yes, 720p' } },
  ROOM_CODE: { label: 'Room code', hint: 'Part of the QR link. Changing it means guests need to scan again.' },
  HOST_PIN: { label: 'Host PIN', hint: 'Unlocks skip, pause and other host controls on a phone.' },
  QUEUE_LIMIT: { label: 'Songs each person can queue' },
  IMPORT_LIMIT: { label: 'New songs each person can add per hour' },
  ADMIN_REMOTE: { label: 'Allow admin from outside your network' },
  TRUSTED_PROXY: { label: 'Trusted proxy address' },
}

const props = defineProps<{ defs: Def[]; values: Record<string, string>; page: string }>()
const emit = defineEmits<{ restart: [] }>()
const qc = useQueryClient()

// Number boxes give numbers back, so values are compared and sent as text.
const draft = ref<Record<string, string | number>>({})
const text = (key: string) => String(draft.value[key] ?? '')
const errors = ref<Record<string, string>>({})
const message = ref('')
const showAdvanced = ref(false)

watch(() => props.values, (v) => (draft.value = { ...v }), { immediate: true })

const hidden = (d: Def) => d.key === 'COUNTDOWN_SECONDS' && draft.value.COUNTDOWN !== 'true'
const basic = computed(() => props.defs.filter((d) => d.page === props.page && !d.advanced))
const advanced = computed(() => props.defs.filter((d) => d.page === props.page && d.advanced))
const shown = computed(() => basic.value.filter((d) => !hidden(d)))
const dirty = computed(() => [...basic.value, ...advanced.value].some((d) => text(d.key) !== props.values[d.key]))

const saved = computed(() => !dirty.value && message.value === 'Saved')
const note = computed(() => (saved.value ? 'Saved' : message.value && message.value !== 'Saved' ? message.value : 'You have unsaved changes'))

const labelOf = (d: Def) => FRIENDLY[d.key]?.label ?? d.label
const hintOf = (d: Def) => FRIENDLY[d.key]?.hint
const choiceLabel = (d: Def, c: string) => FRIENDLY[d.key]?.choices?.[c] ?? c
const suffixOf = (d: Def) => FRIENDLY[d.key]?.suffix

function toggle(d: Def) {
  draft.value[d.key] = draft.value[d.key] === 'true' ? 'false' : 'true'
  message.value = ''
}

async function save() {
  errors.value = {}
  message.value = ''
  const changes: Record<string, string> = {}
  for (const d of [...basic.value, ...advanced.value]) {
    if (text(d.key) !== props.values[d.key]) changes[d.key] = text(d.key)
  }
  try {
    const res = await send<{ restart: boolean }>('PATCH', '/api/admin/settings', changes)
    await qc.invalidateQueries({ queryKey: ['settings'] })
    message.value = 'Saved'
    if (res.restart) emit('restart')
  } catch (e) {
    const err = e as ApiError
    if (err.fields) {
      errors.value = err.fields
      message.value = 'Please fix the highlighted settings.'
      // Open the advanced settings when the problem is in there.
      if (advanced.value.some((d) => err.fields?.[d.key])) showAdvanced.value = true
    } else message.value = err.message
  }
}

function discard() {
  draft.value = { ...props.values }
  errors.value = {}
  message.value = ''
}

const control = 'w-full rounded-lg border border-ink-700 bg-ink-800 px-3 py-2 outline-none transition-shadow focus:ring-2 focus:ring-brand'
const row = 'flex flex-col gap-2 px-5 py-4 sm:flex-row sm:items-center sm:justify-between sm:gap-8'
</script>

<template>
  <form class="max-w-3xl space-y-4" @submit.prevent="save">
    <template v-for="group in [{ title: '', list: shown, open: true }, { title: 'Advanced', list: advanced, open: showAdvanced }]" :key="group.title">
      <button v-if="group.title && group.list.length" type="button" class="text-sm font-medium text-ink-300 hover:text-white" @click="showAdvanced = !showAdvanced">{{ showAdvanced ? 'Hide advanced settings' : 'Show advanced settings' }}</button>
      <div v-if="group.open && group.list.length" class="divide-y divide-ink-800 rounded-2xl border border-ink-800 bg-ink-900">
        <div v-for="d in group.list" :key="d.key" :class="row">
          <div class="min-w-0 sm:flex-1">
            <p class="font-medium">{{ labelOf(d) }}</p>
            <p v-if="hintOf(d)" class="mt-0.5 text-sm text-ink-300">{{ hintOf(d) }}</p>
            <p v-if="errors[d.key]" class="mt-1 text-sm text-red-300">{{ errors[d.key] }}</p>
          </div>
          <div class="sm:w-72 sm:shrink-0">
            <div v-if="d.kind === 'bool'" class="sm:flex sm:justify-end">
              <button type="button" role="switch" :aria-checked="draft[d.key] === 'true'" :aria-label="labelOf(d)" class="relative h-7 w-12 rounded-full transition-colors" :class="draft[d.key] === 'true' ? 'bg-brand' : 'bg-ink-700'" @click="toggle(d)">
                <span class="absolute left-1 top-1 h-5 w-5 rounded-full bg-white transition-transform" :class="draft[d.key] === 'true' ? 'translate-x-5' : ''" />
              </button>
            </div>
            <select v-else-if="d.kind === 'choice'" v-model="draft[d.key]" :class="control">
              <option v-for="c in d.choices" :key="c" :value="c">{{ choiceLabel(d, c) }}</option>
            </select>
            <div v-else class="flex items-center gap-2">
              <input v-model="draft[d.key]" :type="d.kind === 'number' ? 'number' : 'text'" :disabled="d.key === 'MEDIA_ROOT'" :class="[control, d.key === 'MEDIA_ROOT' && 'opacity-60']" />
              <span v-if="suffixOf(d)" class="shrink-0 text-sm text-ink-300">{{ suffixOf(d) }}</span>
            </div>
          </div>
        </div>
      </div>
    </template>

    <div v-if="dirty || message" class="sticky bottom-3 flex items-center justify-between gap-3 rounded-2xl border border-ink-700 bg-ink-800 px-4 py-3 shadow-lg shadow-black/40">
      <span class="text-sm" :class="saved ? 'text-emerald-300' : note === 'You have unsaved changes' ? 'text-ink-100' : 'text-red-300'">{{ note }}</span>
      <div v-if="dirty" class="flex shrink-0 gap-2">
        <button type="button" class="rounded-lg px-4 py-2 text-sm font-medium text-ink-300 hover:text-white" @click="discard">Discard</button>
        <button class="rounded-lg bg-brand px-5 py-2 text-sm font-bold text-white">Save changes</button>
      </div>
    </div>
  </form>
</template>
