<script setup lang="ts">
export type AdminIconName = 'home' | 'music' | 'pulse' | 'wifi' | 'play' | 'cpu' | 'disk' | 'lock' | 'wrench' | 'logout' | 'check' | 'alert'

defineProps<{ name: AdminIconName; size?: number }>()

const paths: Record<AdminIconName, string[]> = {
  home: ['M3 11l9-8 9 8v9a1 1 0 0 1-1 1h-5v-6h-6v6H4a1 1 0 0 1-1-1z'],
  music: ['M9 18V5l12-2v13', 'M9 18a3 3 0 1 1-6 0 3 3 0 0 1 6 0z', 'M21 16a3 3 0 1 1-6 0 3 3 0 0 1 6 0z'],
  pulse: ['M3 12h4l3-8 4 16 3-8h4'],
  wifi: ['M5 12.5a10 10 0 0 1 14 0', 'M8.5 16a5 5 0 0 1 7 0', 'M2 9a15 15 0 0 1 20 0', 'M12 20h.01'],
  play: ['M6 4l14 8-14 8z'],
  cpu: ['M6 6h12v12H6z', 'M9 2v4', 'M15 2v4', 'M9 18v4', 'M15 18v4', 'M2 9h4', 'M2 15h4', 'M18 9h4', 'M18 15h4'],
  disk: ['M4 6c0-1.7 3.6-3 8-3s8 1.3 8 3-3.6 3-8 3-8-1.3-8-3z', 'M4 6v12c0 1.7 3.6 3 8 3s8-1.3 8-3V6', 'M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3'],
  lock: ['M5 11h14v10H5z', 'M8 11V7a4 4 0 0 1 8 0v4'],
  wrench: ['M14.7 6.3a4 4 0 0 0-5.4 5.1L3 17.7V21h3.3l6.3-6.3a4 4 0 0 0 5.1-5.4l-2.6 2.6-3-3z'],
  logout: ['M15 4h4a1 1 0 0 1 1 1v14a1 1 0 0 1-1 1h-4', 'M10 17l-5-5 5-5', 'M5 12h11'],
  check: ['M5 12l5 5L20 7'],
  alert: ['M12 3l10 18H2z', 'M12 10v5', 'M12 18h.01'],
}
</script>

<template>
  <svg :width="size ?? 20" :height="size ?? 20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <path v-for="d in paths[name]" :key="d" :d="d" />
  </svg>
</template>
