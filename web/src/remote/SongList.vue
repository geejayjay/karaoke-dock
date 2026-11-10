<script setup lang="ts">
import InfiniteList from '../shared/InfiniteList.vue'
import SongCard from './SongCard.vue'
import { pageFetcher } from './api'
import { useAddSong } from './addSong'
import { useParty } from './party'
import type { SongRow } from '../shared/types'

const props = defineProps<{ params: Record<string, string | undefined>; empty?: string }>()
const add = useAddSong()
const party = useParty()
</script>

<template>
  <InfiniteList :query-key="['songs', params]" :fetch-page="pageFetcher<SongRow>('/api/songs', props.params)" :row-height="64" :item-key="(s: SongRow) => s.id" :empty="empty ?? 'No songs here yet.'">
    <template #default="{ item }">
      <button class="min-w-0 px-1 text-left transition-transform active:scale-[.98]" @click="add(item as SongRow)"><SongCard :song="(item as SongRow)" adds :added="party.queuedIds.has((item as SongRow).id)" /></button>
    </template>
    <template #empty><slot name="empty">{{ empty ?? 'No songs here yet.' }}</slot></template>
  </InfiniteList>
</template>
