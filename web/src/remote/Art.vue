<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import Icon from './Icon.vue'
import { gradientFor } from './colors'

const props = defineProps<{ src?: string; label: string; round?: boolean }>()
const failed = ref(false)
watch(() => props.src, () => (failed.value = false))

const initials = computed(() => props.label.split(/\s+/).slice(0, 2).map((w) => w[0] ?? '').join('').toUpperCase())
</script>

<template>
  <div class="relative aspect-square overflow-hidden" :class="round ? 'rounded-full' : 'rounded-md'">
    <img v-if="src && !failed" :src="src" loading="lazy" alt="" class="h-full w-full object-cover" @error="failed = true" />
    <div v-else class="grid h-full w-full place-items-center text-white/90" :style="{ background: gradientFor(label) }">
      <span v-if="round" class="text-lg font-bold">{{ initials }}</span>
      <Icon v-else name="music" />
    </div>
  </div>
</template>
