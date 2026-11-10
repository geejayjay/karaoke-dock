<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import InfiniteList from '../shared/InfiniteList.vue'
import SettingsForm, { type Def } from './SettingsForm.vue'
import LibraryPage from './LibraryPage.vue'
import StatusPage from './StatusPage.vue'
import OverviewPage, { type Status } from './OverviewPage.vue'
import AdminIcon, { type AdminIconName } from './AdminIcon.vue'
import BrandMark from '../shared/BrandMark.vue'
import { api, pageFetcher, send, signedIn } from './api'

interface NavItem {
  page: string
  label: string
  icon: AdminIconName
  blurb: string
}

const groups: { title: string; items: NavItem[] }[] = [
  {
    title: '',
    items: [{ page: 'Overview', label: 'Home', icon: 'home', blurb: "Here's how your karaoke is doing." }],
  },
  {
    title: 'Songs',
    items: [
      { page: 'Library', label: 'Library', icon: 'music', blurb: 'Every song you have. Add, fix or check them here.' },
      { page: 'Song status', label: 'Song status', icon: 'pulse', blurb: "What's being prepared, what's ready and what needs a look." },
    ],
  },
  {
    title: 'Settings',
    items: [
      { page: 'Network', label: 'Joining', icon: 'wifi', blurb: 'How guests reach your karaoke from their phones.' },
      { page: 'Playback', label: 'Playback', icon: 'play', blurb: 'How songs start and play on the TV.' },
      { page: 'Processing', label: 'Processing', icon: 'cpu', blurb: 'How new songs are prepared.' },
      { page: 'Storage', label: 'Storage', icon: 'disk', blurb: 'Where your music is kept and how much space is left.' },
      { page: 'Access', label: 'Access', icon: 'lock', blurb: 'Passwords, room code and limits for guests.' },
      { page: 'System', label: 'System', icon: 'wrench', blurb: 'Updates, backup and what the app has been doing.' },
    ],
  },
]

const qc = useQueryClient()
const page = ref('Overview')
const current = computed(() => groups.flatMap((g) => g.items).find((i) => i.page === page.value)!)
const password = ref('')
const passwordAgain = ref('')
const needsSetup = ref(false)
const loginError = ref('')
const needRestart = ref(false)
const pwCurrent = ref('')
const pwNext = ref('')
const pwMessage = ref('')
const moveTarget = ref('')
const moveMessage = ref('')
const toolMessage = ref('')

const settings = useQuery({
  queryKey: ['settings'],
  queryFn: () => api<{ defs: Def[]; values: Record<string, string> }>('/api/admin/settings'),
  enabled: computed(() => signedIn.value === true),
})
const status = useQuery({
  queryKey: ['status'],
  queryFn: () => api<Status>('/api/admin/status'),
  enabled: computed(() => signedIn.value === true),
  refetchInterval: 5000,
})
const hasSettings = computed(() => !['Overview', 'Library', 'Song status', 'System'].includes(page.value))

onMounted(async () => {
  try {
    await api('/api/admin/status')
    signedIn.value = true
  } catch {
    // No password yet: ask for one instead of a sign-in.
    needsSetup.value = await api<{ needed: boolean }>('/api/admin/setup').then((r) => r.needed).catch(() => false)
    signedIn.value = false
  }
})

async function createPassword() {
  loginError.value = ''
  if (password.value !== passwordAgain.value) {
    loginError.value = "The passwords don't match."
    return
  }
  try {
    await send('POST', '/api/admin/setup', { password: password.value })
    password.value = passwordAgain.value = ''
    needsSetup.value = false
    signedIn.value = true
    await qc.invalidateQueries()
  } catch (e) {
    loginError.value = (e as Error).message
    // Someone else created it first.
    if (/already/.test(loginError.value)) needsSetup.value = false
  }
}

async function login() {
  loginError.value = ''
  try {
    await send('POST', '/api/admin/login', { password: password.value })
    password.value = ''
    signedIn.value = true
    await qc.invalidateQueries()
  } catch (e) {
    loginError.value = (e as Error).message
  }
}

async function logout() {
  await send('POST', '/api/admin/logout').catch(() => {})
  signedIn.value = false
}

async function changePassword() {
  pwMessage.value = ''
  try {
    await send('POST', '/api/admin/password', { current: pwCurrent.value, next: pwNext.value })
    pwCurrent.value = pwNext.value = ''
    pwMessage.value = 'Password changed.'
    await qc.invalidateQueries({ queryKey: ['settings'] })
  } catch (e) {
    pwMessage.value = (e as Error).message
  }
}

async function startMove() {
  moveMessage.value = ''
  try {
    await send('POST', '/api/admin/storage/move', { target: moveTarget.value })
    moveMessage.value = 'Moving files…'
    await qc.invalidateQueries({ queryKey: ['status'] })
  } catch (e) {
    moveMessage.value = (e as Error).message
  }
}

async function tool(path: string, label: string) {
  toolMessage.value = `${label}…`
  try {
    const r = await send<{ output: string }>('POST', path)
    toolMessage.value = r.output || 'Done.'
  } catch (e) {
    toolMessage.value = (e as Error).message
  }
}

async function restart() {
  await send('POST', '/api/admin/restart').catch(() => {})
  needRestart.value = false
  toolMessage.value = 'Restarting…'
}

function gb(n?: number) {
  return n === undefined ? '-' : `${(n / 1024 ** 3).toFixed(1)} GB`
}

const diskNames: Record<string, string> = { media: 'Music', metadata: 'Song details', temp: 'Temporary files' }

interface LogRow {
  seq: number
  text: string
}
const fetchLogs = pageFetcher<LogRow>('/api/admin/logs')

const input = 'w-full rounded-lg border border-ink-700 bg-ink-800 px-3 py-2.5 outline-none transition-shadow focus:ring-2 focus:ring-brand'
const btn = 'rounded-lg bg-ink-700 px-4 py-2 text-sm font-medium transition-colors hover:bg-ink-500/40'
const primary = 'w-full rounded-lg bg-brand py-2.5 font-bold text-white'
const card = 'max-w-3xl space-y-3 rounded-2xl border border-ink-800 bg-ink-900 p-5'
const authCard = 'w-full max-w-sm space-y-5 rounded-2xl border border-ink-800 bg-ink-900 p-7'
</script>

<template>
  <div v-if="signedIn === null" class="p-10 text-center text-ink-500">Loading…</div>

  <main v-else-if="!signedIn" class="flex min-h-[100dvh] items-center justify-center p-5">
    <div :class="authCard">
      <div class="text-center">
        <BrandMark class="justify-center text-4xl" />
        <p class="mt-1 text-sm text-ink-300">{{ needsSetup ? 'Welcome! Create a password to manage your karaoke.' : 'Sign in to manage your karaoke.' }}</p>
      </div>
      <form v-if="needsSetup" class="space-y-3" @submit.prevent="createPassword">
        <input v-model="password" type="password" autocomplete="new-password" placeholder="New password, 8 characters or more" :class="input" />
        <input v-model="passwordAgain" type="password" autocomplete="new-password" placeholder="Type it again" :class="input" />
        <p v-if="loginError" class="text-sm text-red-300">{{ loginError }}</p>
        <button :class="primary">Create password</button>
      </form>
      <form v-else class="space-y-3" @submit.prevent="login">
        <input v-model="password" type="password" autocomplete="current-password" placeholder="Admin password" :class="input" />
        <p v-if="loginError" class="text-sm text-red-300">{{ loginError }}</p>
        <button :class="primary">Sign in</button>
      </form>
    </div>
  </main>

  <div v-else class="md:flex">
    <aside class="md:sticky md:top-0 md:flex md:h-[100dvh] md:w-60 md:shrink-0 md:flex-col md:border-r md:border-ink-800 md:p-4">
      <div class="flex items-center justify-between px-4 pt-4 md:px-2 md:pt-1">
        <BrandMark class="text-2xl" />
        <button class="text-ink-300 md:hidden" aria-label="Sign out" @click="logout"><AdminIcon name="logout" /></button>
      </div>
      <nav class="flex gap-1 overflow-x-auto px-4 py-3 md:mt-4 md:flex-1 md:flex-col md:overflow-y-auto md:p-0">
        <template v-for="g in groups" :key="g.title">
          <p v-if="g.title" class="hidden px-3 pb-1 pt-4 text-xs font-semibold uppercase tracking-wide text-ink-500 md:block">{{ g.title }}</p>
          <button
            v-for="item in g.items"
            :key="item.page"
            class="flex shrink-0 items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition-colors"
            :class="page === item.page ? 'bg-brand text-white' : 'text-ink-300 hover:bg-ink-800 hover:text-white'"
            @click="page = item.page"
          >
            <AdminIcon :name="item.icon" :size="18" />{{ item.label }}
          </button>
        </template>
      </nav>
      <button class="hidden items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium text-ink-300 hover:bg-ink-800 hover:text-white md:flex" @click="logout"><AdminIcon name="logout" :size="18" />Sign out</button>
    </aside>

    <div class="min-w-0 flex-1 p-4 md:p-8">
      <header class="mb-5">
        <h2 class="text-2xl font-extrabold tracking-tight">{{ current.label }}</h2>
        <p class="mt-0.5 text-ink-300">{{ current.blurb }}</p>
      </header>

      <div v-if="needRestart" class="mb-4 flex max-w-3xl items-center justify-between gap-3 rounded-2xl border border-amber-500/30 bg-amber-500/10 p-4">
        <span>Restart the app to apply this change.</span><button class="rounded-lg bg-brand px-4 py-2 text-sm font-bold text-white" @click="restart">Restart now</button>
      </div>

      <OverviewPage v-if="page === 'Overview'" :status="status.data.value" :qr-stamp="status.dataUpdatedAt.value" @go="page = $event" />
      <LibraryPage v-else-if="page === 'Library'" />
      <StatusPage v-else-if="page === 'Song status'" />

      <div v-else-if="settings.data.value" class="space-y-5">
        <SettingsForm v-if="hasSettings" :defs="settings.data.value.defs" :values="settings.data.value.values" :page="page" @restart="needRestart = true" />

        <section v-if="page === 'Network'" :class="card" class="flex items-center gap-5 space-y-0">
          <img :src="`/api/qr.svg?v=${status.dataUpdatedAt.value}`" alt="" class="h-32 w-32 shrink-0 rounded-xl bg-white p-1.5" />
          <div class="min-w-0">
            <h3 class="font-bold">This is what guests scan</h3>
            <p class="mt-2 break-all text-sm text-ink-300">{{ status.data.value?.joinUrl }}</p>
          </div>
        </section>

        <template v-if="page === 'Storage'">
          <section :class="card">
            <h3 class="font-bold">Space left</h3>
            <p v-for="(d, k) in status.data.value?.disk" :key="k" class="flex justify-between text-sm"><span>{{ diskNames[k] ?? k }}</span><span class="text-ink-300">{{ gb(d?.free) }} free of {{ gb(d?.total) }}</span></p>
            <p class="text-sm" :class="status.data.value?.network.mediaOk ? 'text-emerald-300' : 'text-red-300'">Music drive {{ status.data.value?.network.mediaOk ? 'connected' : 'unavailable' }}</p>
          </section>
          <section :class="card">
            <h3 class="font-bold">Move files</h3>
            <p class="text-sm text-ink-300">Your music is in {{ status.data.value?.mediaRoot }}</p>
            <input v-model="moveTarget" placeholder="New folder, full path" :class="input" />
            <button :class="btn" :disabled="status.data.value?.move?.running" @click="startMove">Move files</button>
            <p class="text-sm text-ink-300">
              <template v-if="status.data.value?.move?.running">Moved {{ status.data.value.move.done }} files…</template>
              <template v-else-if="status.data.value?.move?.error">{{ status.data.value.move.error }}</template>
              <template v-else-if="status.data.value?.move">Moved {{ status.data.value.move.done }} files.</template>
              <template v-else>{{ moveMessage }}</template>
            </p>
          </section>
        </template>

        <section v-if="page === 'Access'" :class="card">
          <h3 class="font-bold">Change admin password</h3>
          <form class="space-y-3" @submit.prevent="changePassword">
            <input v-model="pwCurrent" type="password" autocomplete="current-password" placeholder="Current password" :class="input" />
            <input v-model="pwNext" type="password" autocomplete="new-password" placeholder="New password" :class="input" />
            <button :class="btn">Change password</button>
            <p class="text-sm text-ink-300">{{ pwMessage }}</p>
          </form>
        </section>

        <template v-if="page === 'System'">
          <section :class="card">
            <h3 class="font-bold">Maintenance</h3>
            <div class="flex flex-wrap gap-2">
              <button :class="btn" @click="tool('/api/admin/tools/yt-dlp/update', 'Updating the YouTube downloader')">Update YouTube downloader</button>
              <button :class="btn" @click="tool('/api/admin/tools/amll/update', 'Updating duet lyrics')">Update duet lyrics</button>
              <a :class="btn" href="/api/admin/backup">Download backup</a>
              <button :class="btn" @click="restart">Restart app</button>
            </div>
            <p class="break-words text-sm text-ink-300">{{ toolMessage }}</p>
          </section>
          <section :class="card">
            <h3 class="font-bold">Details</h3>
            <p v-for="(v, k) in status.data.value?.tools" :key="k" class="flex justify-between gap-4 text-sm"><span>{{ k }}</span><span class="truncate text-ink-300">{{ v }}</span></p>
            <p class="flex justify-between text-sm"><span>Video conversion</span><span class="text-ink-300">{{ status.data.value?.encoders?.join(', ') || 'checking' }}</span></p>
            <p class="flex justify-between text-sm"><span>AI helper</span><span class="text-ink-300">{{ status.data.value?.sidecar.running ? 'working' : 'resting' }}</span></p>
            <p class="flex justify-between text-sm"><span>Internet</span><span class="text-ink-300">{{ status.data.value?.network.online ? 'connected' : 'offline' }}</span></p>
          </section>
          <section class="h-96 max-w-3xl overflow-hidden rounded-2xl border border-ink-800 bg-ink-900 py-2">
            <InfiniteList :query-key="['logs']" :fetch-page="fetchLogs" :row-height="28" :item-key="(l: LogRow) => l.seq" empty="No activity yet.">
              <template #default="{ item }">
                <p class="truncate font-mono text-xs leading-7 text-ink-300" :title="(item as LogRow).text">{{ (item as LogRow).text }}</p>
              </template>
            </InfiniteList>
          </section>
        </template>
      </div>
    </div>
  </div>
</template>
