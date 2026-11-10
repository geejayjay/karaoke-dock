<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useQueryClient } from '@tanstack/vue-query'
import InfiniteList from '../shared/InfiniteList.vue'
import Art from '../remote/Art.vue'
import StatusChips from './StatusChips.vue'
import TimingCheck from './TimingCheck.vue'
import { api, pageFetcher, send } from './api'
import { REFRESH_MS, statusLine, type SongArtist, type StatusRow } from './status'
import type { AlbumRow, ArtistRow, SongRow } from '../shared/types'

type ViewMode = 'songs' | 'artists' | 'albums'

interface IdentifyMatch {
  recordingId: string
  title: string
  artists?: { mbid?: string; name: string; gender?: string }[]
  album?: string
  releaseGroupId?: string
  year?: number
  genre?: string
  language?: string
}

interface SongDetail {
  id: string
  title: string
  artist: string
  albumId: string
  album: string
  genre: string
  language: string
  coverId?: string
  artists?: SongArtist[]
}

const qc = useQueryClient()
const view = ref<ViewMode>('songs')
const text = ref('')
const q = ref('')
const missing = ref(false)
const unidentified = ref(false)
const singlesOnly = ref(false)
const activeArtist = ref<ArtistRow | null>(null)
const activeAlbum = ref<AlbumRow | null>(null)
const coverStamp = ref(Date.now())

const editing = ref<null | {
  id: string
  title: string
  artist: string
  albumId: string
  album: string
  genre: string
  language: string
  coverId?: string
  refreshLyrics: boolean
}>(null)

const matches = ref<IdentifyMatch[] | null>(null)
const searchingMatches = ref(false)
const reidentifying = ref(false)
const editingArtist = ref<null | { id: string; name: string; gender: string; coverId?: string; hasCustom?: boolean }>(null)
const editingAlbum = ref<null | { id: string; title: string; artist: string; year: number; hasCover: boolean }>(null)

const message = ref('')
const checking = ref<StatusRow | null>(null)
const uploading = ref(false)
const uploadingCover = ref(false)

let timer = 0
watch(text, (v) => {
  window.clearTimeout(timer)
  timer = window.setTimeout(() => (q.value = v.trim()), 250)
})

const songParams = computed(() => ({
  q: q.value || undefined,
  artist: activeArtist.value?.id || undefined,
  album: singlesOnly.value ? '__singles' : activeAlbum.value?.id || undefined,
  missing: missing.value ? '1' : undefined,
  unidentified: unidentified.value ? '1' : undefined,
}))

const artistParams = computed(() => ({ q: q.value || undefined }))
const albumParams = computed(() => ({
  q: q.value || undefined,
  artist: activeArtist.value?.id || undefined,
}))

function coverUrl(id?: string) {
  return id ? `/covers/${id}?v=${coverStamp.value}` : undefined
}

function switchView(next: ViewMode) {
  view.value = next
  if (next !== 'songs') {
    missing.value = false
    unidentified.value = false
    singlesOnly.value = false
  }
}

function selectArtist(a: ArtistRow) {
  activeArtist.value = a
  activeAlbum.value = null
  singlesOnly.value = false
  view.value = 'songs'
}

function selectAlbum(a: AlbumRow) {
  activeAlbum.value = a
  singlesOnly.value = false
  view.value = 'songs'
}

async function filterByArtist(a: SongArtist) {
  try {
    const full = await api<ArtistRow>(`/api/admin/artists/${a.id}`)
    selectArtist(full)
  } catch {
    selectArtist({ id: a.id, name: a.name, gender: a.gender })
  }
}

async function filterByAlbum(albumId?: string, albumTitle?: string) {
  if (!albumId) return
  try {
    const full = await api<AlbumRow>(`/api/admin/albums/${albumId}`)
    selectAlbum(full)
  } catch {
    selectAlbum({ id: albumId, title: albumTitle ?? 'Album', artistId: '', artist: '', hasCover: false })
  }
}

function clearFilter() {
  activeArtist.value = null
  activeAlbum.value = null
  singlesOnly.value = false
}

async function refreshAll() {
  coverStamp.value = Date.now()
  await Promise.all([
    qc.invalidateQueries({ queryKey: ['admin-songs'] }),
    qc.invalidateQueries({ queryKey: ['admin-artists'] }),
    qc.invalidateQueries({ queryKey: ['admin-albums'] }),
  ])
  if (activeArtist.value) {
    api<ArtistRow>(`/api/admin/artists/${activeArtist.value.id}`)
      .then((a) => (activeArtist.value = a))
      .catch(() => {})
  }
  if (activeAlbum.value) {
    api<AlbumRow>(`/api/admin/albums/${activeAlbum.value.id}`)
      .then((a) => (activeAlbum.value = a))
      .catch(() => {})
  }
}

async function run(fn: () => Promise<unknown>, done: string) {
  message.value = ''
  try {
    await fn()
    message.value = done
    await refreshAll()
  } catch (e) {
    message.value = (e as Error).message
  }
}

async function edit(s: SongRow) {
  matches.value = null
  const full = await api<SongDetail>(`/api/admin/songs/${s.id}`)
  editing.value = {
    id: full.id,
    title: full.title,
    artist: full.artist,
    albumId: full.albumId,
    album: full.album,
    genre: full.genre,
    language: full.language,
    coverId: full.coverId,
    refreshLyrics: false,
  }
}

async function save() {
  const e = editing.value
  if (!e) return
  await run(() => send('PATCH', `/api/admin/songs/${e.id}`, e), 'Saved.')
  editing.value = null
  matches.value = null
}

async function findMatches() {
  const e = editing.value
  if (!e) return
  searchingMatches.value = true
  message.value = ''
  try {
    const qs = new URLSearchParams()
    if (e.title.trim()) qs.set('title', e.title.trim())
    if (e.artist.trim()) qs.set('artist', e.artist.trim())
    const res = await api<{ items: IdentifyMatch[] }>(`/api/admin/songs/${e.id}/identify?${qs.toString()}`)
    matches.value = res.items
  } catch (err) {
    message.value = (err as Error).message
  } finally {
    searchingMatches.value = false
  }
}

async function applyReidentify(match?: IdentifyMatch) {
  const e = editing.value
  if (!e) return
  reidentifying.value = true
  message.value = ''
  try {
    const body = match
      ? {
          recordingId: match.recordingId,
          releaseGroupId: match.releaseGroupId ?? '',
          refreshLyrics: true,
        }
      : {
          title: e.title.trim(),
          artist: e.artist.trim(),
          refreshLyrics: true,
        }
    const updated = await send<SongDetail>('POST', `/api/admin/songs/${e.id}/reidentify`, body)
    editing.value = {
      id: updated.id,
      title: updated.title,
      artist: updated.artist,
      albumId: updated.albumId,
      album: updated.album,
      genre: updated.genre,
      language: updated.language,
      coverId: updated.coverId,
      refreshLyrics: false,
    }
    matches.value = null
    message.value = 'Re-identified song details and cover.'
    await refreshAll()
  } catch (err) {
    message.value = (err as Error).message
  } finally {
    reidentifying.value = false
  }
}

async function uploadSongCover(ev: Event) {
  const el = ev.target as HTMLInputElement
  const file = el.files?.[0]
  const e = editing.value
  if (!file || !e) return
  uploadingCover.value = true
  try {
    const form = new FormData()
    form.append('file', file)
    const res = await api<{ coverId: string }>(`/api/admin/songs/${e.id}/cover`, { method: 'POST', body: form })
    e.coverId = res.coverId
    e.albumId = res.coverId
    if (!e.album) e.album = e.title
    message.value = 'Cover updated.'
    await refreshAll()
  } catch (err) {
    message.value = (err as Error).message
  } finally {
    uploadingCover.value = false
    el.value = ''
  }
}

function openArtistEdit(a: ArtistRow) {
  editingArtist.value = {
    id: a.id,
    name: a.name,
    gender: a.gender ?? '',
    coverId: a.coverId,
    hasCustom: a.hasCustom,
  }
}

async function saveArtist() {
  const a = editingArtist.value
  if (!a) return
  await run(() => send('PATCH', `/api/admin/artists/${a.id}`, { name: a.name, gender: a.gender }), 'Singer saved.')
  editingArtist.value = null
}

async function uploadArtistCover(ev: Event) {
  const el = ev.target as HTMLInputElement
  const file = el.files?.[0]
  const a = editingArtist.value
  if (!file || !a) return
  uploadingCover.value = true
  try {
    const form = new FormData()
    form.append('file', file)
    const res = await api<{ coverId: string }>(`/api/admin/artists/${a.id}/cover`, { method: 'POST', body: form })
    a.coverId = res.coverId
    a.hasCustom = true
    message.value = 'Singer image updated.'
    await refreshAll()
  } catch (err) {
    message.value = (err as Error).message
  } finally {
    uploadingCover.value = false
    el.value = ''
  }
}

async function resetArtistCover() {
  const a = editingArtist.value
  if (!a) return
  try {
    const updated = await send<ArtistRow>('DELETE', `/api/admin/artists/${a.id}/cover`)
    a.coverId = updated.coverId
    a.hasCustom = updated.hasCustom
    message.value = 'Singer image reset.'
    await refreshAll()
  } catch (err) {
    message.value = (err as Error).message
  }
}

function openAlbumEdit(a: AlbumRow) {
  editingAlbum.value = {
    id: a.id,
    title: a.title,
    artist: a.artist,
    year: a.year ?? 0,
    hasCover: a.hasCover,
  }
}

async function saveAlbum() {
  const a = editingAlbum.value
  if (!a) return
  await run(() => send('PATCH', `/api/admin/albums/${a.id}`, { title: a.title, year: Number(a.year) || 0 }), 'Album saved.')
  editingAlbum.value = null
}

async function uploadAlbumCover(ev: Event) {
  const el = ev.target as HTMLInputElement
  const file = el.files?.[0]
  const a = editingAlbum.value
  if (!file || !a) return
  uploadingCover.value = true
  try {
    const form = new FormData()
    form.append('file', file)
    await api(`/api/admin/albums/${a.id}/cover`, { method: 'POST', body: form })
    a.hasCover = true
    message.value = 'Album cover updated.'
    await refreshAll()
  } catch (err) {
    message.value = (err as Error).message
  } finally {
    uploadingCover.value = false
    el.value = ''
  }
}

async function removeAlbumCover() {
  const a = editingAlbum.value
  if (!a) return
  try {
    await send('DELETE', `/api/admin/albums/${a.id}/cover`)
    a.hasCover = false
    message.value = 'Album cover removed.'
    await refreshAll()
  } catch (err) {
    message.value = (err as Error).message
  }
}

async function addFile(e: Event) {
  const el = e.target as HTMLInputElement
  const file = el.files?.[0]
  if (!file) return
  uploading.value = true
  const form = new FormData()
  form.append('file', file)
  await run(() => api('/api/admin/songs/upload', { method: 'POST', body: form }), 'Added. Processing in the background.')
  uploading.value = false
  el.value = ''
}

function reprocess(s: SongRow) {
  void run(() => send('POST', `/api/admin/songs/${s.id}/reprocess`), 'Reprocessing.')
}

function remove(s: SongRow) {
  if (!confirm(`Delete "${s.title}"? This can't be undone.`)) return
  void run(() => send('DELETE', `/api/admin/songs/${s.id}`), 'Deleted.')
}

const searchPlaceholder = computed(() => {
  if (view.value === 'artists') return 'Search singers'
  if (view.value === 'albums') return 'Search albums'
  return 'Search songs, singers or albums'
})

const input = 'w-full rounded-lg bg-ink-800 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-brand'
const chipBtn = 'rounded-lg px-3 py-1.5 text-xs font-medium transition-colors'
</script>

<template>
  <div class="flex h-[calc(100dvh-11rem)] flex-col gap-3 md:h-[calc(100dvh-9.5rem)]">
    <div class="flex flex-wrap items-center gap-2.5">
      <div class="flex rounded-lg border border-ink-800 bg-ink-900 p-0.5">
        <button
          v-for="m in (['songs', 'artists', 'albums'] as const)"
          :key="m"
          class="rounded-md px-3 py-1.5 text-sm font-medium capitalize transition-colors"
          :class="view === m ? 'bg-ink-700 text-white' : 'text-ink-300 hover:text-white'"
          @click="switchView(m)"
        >
          {{ m === 'artists' ? 'Singers' : m }}
        </button>
      </div>

      <input v-model="text" type="search" :placeholder="searchPlaceholder" :class="input" class="max-w-xs" />

      <template v-if="view === 'songs'">
        <button
          :class="[chipBtn, unidentified ? 'bg-amber-500/20 text-amber-200 ring-1 ring-amber-500/40' : 'bg-ink-800 text-ink-300 hover:text-white']"
          @click="unidentified = !unidentified"
        >
          Unidentified
        </button>
        <button
          :class="[chipBtn, singlesOnly ? 'bg-brand/20 text-white ring-1 ring-brand' : 'bg-ink-800 text-ink-300 hover:text-white']"
          @click="singlesOnly = !singlesOnly; if (singlesOnly) activeAlbum = null"
        >
          No album
        </button>
        <button
          :class="[chipBtn, missing ? 'bg-red-500/20 text-red-200 ring-1 ring-red-500/40' : 'bg-ink-800 text-ink-300 hover:text-white']"
          @click="missing = !missing"
        >
          Missing audio
        </button>
      </template>

      <div class="ml-auto flex items-center gap-3">
        <span v-if="message" class="text-sm text-ink-300">{{ message }}</span>
        <label class="cursor-pointer rounded-lg bg-brand px-3.5 py-2 text-sm font-bold text-white">
          {{ uploading ? 'Adding…' : 'Add song file' }}
          <input type="file" accept="audio/*,video/*" class="hidden" :disabled="uploading" @change="addFile" />
        </label>
      </div>
    </div>

    <!-- Active Singer / Album Filter Bar -->
    <div v-if="activeArtist || activeAlbum" class="flex items-center justify-between gap-3 rounded-xl border border-ink-800 bg-ink-900 px-3.5 py-2.5">
      <div v-if="activeArtist" class="flex min-w-0 items-center gap-3">
        <Art :src="coverUrl(activeArtist.coverId)" :label="activeArtist.name" round class="h-10 w-10 shrink-0" />
        <div class="min-w-0">
          <div class="flex items-center gap-2">
            <span class="truncate font-bold">{{ activeArtist.name }}</span>
            <span v-if="activeArtist.gender" class="rounded bg-ink-800 px-1.5 py-0.5 text-[11px] capitalize text-ink-300">{{ activeArtist.gender }}</span>
          </div>
          <p class="text-xs text-ink-300">Showing songs by this singer</p>
        </div>
      </div>
      <div v-else-if="activeAlbum" class="flex min-w-0 items-center gap-3">
        <Art :src="activeAlbum.hasCover ? coverUrl(activeAlbum.id) : undefined" :label="activeAlbum.title" class="h-10 w-10 shrink-0" />
        <div class="min-w-0">
          <div class="flex items-center gap-2">
            <span class="truncate font-bold">{{ activeAlbum.title }}</span>
            <span v-if="activeAlbum.year" class="text-xs text-ink-300">({{ activeAlbum.year }})</span>
          </div>
          <p class="truncate text-xs text-ink-300">{{ activeAlbum.artist || 'Album' }}</p>
        </div>
      </div>
      <div class="flex shrink-0 items-center gap-2">
        <button v-if="activeArtist" class="rounded-lg bg-ink-700 px-3 py-1.5 text-xs font-medium" @click="openArtistEdit(activeArtist)">Edit singer</button>
        <button v-if="activeAlbum" class="rounded-lg bg-ink-700 px-3 py-1.5 text-xs font-medium" @click="openAlbumEdit(activeAlbum)">Edit album</button>
        <button class="rounded-lg bg-ink-800 px-3 py-1.5 text-xs text-ink-300 hover:text-white" @click="clearFilter">All songs</button>
      </div>
    </div>

    <!-- Songs View -->
    <div v-if="view === 'songs'" class="min-h-0 flex-1 overflow-hidden rounded-2xl border border-ink-800 bg-ink-900 pt-1">
      <InfiniteList
        :query-key="['admin-songs', songParams]"
        :fetch-page="pageFetcher<StatusRow>('/api/admin/songs', songParams)"
        :row-height="96"
        :item-key="(s: StatusRow) => s.id"
        empty="No matching songs."
        :refetch-ms="REFRESH_MS"
      >
        <template #default="{ item }">
          <div class="flex h-full min-w-0 items-center gap-3">
            <button class="group relative h-12 w-12 shrink-0 overflow-hidden rounded-md" title="Edit song & cover" @click="edit(item as StatusRow)">
              <Art :src="coverUrl((item as StatusRow).coverId)" :label="(item as StatusRow).title" class="h-full w-full" />
            </button>
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2">
                <p class="truncate font-semibold">{{ (item as StatusRow).title }}</p>
                <span v-if="!(item as StatusRow).artist || !(item as StatusRow).album" class="shrink-0 rounded bg-amber-500/15 px-1.5 py-0.5 text-[11px] font-medium text-amber-200">
                  {{ !(item as StatusRow).artist ? 'No singer' : 'No album' }}
                </span>
              </div>
              <div class="flex min-w-0 flex-wrap items-center gap-x-1.5 text-sm text-ink-300">
                <template v-if="(item as StatusRow).artists?.length">
                  <template v-for="(a, idx) in (item as StatusRow).artists" :key="a.id">
                    <button class="truncate hover:text-white hover:underline" @click="filterByArtist(a)">{{ a.name }}</button><span v-if="idx + 1 < ((item as StatusRow).artists?.length ?? 0)">,</span>
                  </template>
                </template>
                <span v-else-if="(item as StatusRow).artist" class="truncate">{{ (item as StatusRow).artist }}</span>

                <template v-if="(item as StatusRow).album">
                  <span class="text-ink-500">·</span>
                  <button class="truncate hover:text-white hover:underline" @click="filterByAlbum((item as StatusRow).albumId, (item as StatusRow).album)">
                    {{ (item as StatusRow).album }}
                  </button>
                </template>

                <template v-if="(item as StatusRow).genre">
                  <span class="text-ink-500">·</span>
                  <span class="truncate text-xs">{{ (item as StatusRow).genre }}</span>
                </template>
                <template v-if="(item as StatusRow).language">
                  <span class="text-ink-500">·</span>
                  <span class="truncate text-xs">{{ (item as StatusRow).language }}</span>
                </template>
              </div>
              <div class="mt-1 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                <StatusChips :audio="(item as StatusRow).audio" :lyrics="(item as StatusRow).lyrics" :timed-lines="(item as StatusRow).timedLines" :lyric-lines="(item as StatusRow).lyricLines" />
                <span class="min-w-0 truncate text-xs" :class="(item as StatusRow).overall === 'problem' ? 'text-red-300' : 'text-ink-300'">{{ statusLine(item as StatusRow) }}</span>
              </div>
            </div>
            <div class="flex w-44 shrink-0 flex-wrap justify-end gap-1 sm:w-auto sm:flex-nowrap sm:gap-1.5">
              <button v-if="(item as StatusRow).hasLyrics" class="rounded-lg bg-ink-700 px-2.5 py-1.5 text-xs font-medium hover:bg-ink-500/40" @click="checking = item as StatusRow">Check</button>
              <button class="rounded-lg bg-ink-700 px-2.5 py-1.5 text-xs font-medium hover:bg-ink-500/40" @click="edit(item as StatusRow)">Edit</button>
              <button class="rounded-lg bg-ink-700 px-2.5 py-1.5 text-xs font-medium hover:bg-ink-500/40" @click="reprocess(item as StatusRow)">Reprocess</button>
              <button class="rounded-lg bg-ink-700 px-2.5 py-1.5 text-xs font-medium text-red-300 hover:bg-ink-500/40" @click="remove(item as StatusRow)">Delete</button>
            </div>
          </div>
        </template>
      </InfiniteList>
    </div>

    <!-- Singers View -->
    <div v-else-if="view === 'artists'" class="min-h-0 flex-1 overflow-hidden rounded-2xl border border-ink-800 bg-ink-900 pt-1">
      <InfiniteList
        :query-key="['admin-artists', artistParams]"
        :fetch-page="pageFetcher<ArtistRow>('/api/admin/artists', artistParams)"
        :row-height="68"
        :item-key="(a: ArtistRow) => a.id"
        empty="No singers found."
      >
        <template #default="{ item }">
          <div class="flex h-full min-w-0 items-center gap-3">
            <button class="h-11 w-11 shrink-0" @click="openArtistEdit(item as ArtistRow)">
              <Art :src="coverUrl((item as ArtistRow).coverId)" :label="(item as ArtistRow).name" round class="h-full w-full" />
            </button>
            <button class="min-w-0 flex-1 text-left" @click="selectArtist(item as ArtistRow)">
              <div class="flex items-center gap-2">
                <p class="truncate font-semibold hover:underline">{{ (item as ArtistRow).name }}</p>
                <span v-if="(item as ArtistRow).gender" class="rounded bg-ink-800 px-1.5 py-0.5 text-[11px] capitalize text-ink-300">{{ (item as ArtistRow).gender }}</span>
              </div>
              <p class="text-xs text-ink-300">{{ (item as ArtistRow).songCount ?? 0 }} {{ (item as ArtistRow).songCount === 1 ? 'song' : 'songs' }}</p>
            </button>
            <div class="flex shrink-0 gap-1.5">
              <button class="rounded-lg bg-ink-700 px-3 py-1.5 text-xs font-medium hover:bg-ink-500/40" @click="selectArtist(item as ArtistRow)">Songs</button>
              <button class="rounded-lg bg-ink-700 px-3 py-1.5 text-xs font-medium hover:bg-ink-500/40" @click="openArtistEdit(item as ArtistRow)">Edit</button>
            </div>
          </div>
        </template>
      </InfiniteList>
    </div>

    <!-- Albums View -->
    <div v-else-if="view === 'albums'" class="min-h-0 flex-1 overflow-hidden rounded-2xl border border-ink-800 bg-ink-900 pt-1">
      <InfiniteList
        :query-key="['admin-albums', albumParams]"
        :fetch-page="pageFetcher<AlbumRow>('/api/admin/albums', albumParams)"
        :row-height="72"
        :item-key="(a: AlbumRow) => a.id"
        empty="No albums found."
      >
        <template #default="{ item }">
          <div class="flex h-full min-w-0 items-center gap-3">
            <button class="h-12 w-12 shrink-0" @click="openAlbumEdit(item as AlbumRow)">
              <Art :src="(item as AlbumRow).hasCover ? coverUrl((item as AlbumRow).id) : undefined" :label="(item as AlbumRow).title" class="h-full w-full" />
            </button>
            <button class="min-w-0 flex-1 text-left" @click="selectAlbum(item as AlbumRow)">
              <div class="flex items-center gap-2">
                <p class="truncate font-semibold hover:underline">{{ (item as AlbumRow).title }}</p>
                <span v-if="!(item as AlbumRow).hasCover" class="rounded bg-amber-500/15 px-1.5 py-0.5 text-[11px] text-amber-200">No cover</span>
              </div>
              <p class="truncate text-xs text-ink-300">
                {{ (item as AlbumRow).artist || 'Unknown singer' }}
                <template v-if="(item as AlbumRow).year"> · {{ (item as AlbumRow).year }}</template>
                · {{ (item as AlbumRow).songCount ?? 0 }} {{ (item as AlbumRow).songCount === 1 ? 'song' : 'songs' }}
              </p>
            </button>
            <div class="flex shrink-0 gap-1.5">
              <button class="rounded-lg bg-ink-700 px-3 py-1.5 text-xs font-medium hover:bg-ink-500/40" @click="selectAlbum(item as AlbumRow)">Songs</button>
              <button class="rounded-lg bg-ink-700 px-3 py-1.5 text-xs font-medium hover:bg-ink-500/40" @click="openAlbumEdit(item as AlbumRow)">Edit</button>
            </div>
          </div>
        </template>
      </InfiniteList>
    </div>

    <TimingCheck v-if="checking" :song="checking" @close="checking = null" />

    <!-- Edit Song & Re-identify Modal -->
    <div v-if="editing" class="fixed inset-0 z-10 grid place-items-center overflow-y-auto bg-black/60 p-4">
      <form class="w-full max-w-lg space-y-3.5 rounded-2xl border border-ink-800 bg-ink-900 p-5" @submit.prevent="save">
        <div class="flex items-start justify-between gap-3">
          <div class="flex items-center gap-3.5">
            <div class="relative h-16 w-16 shrink-0">
              <Art :src="coverUrl(editing.coverId)" :label="editing.title || 'Song'" class="h-full w-full" />
            </div>
            <div class="space-y-1.5">
              <h3 class="text-lg font-bold leading-tight">Song details</h3>
              <label class="inline-block cursor-pointer rounded-lg bg-ink-700 px-3 py-1 text-xs font-medium hover:bg-ink-500/40">
                {{ uploadingCover ? 'Uploading…' : 'Change image' }}
                <input type="file" accept="image/*" class="hidden" :disabled="uploadingCover" @change="uploadSongCover" />
              </label>
            </div>
          </div>
          <div class="flex flex-wrap justify-end gap-1.5">
            <button
              type="button"
              class="rounded-lg bg-ink-700 px-3 py-1.5 text-xs font-medium hover:bg-ink-500/40"
              :disabled="searchingMatches || reidentifying"
              @click="findMatches"
            >
              {{ searchingMatches ? 'Searching…' : 'Find match' }}
            </button>
            <button
              type="button"
              class="rounded-lg bg-brand/20 px-3 py-1.5 text-xs font-semibold text-white ring-1 ring-brand/50 hover:bg-brand/30"
              :disabled="reidentifying"
              @click="applyReidentify()"
            >
              {{ reidentifying ? 'Identifying…' : 'Auto re-identify' }}
            </button>
          </div>
        </div>

        <!-- Candidate Matches List -->
        <div v-if="matches !== null" class="max-h-48 space-y-1.5 overflow-y-auto rounded-xl border border-ink-800 bg-ink-950 p-2.5">
          <div class="flex items-center justify-between px-1 text-xs text-ink-300">
            <span>{{ matches.length ? 'Pick a match to fill details, cover & lyrics' : 'No matches found. Try adjusting title or singer below.' }}</span>
            <button type="button" class="text-ink-300 hover:text-white" @click="matches = null">Close</button>
          </div>
          <div
            v-for="m in matches"
            :key="m.recordingId + (m.releaseGroupId ?? '')"
            class="flex items-center justify-between gap-3 rounded-lg bg-ink-900 px-3 py-2 text-sm"
          >
            <div class="min-w-0 flex-1">
              <p class="truncate font-semibold">{{ m.title }}</p>
              <p class="truncate text-xs text-ink-300">
                {{ m.artists?.map((a) => a.name).join(', ') }}
                <template v-if="m.album"> · {{ m.album }}</template>
                <template v-if="m.year"> ({{ m.year }})</template>
                <template v-if="m.language"> · {{ m.language }}</template>
              </p>
            </div>
            <button
              type="button"
              class="shrink-0 rounded-lg bg-brand px-3 py-1 text-xs font-bold text-white"
              :disabled="reidentifying"
              @click="applyReidentify(m)"
            >
              Use
            </button>
          </div>
        </div>

        <label class="block">
          <span class="mb-1 block text-xs font-medium text-ink-300">Title</span>
          <input v-model="editing.title" :class="input" />
        </label>
        <label class="block">
          <span class="mb-1 block text-xs font-medium text-ink-300">Singers (comma between names)</span>
          <input v-model="editing.artist" :class="input" />
        </label>
        <label class="block">
          <span class="mb-1 block text-xs font-medium text-ink-300">Album</span>
          <input v-model="editing.album" :class="input" />
        </label>
        <div class="grid grid-cols-2 gap-3">
          <label class="block">
            <span class="mb-1 block text-xs font-medium text-ink-300">Genre</span>
            <input v-model="editing.genre" :class="input" />
          </label>
          <label class="block">
            <span class="mb-1 block text-xs font-medium text-ink-300">Language</span>
            <input v-model="editing.language" :class="input" />
          </label>
        </div>

        <label class="flex items-center gap-2 pt-1 text-xs text-ink-300">
          <input v-model="editing.refreshLyrics" type="checkbox" class="h-4 w-4 accent-brand" />
          Also look up lyrics again for this title and singer
        </label>

        <div class="flex gap-2.5 pt-1">
          <button class="rounded-lg bg-brand px-5 py-2 text-sm font-bold text-white">Save</button>
          <button type="button" class="rounded-lg bg-ink-700 px-5 py-2 text-sm" @click="editing = null; matches = null">Cancel</button>
        </div>
      </form>
    </div>

    <!-- Edit Singer Modal -->
    <div v-if="editingArtist" class="fixed inset-0 z-10 grid place-items-center bg-black/60 p-4">
      <form class="w-full max-w-sm space-y-4 rounded-2xl border border-ink-800 bg-ink-900 p-5" @submit.prevent="saveArtist">
        <div class="flex items-center gap-3.5">
          <Art :src="coverUrl(editingArtist.coverId)" :label="editingArtist.name || 'Singer'" round class="h-16 w-16 shrink-0" />
          <div class="space-y-1.5">
            <h3 class="text-lg font-bold leading-tight">Edit singer</h3>
            <div class="flex flex-wrap gap-1.5">
              <label class="cursor-pointer rounded-lg bg-ink-700 px-3 py-1 text-xs font-medium hover:bg-ink-500/40">
                {{ uploadingCover ? 'Uploading…' : 'Change image' }}
                <input type="file" accept="image/*" class="hidden" :disabled="uploadingCover" @change="uploadArtistCover" />
              </label>
              <button v-if="editingArtist.hasCustom" type="button" class="rounded-lg bg-ink-800 px-2.5 py-1 text-xs text-ink-300 hover:text-white" @click="resetArtistCover">
                Reset
              </button>
            </div>
          </div>
        </div>
        <label class="block">
          <span class="mb-1 block text-xs font-medium text-ink-300">Name</span>
          <input v-model="editingArtist.name" :class="input" />
        </label>
        <label class="block">
          <span class="mb-1 block text-xs font-medium text-ink-300">Voice for duets</span>
          <select v-model="editingArtist.gender" :class="input">
            <option value="">Not set</option>
            <option value="male">Male (Boy)</option>
            <option value="female">Female (Girl)</option>
          </select>
        </label>
        <div class="flex gap-2.5">
          <button class="rounded-lg bg-brand px-5 py-2 text-sm font-bold text-white">Save</button>
          <button type="button" class="rounded-lg bg-ink-700 px-5 py-2 text-sm" @click="editingArtist = null">Cancel</button>
        </div>
      </form>
    </div>

    <!-- Edit Album Modal -->
    <div v-if="editingAlbum" class="fixed inset-0 z-10 grid place-items-center bg-black/60 p-4">
      <form class="w-full max-w-sm space-y-4 rounded-2xl border border-ink-800 bg-ink-900 p-5" @submit.prevent="saveAlbum">
        <div class="flex items-center gap-3.5">
          <Art :src="editingAlbum.hasCover ? coverUrl(editingAlbum.id) : undefined" :label="editingAlbum.title || 'Album'" class="h-16 w-16 shrink-0" />
          <div class="space-y-1.5">
            <h3 class="text-lg font-bold leading-tight">Edit album</h3>
            <div class="flex flex-wrap gap-1.5">
              <label class="cursor-pointer rounded-lg bg-ink-700 px-3 py-1 text-xs font-medium hover:bg-ink-500/40">
                {{ uploadingCover ? 'Uploading…' : 'Change cover' }}
                <input type="file" accept="image/*" class="hidden" :disabled="uploadingCover" @change="uploadAlbumCover" />
              </label>
              <button v-if="editingAlbum.hasCover" type="button" class="rounded-lg bg-ink-800 px-2.5 py-1 text-xs text-ink-300 hover:text-white" @click="removeAlbumCover">
                Remove
              </button>
            </div>
          </div>
        </div>
        <label class="block">
          <span class="mb-1 block text-xs font-medium text-ink-300">Album title</span>
          <input v-model="editingAlbum.title" :class="input" />
        </label>
        <label class="block">
          <span class="mb-1 block text-xs font-medium text-ink-300">Year</span>
          <input v-model.number="editingAlbum.year" type="number" min="0" max="3000" placeholder="Optional" :class="input" />
        </label>
        <div class="flex gap-2.5">
          <button class="rounded-lg bg-brand px-5 py-2 text-sm font-bold text-white">Save</button>
          <button type="button" class="rounded-lg bg-ink-700 px-5 py-2 text-sm" @click="editingAlbum = null">Cancel</button>
        </div>
      </form>
    </div>
  </div>
</template>
