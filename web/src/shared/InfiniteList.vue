<script setup lang="ts" generic="T">
import { computed, ref, watchEffect } from 'vue'
import { useInfiniteQuery } from '@tanstack/vue-query'
import { useVirtualizer } from '@tanstack/vue-virtual'
import type { Page } from './types'

const props = withDefaults(
  defineProps<{
    queryKey: unknown[]
    fetchPage: (cursor: string | null) => Promise<Page<T>>
    rowHeight: number
    columns?: number
    empty?: string
    itemKey: (item: T) => string | number
    enabled?: boolean
    refetchMs?: number
  }>(),
  { columns: 1, empty: 'Nothing here yet.', enabled: true },
)

const query = useInfiniteQuery({
  queryKey: computed(() => props.queryKey),
  queryFn: ({ pageParam }) => props.fetchPage(pageParam as string | null),
  initialPageParam: null as string | null,
  getNextPageParam: (last: Page<T>) => last.nextCursor,
  enabled: computed(() => props.enabled),
  refetchInterval: computed(() => props.refetchMs ?? false),
})

const items = computed<T[]>(() => (query.data.value?.pages ?? []).flatMap((p) => p.items))
const rowCount = computed(() => Math.ceil(items.value.length / props.columns))
const scroller = ref<HTMLElement | null>(null)

const virtual = useVirtualizer(
  computed(() => ({
    count: rowCount.value,
    getScrollElement: () => scroller.value,
    estimateSize: () => props.rowHeight,
    overscan: 6,
  })),
)

const rows = computed(() => virtual.value.getVirtualItems())
const total = computed(() => virtual.value.getTotalSize())

// Fetch the next page before the end is reached.
watchEffect(() => {
  const last = rows.value[rows.value.length - 1]
  if (last && last.index >= rowCount.value - 8 && query.hasNextPage.value && !query.isFetchingNextPage.value) {
    void query.fetchNextPage()
  }
})

function rowItems(index: number): T[] {
  return items.value.slice(index * props.columns, (index + 1) * props.columns)
}
</script>

<template>
  <div ref="scroller" class="h-full overflow-y-auto overscroll-contain">
    <div v-if="query.isLoading.value" class="p-8 text-center text-ink-500">Loading…</div>
    <div v-else-if="query.isError.value" class="p-8 text-center text-ink-300">
      <p>{{ (query.error.value as Error)?.message || "Couldn't load this." }}</p>
      <button class="mt-3 rounded-lg bg-ink-700 px-4 py-2" @click="query.refetch()">Try again</button>
    </div>
    <div v-else-if="items.length === 0" class="p-8 text-center text-ink-500"><slot name="empty">{{ empty }}</slot></div>
    <div v-else class="relative w-full" :style="{ height: `${total}px` }">
      <div
        v-for="row in rows"
        :key="row.index"
        class="absolute left-0 top-0 grid w-full gap-3 px-3"
        :style="{ height: `${row.size}px`, transform: `translateY(${row.start}px)`, gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))` }"
      >
        <template v-for="item in rowItems(row.index)" :key="itemKey(item)">
          <slot :item="item" />
        </template>
      </div>
    </div>
  </div>
</template>
