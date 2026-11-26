<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useInfiniteQuery, useQuery } from '@tanstack/vue-query'
import InfiniteList from '../shared/InfiniteList.vue'
import SongList from './SongList.vue'
import Shelf from './Shelf.vue'
import Art from './Art.vue'
import Icon from './Icon.vue'
import BrandMark from '../shared/BrandMark.vue'
import { pageFetcher } from './api'
import { useAddSong } from './addSong'
import { useParty } from './party'
import { gradientFor } from './colors'
import type { AlbumRow, ArtistRow, SongRow } from '../shared/types'

type View =
  | { kind: 'home' }
  | { kind: 'artists' | 'albums'; from?: string }
  | { kind: 'songs'; title: string; params: Record<string, string | undefined>; art?: string; label?: string; byline?: string }
  | { kind: 'artist'; id: string; name: string; art?: string }

const party = useParty()
const add = useAddSong()
const stack = ref<View[]>([{ kind: 'home' }])
const view = computed(() => stack.value[stack.value.length - 1])
const LETTERS = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ'.split('')

function open(v: View) {
  stack.value.push(v)
  history.pushState({ depth: stack.value.length }, '')
}

function goBack() {
  history.back()
}

function onPop() {
  if (stack.value.length > 1) stack.value.pop()
}

function jump(letter: string) {
  const v = view.value
  if (v.kind === 'artists' || v.kind === 'albums') {
    stack.value[stack.value.length - 1] = { ...v, from: v.from === letter ? undefined : letter }
  }
}

onMounted(() => window.addEventListener('popstate', onPop))
onBeforeUnmount(() => window.removeEventListener('popstate', onPop))

const anySongs = useQuery({
  queryKey: ['songs', 'any'],
  queryFn: () => pageFetcher<SongRow>('/api/songs', { limit: '1' })(null),
  refetchInterval: 30000,
})
const isEmpty = computed(() => anySongs.data.value?.items.length === 0)

const title = computed(() => {
  const v = view.value
  if (v.kind === 'artists') return 'Singers'
  if (v.kind === 'albums') return 'Albums'
  return v.kind === 'songs' ? v.title : v.kind === 'artist' ? v.name : ''
})

const fromLetter = computed(() => {
  const v = view.value
  return v.kind === 'artists' || v.kind === 'albums' ? v.from : undefined
})

const songThumb = (s: SongRow) => (s.coverId ? `/covers/${s.coverId}` : undefined)
const albumCover = (a: AlbumRow) => (a.hasCover ? `/covers/${a.id}` : undefined)
const artistCover = (a: ArtistRow) => (a.coverId ? `/covers/${a.coverId}` : undefined)

function openAlbum(a: AlbumRow) {
  open({ kind: 'songs', title: a.title, params: { album: a.id }, art: albumCover(a), label: 'Album', byline: a.artist })
}

function openArtist(a: ArtistRow) {
  open({ kind: 'artist', id: a.id, name: a.name, art: artistCover(a) })
}

function openGroup(label: 'Genre' | 'Language', name: string) {
  open({ kind: 'songs', title: name, label, params: label === 'Genre' ? { genre: name } : { language: name } })
}

// Albums strip on a singer's page.
const artistId = computed(() => (view.value.kind === 'artist' ? view.value.id : ''))
const singlesQuery = useInfiniteQuery({
  queryKey: computed(() => ['artist-singles', artistId.value]),
  queryFn: () => pageFetcher<SongRow>('/api/songs', { artist: artistId.value, album: '__singles', limit: '1' })(null),
  initialPageParam: null as string | null,
  getNextPageParam: () => null,
  enabled: computed(() => artistId.value !== ''),
})
const hasSingles = computed(() => (singlesQuery.data.value?.pages[0]?.items.length ?? 0) > 0)

const press = 'text-left transition-transform active:scale-[.97]'
</script>

<template>
  <div v-if="view.kind === 'home'" class="no-scrollbar h-full overflow-y-auto pb-6">
    <header class="bg-gradient-to-b from-brand/30 to-transparent px-4 pb-2 pt-5">
      <BrandMark class="mb-3 text-xl" />
      <h1 class="truncate pr-16 text-3xl font-extrabold tracking-tight text-white">Hi, {{ party.name }}</h1>
      <p class="mt-1 text-ink-300">What do you want to sing?</p>
    </header>

    <div v-if="isEmpty" class="flex flex-col items-center gap-3 p-10 text-center text-ink-300">
      <Icon name="music" :size="40" />
      <p class="text-lg font-semibold text-white">No songs yet</p>
      <p class="text-sm">Find one in Search and it will show up here.</p>
    </div>

    <Shelf title="Sing again" :query-key="['songs', 'shelf', 'history']" :fetch-page="pageFetcher<SongRow>('/api/history', { limit: '20' })" :item-key="(s: SongRow) => s.id">
      <template #default="{ item }">
        <button class="relative block w-36" :class="press" @click="add(item as SongRow)">
          <span v-if="party.queuedIds.has((item as SongRow).id)" class="absolute right-1.5 top-1.5 z-10 grid h-7 w-7 place-items-center rounded-full bg-emerald-500 text-white shadow" aria-label="Added"><Icon name="check" :size="16" /></span>
          <Art :src="songThumb(item as SongRow)" :label="(item as SongRow).title" class="w-full shadow-lg shadow-black/40" />
          <span class="mt-2 block truncate text-sm font-semibold text-white">{{ (item as SongRow).title }}</span>
          <span class="block truncate text-xs text-ink-300">{{ (item as SongRow).artist }}</span>
        </button>
      </template>
    </Shelf>

    <Shelf title="Recently added" :query-key="['songs', 'shelf', 'new']" :fetch-page="pageFetcher<SongRow>('/api/songs', { sort: 'new', limit: '20' })" :item-key="(s: SongRow) => s.id">
      <template #default="{ item }">
        <button class="relative block w-36" :class="press" @click="add(item as SongRow)">
          <span v-if="party.queuedIds.has((item as SongRow).id)" class="absolute right-1.5 top-1.5 z-10 grid h-7 w-7 place-items-center rounded-full bg-emerald-500 text-white shadow" aria-label="Added"><Icon name="check" :size="16" /></span>
          <Art :src="songThumb(item as SongRow)" :label="(item as SongRow).title" class="w-full shadow-lg shadow-black/40" />
          <span class="mt-2 block truncate text-sm font-semibold text-white">{{ (item as SongRow).title }}</span>
          <span class="block truncate text-xs text-ink-300">{{ (item as SongRow).artist }}</span>
        </button>
      </template>
    </Shelf>

    <Shelf title="Most sung" :query-key="['songs', 'shelf', 'popular']" :fetch-page="pageFetcher<SongRow>('/api/songs', { sort: 'popular', limit: '20' })" :item-key="(s: SongRow) => s.id" can-see-all @see-all="open({ kind: 'songs', title: 'Most sung', params: { sort: 'popular' } })">
      <template #default="{ item }">
        <button class="relative block w-36" :class="press" @click="add(item as SongRow)">
          <span v-if="party.queuedIds.has((item as SongRow).id)" class="absolute right-1.5 top-1.5 z-10 grid h-7 w-7 place-items-center rounded-full bg-emerald-500 text-white shadow" aria-label="Added"><Icon name="check" :size="16" /></span>
          <Art :src="songThumb(item as SongRow)" :label="(item as SongRow).title" class="w-full shadow-lg shadow-black/40" />
          <span class="mt-2 block truncate text-sm font-semibold text-white">{{ (item as SongRow).title }}</span>
          <span class="block truncate text-xs text-ink-300">{{ (item as SongRow).artist }}</span>
        </button>
      </template>
    </Shelf>

    <Shelf title="Albums" :query-key="['albums', 'shelf']" :fetch-page="pageFetcher<AlbumRow>('/api/albums', { limit: '20' })" :item-key="(a: AlbumRow) => a.id" can-see-all @see-all="open({ kind: 'albums' })">
      <template #default="{ item }">
        <button class="block w-36" :class="press" @click="openAlbum(item as AlbumRow)">
          <Art :src="albumCover(item as AlbumRow)" :label="(item as AlbumRow).title" class="w-full shadow-lg shadow-black/40" />
          <span class="mt-2 block truncate text-sm font-semibold text-white">{{ (item as AlbumRow).title }}</span>
          <span class="block truncate text-xs text-ink-300">{{ (item as AlbumRow).artist }}</span>
        </button>
      </template>
    </Shelf>

    <Shelf title="Singers" :query-key="['artists', 'shelf']" :fetch-page="pageFetcher<ArtistRow>('/api/artists', { limit: '20' })" :item-key="(a: ArtistRow) => a.id" can-see-all @see-all="open({ kind: 'artists' })">
      <template #default="{ item }">
        <button class="block w-28" :class="press" @click="openArtist(item as ArtistRow)">
          <Art :src="artistCover(item as ArtistRow)" :label="(item as ArtistRow).name" round class="w-full shadow-lg shadow-black/40" />
          <span class="mt-2 block truncate text-center text-sm font-semibold text-white">{{ (item as ArtistRow).name }}</span>
        </button>
      </template>
    </Shelf>

    <Shelf title="Genres" :query-key="['genres', 'shelf']" :fetch-page="pageFetcher<string>('/api/genres', { limit: '20' })" :item-key="(g: string) => g">
      <template #default="{ item }">
        <button class="flex h-20 w-40 items-end rounded-md p-3 text-lg font-bold capitalize text-white" :class="press" :style="{ background: gradientFor(item as string) }" @click="openGroup('Genre', item as string)"><span class="truncate">{{ item }}</span></button>
      </template>
    </Shelf>

    <Shelf title="Languages" :query-key="['languages', 'shelf']" :fetch-page="pageFetcher<string>('/api/languages', { limit: '20' })" :item-key="(l: string) => l">
      <template #default="{ item }">
        <button class="flex h-20 w-40 items-end rounded-md p-3 text-lg font-bold text-white" :class="press" :style="{ background: gradientFor(item as string) }" @click="openGroup('Language', item as string)"><span class="truncate">{{ item }}</span></button>
      </template>
    </Shelf>
  </div>

  <div v-else class="flex h-full flex-col">
    <header class="flex items-center gap-2 px-2 pb-1 pt-3">
      <button class="grid h-10 w-10 shrink-0 place-items-center rounded-full text-white" aria-label="Back" @click="goBack"><Icon name="back" /></button>
      <h1 v-if="view.kind === 'artists' || view.kind === 'albums'" class="truncate text-2xl font-extrabold tracking-tight text-white">{{ title }}</h1>
    </header>

    <div v-if="view.kind === 'songs' || view.kind === 'artist'" class="flex items-end gap-4 bg-gradient-to-b from-brand/25 to-transparent px-4 pb-4">
      <Art :src="view.art" :label="title" :round="view.kind === 'artist'" class="h-28 w-28 shrink-0 shadow-xl shadow-black/50" />
      <div class="min-w-0 pb-1">
        <p class="text-xs font-semibold text-ink-100">{{ view.kind === 'artist' ? 'Singer' : view.label ?? 'Songs' }}</p>
        <h2 class="line-clamp-2 text-2xl font-extrabold leading-tight tracking-tight text-white">{{ title }}</h2>
        <p v-if="view.kind === 'songs' && view.byline" class="mt-1 truncate text-sm text-ink-300">{{ view.byline }}</p>
      </div>
    </div>

    <div class="relative min-h-0 flex-1">
      <InfiniteList v-if="view.kind === 'artists'" :query-key="['artists', fromLetter]" :fetch-page="pageFetcher<ArtistRow>('/api/artists', { from: fromLetter })" :row-height="68" :item-key="(a: ArtistRow) => a.id">
        <template #default="{ item }">
          <button class="flex min-w-0 items-center gap-3 pr-6" :class="press" @click="openArtist(item as ArtistRow)">
            <Art :src="artistCover(item as ArtistRow)" :label="(item as ArtistRow).name" round class="h-12 w-12 shrink-0" />
            <span class="truncate font-medium text-white">{{ (item as ArtistRow).name }}</span>
          </button>
        </template>
      </InfiniteList>

      <InfiniteList v-else-if="view.kind === 'albums'" :query-key="['albums', fromLetter]" :fetch-page="pageFetcher<AlbumRow>('/api/albums', { from: fromLetter })" :row-height="216" :columns="2" :item-key="(a: AlbumRow) => a.id">
        <template #default="{ item }">
          <button class="min-w-0 pr-3" :class="press" @click="openAlbum(item as AlbumRow)">
            <Art :src="albumCover(item as AlbumRow)" :label="(item as AlbumRow).title" class="w-full shadow-lg shadow-black/40" />
            <span class="mt-2 block truncate text-sm font-semibold text-white">{{ (item as AlbumRow).title }}</span>
            <span class="block truncate text-xs text-ink-300">{{ (item as AlbumRow).artist }}</span>
          </button>
        </template>
      </InfiniteList>

      <SongList v-else-if="view.kind === 'songs'" :params="view.params" />

      <div v-else-if="view.kind === 'artist'" class="flex h-full flex-col">
        <Shelf title="Albums" :query-key="['artist-albums', artistId]" :fetch-page="pageFetcher<AlbumRow>('/api/albums', { artist: artistId })" :item-key="(a: AlbumRow) => a.id" class="!mt-0 pb-3">
          <template #default="{ item }">
            <button class="block w-24" :class="press" @click="openAlbum(item as AlbumRow)">
              <Art :src="albumCover(item as AlbumRow)" :label="(item as AlbumRow).title" class="w-full" />
              <span class="mt-1 block truncate text-xs font-semibold text-white">{{ (item as AlbumRow).title }}</span>
            </button>
          </template>
        </Shelf>
        <button v-if="hasSingles" class="mx-4 mb-2 self-start rounded-full bg-ink-800 px-4 py-1.5 text-sm font-semibold text-ink-100" @click="open({ kind: 'songs', title: 'Singles', label: view.name, params: { artist: view.id, album: '__singles' } })">Singles</button>
        <div class="min-h-0 flex-1"><SongList :params="{ artist: view.id }" /></div>
      </div>

      <div v-if="view.kind === 'artists' || view.kind === 'albums'" class="absolute bottom-0 right-0 top-0 flex w-5 flex-col items-center justify-between py-1 text-[10px] font-bold text-ink-300">
        <button v-for="l in LETTERS" :key="l" :class="{ 'text-brand': fromLetter === l }" :aria-label="`Jump to ${l}`" @click="jump(l)">{{ l }}</button>
      </div>
    </div>
  </div>
</template>
