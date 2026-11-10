<script setup lang="ts">
import { computed } from 'vue'
import Art from './Art.vue'
import Icon from './Icon.vue'
import { badge, formatTime } from '../shared/types'
import type { SongRow } from '../shared/types'

const props = defineProps<{ song: Pick<SongRow, 'id' | 'title' | 'artist' | 'durationMs' | 'state' | 'coverId'>; subtitle?: string; hideBadge?: boolean; adds?: boolean; added?: boolean }>()
const label = computed(() => badge(props.song.state))
const showLabel = computed(() => !props.hideBadge && label.value !== '' && label.value !== 'Ready to sing')
const tone = computed(() => (label.value === "Couldn't add" ? 'text-red-300' : 'text-amber-200'))
const line = computed(() => {
  if (props.subtitle) return props.subtitle
  const time = props.song.durationMs ? formatTime(props.song.durationMs) : ''
  return [props.song.artist, time].filter(Boolean).join(' · ')
})
</script>

<template>
  <div class="flex h-full min-w-0 items-center gap-3">
    <Art :src="song.coverId ? `/covers/${song.coverId}` : undefined" :label="song.title" class="h-12 w-12 shrink-0" />
    <div class="min-w-0 flex-1">
      <p class="truncate font-medium text-white">{{ song.title }}</p>
      <p class="truncate text-sm text-ink-300">{{ line }}</p>
    </div>
    <span v-if="showLabel" class="shrink-0 text-xs font-medium" :class="tone">{{ label }}</span>
    <span v-if="added" class="flex shrink-0 items-center gap-1 text-xs font-semibold text-emerald-300"><Icon name="check" :size="18" />Added</span>
    <span v-else-if="adds" class="shrink-0 text-ink-300"><Icon name="plus" :size="22" /></span>
  </div>
</template>
