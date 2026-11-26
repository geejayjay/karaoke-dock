<script setup lang="ts">
import { computed, ref } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import InfiniteList from '../shared/InfiniteList.vue'
import StatusChips from './StatusChips.vue'
import { api, pageFetcher } from './api'
import { REFRESH_MS, statusLine, type Filter, type StatusRow } from './status'

const filter = ref<Filter>('all')
const tabs: { id: Filter; label: string; empty: string }[] = [
  { id: 'all', label: 'All', empty: 'No songs yet.' },
  { id: 'working', label: 'Processing', empty: 'Nothing is processing.' },
  { id: 'done', label: 'Processed', empty: 'No songs are processed yet.' },
  { id: 'problem', label: 'Problems', empty: 'No problems.' },
]

const counts = useQuery({
  queryKey: ['processing-counts'],
  queryFn: () => api<Record<Filter, number>>('/api/admin/processing/counts'),
  refetchInterval: REFRESH_MS,
})
const emptyText = computed(() => tabs.find((t) => t.id === filter.value)?.empty)
</script>

<template>
  <div class="flex h-[calc(100dvh-11rem)] flex-col gap-3 md:h-[calc(100dvh-9.5rem)]">
    <div class="flex flex-wrap gap-1">
      <button
        v-for="t in tabs"
        :key="t.id"
        class="rounded-lg px-3 py-1.5 text-sm font-medium"
        :class="filter === t.id ? 'bg-ink-700 text-white' : 'bg-ink-900 text-ink-300'"
        @click="filter = t.id"
      >
        {{ t.label }}<span class="ml-1.5 text-ink-300">{{ counts.data.value?.[t.id] ?? '' }}</span>
      </button>
    </div>
    <div class="min-h-0 flex-1 overflow-hidden rounded-2xl border border-ink-800 bg-ink-900 pt-2">
      <InfiniteList
        :query-key="['processing', filter]"
        :fetch-page="pageFetcher<StatusRow>('/api/admin/processing', { filter })"
        :row-height="72"
        :item-key="(s: StatusRow) => s.id"
        :empty="emptyText"
        :refetch-ms="REFRESH_MS"
      >
        <template #default="{ item }">
          <div class="flex h-full min-w-0 items-center gap-3">
            <div class="min-w-0 flex-1">
              <p class="truncate font-semibold">{{ (item as StatusRow).title }}</p>
              <p class="truncate text-sm" :class="(item as StatusRow).overall === 'problem' ? 'text-red-300' : 'text-ink-300'">{{ statusLine(item as StatusRow) || (item as StatusRow).artist }}</p>
              <div v-if="(item as StatusRow).percent" class="mt-1 h-1 max-w-xs overflow-hidden rounded-full bg-ink-700">
                <div class="h-full bg-brand transition-[width] duration-500" :style="{ width: `${(item as StatusRow).percent}%` }" />
              </div>
            </div>
            <div class="flex shrink-0 flex-col gap-1 sm:flex-row sm:gap-2">
              <StatusChips :audio="(item as StatusRow).audio" :lyrics="(item as StatusRow).lyrics" :timed-lines="(item as StatusRow).timedLines" :lyric-lines="(item as StatusRow).lyricLines" />
            </div>
          </div>
        </template>
      </InfiniteList>
    </div>
  </div>
</template>
