<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import InfiniteList from '../shared/InfiniteList.vue'
import SongList from './SongList.vue'
import SongCard from './SongCard.vue'
import Icon from './Icon.vue'
import { pageFetcher } from './api'
import { useAddToQueue } from './addSong'
import { useParty } from './party'
import { formatTime } from '../shared/types'
import type { SearchResult } from '../shared/types'

const party = useParty()
const text = ref('')
const q = ref('')
const source = ref<'library' | 'youtube'>('library')
const online = computed(() => party.network.online)
let timer = 0

watch(text, (v) => {
  window.clearTimeout(timer)
  timer = window.setTimeout(() => (q.value = v.trim()), 250)
})
watch(online, (v) => {
  if (!v) source.value = 'library'
})

const add = useAddToQueue()

function addYouTube(r: SearchResult) {
  if (r.song) void add(r.song.id, { songId: r.song.id }, `Added ${r.song.title}`)
  else void add(r.videoId, { videoId: r.videoId, title: r.title, channel: r.channel, durationMs: r.durationMs }, 'Added. Getting your song…')
}

const chip = 'rounded-full px-4 py-1.5 text-sm font-semibold transition-colors'
</script>

<template>
  <div class="flex h-full flex-col">
    <div class="px-4 pt-5">
      <h1 class="text-3xl font-extrabold tracking-tight text-white">Search</h1>
      <label class="mt-4 flex items-center gap-3 rounded-lg bg-white px-3 py-3 text-ink-950">
        <Icon name="search" :size="22" />
        <input v-model="text" type="search" placeholder="What do you want to sing?" class="min-w-0 flex-1 bg-transparent text-base font-medium outline-none placeholder:text-ink-500" />
      </label>
      <div class="mt-3 flex gap-2 pb-2">
        <button :class="[chip, source === 'library' ? 'bg-brand text-white' : 'bg-ink-800 text-ink-100']" @click="source = 'library'">Your songs</button>
        <button :class="[chip, source === 'youtube' ? 'bg-brand text-white' : 'bg-ink-800 text-ink-100']" class="disabled:opacity-40" :disabled="!online" @click="source = 'youtube'">YouTube{{ online ? '' : ' (offline)' }}</button>
      </div>
    </div>
    <div class="min-h-0 flex-1">
      <SongList v-if="source === 'library'" :params="{ q }">
        <template #empty>
          <p class="text-lg font-semibold text-white">{{ q ? `“${q}” isn't here yet` : 'No songs yet' }}</p>
          <button v-if="online" class="mt-4 rounded-full bg-brand px-6 py-3 font-bold text-white transition-transform active:scale-95" @click="source = 'youtube'">Add it from YouTube</button>
          <p v-else class="mt-2 text-sm text-ink-300">Connect to the internet to add it.</p>
        </template>
      </SongList>
      <InfiniteList
        v-else-if="q"
        :query-key="['youtube', q]"
        :fetch-page="pageFetcher<SearchResult>('/api/youtube/search', { q })"
        :row-height="64"
        :item-key="(r: SearchResult) => r.videoId"
        empty="No results."
      >
        <template #default="{ item }">
          <button v-if="(item as SearchResult).song" class="min-w-0 px-1 text-left transition-transform active:scale-[.98]" @click="addYouTube(item as SearchResult)">
            <SongCard :song="(item as SearchResult).song!" adds :added="party.queuedIds.has((item as SearchResult).song!.id)" />
          </button>
          <button v-else class="flex h-full min-w-0 items-center gap-3 px-1 text-left transition-transform active:scale-[.98]" @click="addYouTube(item as SearchResult)">
            <img :src="`https://i.ytimg.com/vi/${(item as SearchResult).videoId}/default.jpg`" loading="lazy" alt="" class="h-12 w-12 shrink-0 rounded-md object-cover" />
            <span class="min-w-0 flex-1">
              <span class="block truncate font-medium text-white">{{ (item as SearchResult).title }}</span>
              <span class="block truncate text-sm text-ink-300">{{ (item as SearchResult).channel }}<template v-if="(item as SearchResult).durationMs"> · {{ formatTime((item as SearchResult).durationMs) }}</template></span>
            </span>
            <span v-if="party.queuedIds.has((item as SearchResult).videoId)" class="flex shrink-0 items-center gap-1 text-xs font-semibold text-emerald-300"><Icon name="check" :size="18" />Added</span>
            <span v-else class="shrink-0 text-ink-300"><Icon name="plus" :size="22" /></span>
          </button>
        </template>
      </InfiniteList>
      <div v-else class="flex flex-col items-center gap-3 p-10 text-center text-ink-300">
        <Icon name="search" :size="40" />
        <p class="text-lg font-semibold text-white">Find a karaoke version</p>
        <p class="text-sm">Type a song or singer, or paste a YouTube link.</p>
      </div>
    </div>
  </div>
</template>
