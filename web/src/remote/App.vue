<script setup lang="ts">
import { ref } from 'vue'
import { useParty } from './party'
import SearchTab from './SearchTab.vue'
import BrowseTab from './BrowseTab.vue'
import QueueTab from './QueueTab.vue'
import HostPanel from './HostPanel.vue'
import Icon from './Icon.vue'
import BrandMark from '../shared/BrandMark.vue'

const party = useParty()
const nameInput = ref('')
const tab = ref<'search' | 'browse' | 'queue' | 'controls'>('browse')
const tabs = [
  { id: 'browse', label: 'Browse', icon: 'home' },
  { id: 'search', label: 'Search', icon: 'search' },
  { id: 'queue', label: 'Queue', icon: 'queue' },
  { id: 'controls', label: 'Controls', icon: 'controls' },
] as const

function saveName() {
  const v = nameInput.value.trim()
  if (v) party.setName(v.slice(0, 40))
}
</script>

<template>
  <main v-if="!party.name" class="flex h-[100dvh] flex-col justify-center gap-5 p-6">
    <BrandMark class="justify-center text-5xl" />
    <form class="space-y-3" @submit.prevent="saveName">
      <input v-model="nameInput" maxlength="40" placeholder="Your name" class="w-full rounded-xl bg-ink-800 px-4 py-4 text-center text-lg outline-none focus:ring-2 focus:ring-brand" />
      <button class="w-full rounded-xl bg-brand py-4 text-lg font-bold">Let's sing</button>
    </form>
  </main>

  <div v-else class="relative flex h-[100dvh] flex-col">
    <span v-if="!party.connected" class="absolute right-3 top-3 z-10 text-xs text-ink-300">Reconnecting…</span>
    <div class="min-h-0 flex-1">
      <KeepAlive>
        <SearchTab v-if="tab === 'search'" />
        <BrowseTab v-else-if="tab === 'browse'" />
        <QueueTab v-else-if="tab === 'queue'" />
        <HostPanel v-else />
      </KeepAlive>
    </div>
    <button v-if="party.turnText" class="truncate bg-brand px-4 py-2 text-center text-sm font-bold text-white" @click="tab = 'queue'">{{ party.turnText }}</button>
    <nav class="grid grid-cols-4 bg-black/30 pb-[env(safe-area-inset-bottom)]">
      <button v-for="t in tabs" :key="t.id" class="flex flex-col items-center gap-1 py-2.5 text-[11px] font-semibold transition-colors" :class="tab === t.id ? 'text-white' : 'text-ink-500'" @click="tab = t.id">
        <Icon :name="t.icon" />{{ t.label }}
      </button>
    </nav>
  </div>

  <div v-if="party.toast" class="fixed inset-x-4 bottom-20 z-30 rounded-xl bg-ink-700 px-4 py-3 text-center font-medium shadow-lg">{{ party.toast }}</div>
</template>
