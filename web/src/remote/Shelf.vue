<script setup lang="ts" generic="T">
import { computed } from 'vue'
import { useInfiniteQuery } from '@tanstack/vue-query'
import type { Page } from '../shared/types'

const props = defineProps<{
  title: string
  queryKey: unknown[]
  fetchPage: (cursor: string | null) => Promise<Page<T>>
  itemKey: (item: T) => string | number
  canSeeAll?: boolean
}>()
defineEmits<{ seeAll: [] }>()

const REFRESH_MS = 30000
const query = useInfiniteQuery({
  queryKey: computed(() => props.queryKey),
  queryFn: ({ pageParam }) => props.fetchPage(pageParam as string | null),
  initialPageParam: null as string | null,
  getNextPageParam: (last: Page<T>) => last.nextCursor,
  refetchInterval: REFRESH_MS,
})
const items = computed<T[]>(() => (query.data.value?.pages ?? []).flatMap((p) => p.items))

function onScroll(e: Event) {
  const el = e.target as HTMLElement
  if (el.scrollLeft + el.clientWidth > el.scrollWidth - 300 && query.hasNextPage.value && !query.isFetchingNextPage.value) {
    void query.fetchNextPage()
  }
}
</script>

<template>
  <section v-if="items.length" class="mt-6">
    <div class="flex items-baseline justify-between px-4">
      <h2 class="text-xl font-bold tracking-tight text-white">{{ title }}</h2>
      <button v-if="canSeeAll" class="text-sm font-semibold text-ink-300" @click="$emit('seeAll')">See all</button>
    </div>
    <div class="no-scrollbar mt-3 flex snap-x scroll-px-4 gap-3 overflow-x-auto px-4" @scroll.passive="onScroll">
      <div v-for="item in items" :key="itemKey(item)" class="shrink-0 snap-start">
        <slot :item="item" />
      </div>
    </div>
  </section>
</template>
