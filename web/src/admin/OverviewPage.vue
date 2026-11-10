<script setup lang="ts">
import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import AdminIcon from './AdminIcon.vue'
import { api } from './api'
import { REFRESH_MS, type Filter } from './status'

export interface Status {
  tools: Record<string, string>
  encoders: string[]
  sidecar: { running: boolean; devices: string[] | null }
  network: { online: boolean; mediaOk: boolean }
  joinUrl: string
  mediaRoot: string
  move: { running: boolean; done: number; target: string; error?: string } | null
  disk: Record<string, { free: number; total: number } | null>
}

const props = defineProps<{ status?: Status; qrStamp: number }>()
defineEmits<{ go: [page: string] }>()

const counts = useQuery({
  queryKey: ['processing-counts'],
  queryFn: () => api<Record<Filter, number>>('/api/admin/processing/counts'),
  refetchInterval: REFRESH_MS,
})

const tiles = computed(() => [
  { label: 'Songs', value: counts.data.value?.all, page: 'Library', tone: 'text-white' },
  { label: 'Processing', value: counts.data.value?.working, page: 'Song status', tone: 'text-amber-200' },
  { label: 'Ready', value: counts.data.value?.done, page: 'Song status', tone: 'text-emerald-300' },
  { label: 'Need attention', value: counts.data.value?.problem, page: 'Song status', tone: counts.data.value?.problem ? 'text-red-300' : 'text-white' },
])

const health = computed(() => [
  { label: 'Internet', ok: !!props.status?.network.online, good: 'Connected', bad: 'Offline' },
  { label: 'Music drive', ok: !!props.status?.network.mediaOk, good: 'Connected', bad: 'Unavailable' },
])

const music = computed(() => props.status?.disk?.media ?? Object.values(props.status?.disk ?? {})[0] ?? null)
const usedPercent = computed(() => (music.value?.total ? Math.round((1 - music.value.free / music.value.total) * 100) : 0))
/** The TV page, on the same address guests use. */
const tvUrl = computed(() => {
  try {
    return props.status?.joinUrl ? new URL('/tv', props.status.joinUrl).href : ''
  } catch {
    return ''
  }
})
const gb = (n?: number) => (n === undefined ? '-' : `${(n / 1024 ** 3).toFixed(0)} GB`)
const card = 'rounded-2xl border border-ink-800 bg-ink-900 p-5'
</script>

<template>
  <div class="space-y-4">
    <div class="grid grid-cols-2 gap-3 lg:grid-cols-4">
      <button v-for="t in tiles" :key="t.label" :class="card" class="text-left transition-colors hover:border-ink-700" @click="$emit('go', t.page)">
        <p class="text-sm text-ink-300">{{ t.label }}</p>
        <p class="mt-1 text-3xl font-extrabold" :class="t.tone">{{ t.value ?? '–' }}</p>
      </button>
    </div>

    <div class="grid gap-3 lg:grid-cols-2">
      <section :class="card" class="flex items-center gap-5">
        <img :src="`/api/qr.svg?v=${qrStamp}`" alt="QR code to join" class="h-32 w-32 shrink-0 rounded-xl bg-white p-1.5" />
        <div class="min-w-0">
          <h2 class="font-bold">Invite people</h2>
          <p class="mt-1 text-sm text-ink-300">Guests scan this to add songs from their phones.</p>
          <dl class="mt-3 space-y-1.5 text-sm">
            <div>
              <dt class="text-xs font-semibold uppercase tracking-wide text-ink-500">Phones</dt>
              <dd><a :href="status?.joinUrl" target="_blank" rel="noopener" class="break-all text-ink-100 underline-offset-2 hover:underline">{{ status?.joinUrl }}</a></dd>
            </div>
            <div v-if="tvUrl">
              <dt class="text-xs font-semibold uppercase tracking-wide text-ink-500">TV</dt>
              <dd><a :href="tvUrl" target="_blank" rel="noopener" class="break-all text-ink-100 underline-offset-2 hover:underline">{{ tvUrl }}</a></dd>
            </div>
          </dl>
        </div>
      </section>

      <section :class="card" class="space-y-3">
        <h2 class="font-bold">How things are running</h2>
        <p v-for="h in health" :key="h.label" class="flex items-center justify-between text-sm">
          <span>{{ h.label }}</span>
          <span class="flex items-center gap-1.5" :class="h.ok ? 'text-emerald-300' : 'text-red-300'"><AdminIcon :name="h.ok ? 'check' : 'alert'" :size="16" />{{ h.ok ? h.good : h.bad }}</span>
        </p>
        <div v-if="music">
          <p class="flex items-center justify-between text-sm"><span>Space for music</span><span class="text-ink-300">{{ gb(music.free) }} free of {{ gb(music.total) }}</span></p>
          <div class="mt-2 h-2 overflow-hidden rounded-full bg-ink-700"><div class="h-full rounded-full bg-brand" :style="{ width: `${usedPercent}%` }" /></div>
        </div>
      </section>
    </div>
  </div>
</template>
