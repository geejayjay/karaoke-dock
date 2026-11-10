<script setup lang="ts">
export type IconName = 'home' | 'search' | 'queue' | 'plus' | 'back' | 'music' | 'play' | 'pause' | 'skip' | 'restart' | 'lock' | 'close' | 'check' | 'up' | 'down' | 'controls'

defineProps<{ name: IconName; size?: number }>()

const paths: Record<IconName, string[]> = {
  home: ['M3 11l9-8 9 8v9a1 1 0 0 1-1 1h-5v-6h-6v6H4a1 1 0 0 1-1-1z'],
  search: ['M11 19a8 8 0 1 0 0-16 8 8 0 0 0 0 16z', 'M21 21l-4.3-4.3'],
  queue: ['M4 6h16', 'M4 12h16', 'M4 18h10'],
  plus: ['M12 5v14', 'M5 12h14'],
  back: ['M15 18l-6-6 6-6'],
  music: ['M9 18V5l12-2v13', 'M9 18a3 3 0 1 1-6 0 3 3 0 0 1 6 0z', 'M21 16a3 3 0 1 1-6 0 3 3 0 0 1 6 0z'],
  play: ['M7 4l13 8-13 8z'],
  pause: ['M8 5v14', 'M16 5v14'],
  skip: ['M5 5l11 7-11 7z', 'M19 5v14'],
  restart: ['M4 4v6h6', 'M4.5 10a8 8 0 1 1-.5 4'],
  lock: ['M5 11h14v10H5z', 'M8 11V7a4 4 0 0 1 8 0v4'],
  close: ['M6 6l12 12', 'M18 6L6 18'],
  check: ['M5 12l5 5L20 7'],
  up: ['M6 15l6-6 6 6'],
  down: ['M6 9l6 6 6-6'],
  controls: ['M4 6h9', 'M19 6h1', 'M16 3.5a2.5 2.5 0 1 0 0 5 2.5 2.5 0 0 0 0-5z', 'M4 12h1', 'M11 12h9', 'M8 9.5a2.5 2.5 0 1 0 0 5 2.5 2.5 0 0 0 0-5z', 'M4 18h9', 'M19 18h1', 'M16 15.5a2.5 2.5 0 1 0 0 5 2.5 2.5 0 0 0 0-5z'],
}
</script>

<template>
  <svg :width="size ?? 24" :height="size ?? 24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <path v-for="d in paths[name]" :key="d" :d="d" />
  </svg>
</template>
