<script setup lang="ts">
import { computed, ref } from 'vue'
import { useQueryClient, type InfiniteData } from '@tanstack/vue-query'
import InfiniteList from '../shared/InfiniteList.vue'
import QueueRow from './QueueRow.vue'
import Art from './Art.vue'
import { api, pageFetcher, post } from './api'
import { useParty } from './party'
import { formatTime } from '../shared/types'
import type { Page, QueueItem } from '../shared/types'

const ROW_HEIGHT = 68
const party = useParty()
const qc = useQueryClient()
const current = computed(() => party.player?.current ?? null)
const pct = computed(() => (current.value?.durationMs ? Math.min(100, (party.positionMs / current.value.durationMs) * 100) : 0))
const isHost = computed(() => party.pin !== '')
const canRemoveCurrent = computed(() => {
  const c = current.value
  return !!c && (isHost.value || party.mineIds.has(c.itemId) || (!!c.owner && c.owner === party.clientTag))
})

async function removeItem(id: number) {
  try {
    await api(`/api/queue/${id}`, { method: 'DELETE' })
  } catch (e) {
    party.say((e as Error).message)
  }
}

function remove(q: QueueItem) {
  void removeItem(q.id)
}

function removeCurrent() {
  if (current.value && confirm('Stop this song and remove it?')) void removeItem(current.value.itemId)
}

const REACTIONS = ['👏', '🔥', '❤️', '🎉', '😂', '🎤']

function react(emoji: string) {
  navigator.vibrate?.(8)
  post('/api/react', { emoji }).catch(() => {})
}

async function clearQueue() {
  if (!confirm('Remove every song waiting in the queue?')) return
  try {
    await api('/api/queue', { method: 'DELETE' })
  } catch (e) {
    party.say((e as Error).message)
  }
}

const duet = ref<{ id: number; first: string } | null>(null)
const partner = ref('')

function startDuet(id: number, singer: string) {
  const [first, second] = singer.split(' & ')
  duet.value = { id, first }
  partner.value = second ?? ''
}

async function saveDuet() {
  if (!duet.value) return
  try {
    await post(`/api/queue/${duet.value.id}/partner`, { partner: partner.value })
    duet.value = null
  } catch (e) {
    party.say((e as Error).message)
  }
}

function all() {
  return neighbours(0).all.length
}

function neighbours(id: number) {
  const data = qc.getQueryData<InfiniteData<Page<QueueItem>>>(['queue'])
  const all = data?.pages.flatMap((p) => p.items) ?? []
  const i = all.findIndex((x) => x.id === id)
  return { all, i }
}

async function move(q: QueueItem, steps: number) {
  const { all, i } = neighbours(q.id)
  if (i < 0) return
  const target = Math.min(Math.max(i + steps, 0), all.length - 1)
  if (target === i) return
  const rest = all.filter((x) => x.id !== q.id)
  const afterId = target === 0 ? 0 : rest[target - 1].id
  try {
    await post(`/api/queue/${q.id}/move`, { afterId })
  } catch (e) {
    party.say((e as Error).message)
  }
}
</script>

<template>
  <div class="flex h-full flex-col">
    <section v-if="current" class="m-3 rounded-2xl bg-ink-800 p-4">
      <div class="flex items-center justify-between gap-3">
        <p class="min-w-0 truncate text-xs font-semibold uppercase tracking-wide text-brand">Now singing · {{ current.singer }}</p>
        <div v-if="canRemoveCurrent" class="flex shrink-0 gap-1.5">
          <button class="rounded-full bg-ink-700 px-3 py-1 text-sm font-medium text-ink-100" @click="startDuet(current!.itemId, current!.singer)">Duet</button>
          <button class="rounded-full bg-ink-700 px-3 py-1 text-sm font-medium text-red-300" @click="removeCurrent">Remove</button>
        </div>
      </div>
      <div class="mt-2 flex items-center gap-3">
        <Art :src="current.coverId ? `/covers/${current.coverId}` : undefined" :label="current.title" class="h-14 w-14 shrink-0" />
        <div class="min-w-0">
          <p class="truncate text-lg font-bold">{{ current.title }}</p>
          <p class="truncate text-sm text-ink-300">{{ current.artist }}</p>
        </div>
      </div>
      <div class="mt-3 h-1.5 overflow-hidden rounded-full bg-ink-700"><div class="h-full bg-brand transition-[width] duration-1000 ease-linear" :style="{ width: `${pct}%` }" /></div>
      <p class="mt-1 text-right text-xs text-ink-300">{{ formatTime(party.positionMs) }} / {{ formatTime(current.durationMs) }}</p>
      <div class="mt-3 flex justify-between">
        <button v-for="r in REACTIONS" :key="r" class="grid h-11 w-11 place-items-center rounded-full bg-ink-700 text-xl transition-transform active:scale-125" :aria-label="`Send ${r}`" @click="react(r)">{{ r }}</button>
      </div>
    </section>
    <div class="flex items-baseline justify-between gap-3 px-3 pb-2 pt-1">
      <h2 class="text-lg font-bold">Up next</h2>
      <p class="min-w-0 flex-1 truncate text-right text-xs text-ink-500">{{ isHost ? 'Swipe left to remove, right to play next' : 'Swipe your song left to remove it' }}</p>
      <button v-if="isHost" class="shrink-0 text-xs font-semibold text-red-300" @click="clearQueue">Clear</button>
    </div>
    <div class="min-h-0 flex-1">
      <InfiniteList :query-key="['queue']" :fetch-page="pageFetcher<QueueItem>('/api/queue')" :row-height="ROW_HEIGHT" :item-key="(q: QueueItem) => q.id" empty="The queue is empty. Add a song!">
        <template #default="{ item }">
          <QueueRow
            :item="(item as QueueItem)"
            :can-remove="isHost || !!(item as QueueItem).mine || party.mineIds.has((item as QueueItem).id)"
            :can-move="isHost"
            :row-height="ROW_HEIGHT"
            @remove="remove(item as QueueItem)"
            @move="move(item as QueueItem, $event)"
            @play-next="move(item as QueueItem, -all())"
            @duet="startDuet((item as QueueItem).id, (item as QueueItem).singer)"
          />
        </template>
      </InfiniteList>
    </div>

    <div v-if="duet" class="fixed inset-0 z-20 grid place-items-center bg-black/60 p-6" @click.self="duet = null">
      <form class="w-full max-w-xs space-y-3 rounded-2xl bg-ink-900 p-5" @submit.prevent="saveDuet">
        <h3 class="text-lg font-bold text-white">Singing with {{ duet.first }}</h3>
        <input v-model="partner" maxlength="40" autofocus placeholder="Second singer's name" class="w-full rounded-xl bg-ink-800 px-4 py-3 outline-none focus:ring-2 focus:ring-brand" />
        <div class="flex gap-2">
          <button type="button" class="flex-1 rounded-xl bg-ink-700 py-3 font-semibold" @click="duet = null">Cancel</button>
          <button class="flex-1 rounded-xl bg-brand py-3 font-bold text-white">Save</button>
        </div>
      </form>
    </div>
  </div>
</template>
