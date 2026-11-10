import { defineStore } from 'pinia'
import { useQueryClient, type InfiniteData, type QueryClient } from '@tanstack/vue-query'
import { computed, ref, watch } from 'vue'
import { Live } from '../shared/live'
import { api, setHostPin } from './api'
import type { Network, Page, PlayerState, QueueItem, QueueUpdate, ServerEvent, SongRow } from '../shared/types'

export const room = location.pathname.split('/')[2] ?? ''

interface MyTurn {
  waiting: boolean
  ahead?: number
  title?: string
}

function read(key: string): string {
  try {
    return localStorage.getItem(key) ?? ''
  } catch {
    return ''
  }
}

function write(key: string, value: string) {
  try {
    localStorage.setItem(key, value)
  } catch {
    // Private browsing can block storage, and the app still works.
  }
}

type Paged<T> = InfiniteData<Page<T>, string | null>

function mapPages<T>(data: Paged<T> | undefined, fn: (items: T[]) => T[]): Paged<T> | undefined {
  if (!data) return data
  return { ...data, pages: data.pages.map((p) => ({ ...p, items: fn(p.items) })) }
}

/** Applies live queue changes to the cache, without refetching. */
function applyQueue(qc: QueryClient, u: QueueUpdate) {
  if (u.op === 'move') {
    void qc.invalidateQueries({ queryKey: ['queue'] })
    return
  }
  qc.setQueryData<Paged<QueueItem>>(['queue'], (data) => {
    if (!data) return data
    if (u.op === 'add' && u.item) {
      const pages = data.pages.slice()
      const last = pages[pages.length - 1]
      if (last.nextCursor === null && !pages.some((p) => p.items.some((i) => i.id === u.item!.id))) {
        pages[pages.length - 1] = { ...last, items: [...last.items, u.item] }
      }
      return { ...data, pages }
    }
    const gone = u.op === 'remove' ? u.id : u.startedId
    return mapPages(data, (items) => items.filter((i) => i.id !== gone))
  })
}

function applySong(qc: QueryClient, song: SongRow) {
  qc.setQueriesData<Paged<SongRow>>({ queryKey: ['songs'] }, (d) => mapPages(d, (items) => items.map((s) => (s.id === song.id ? song : s))))
  qc.setQueryData<Paged<QueueItem>>(['queue'], (d) => mapPages(d, (items) => items.map((q) => (q.song.id === song.id ? { ...q, song } : q))))
}

export const useParty = defineStore('party', () => {
  const qc = useQueryClient()
  const name = ref(read('kd_name'))
  // The host stays unlocked on this phone until they lock it.
  const pin = ref(read('kd_pin'))
  setHostPin(pin.value)
  const connected = ref(false)
  const network = ref<Network>({ online: false, mediaOk: true })
  const player = ref<PlayerState | null>(null)
  const positionMs = ref(0)
  const toast = ref('')
  const mineIds = ref(new Set<number>())
  const clientTag = ref('')
  let toastTimer = 0

  api<{ clientTag: string }>('/api/session')
    .then((s) => (clientTag.value = s.clientTag))
    .catch(() => {})

  function setName(v: string) {
    name.value = v
    write('kd_name', v)
  }

  /** Songs waiting or playing, so they can be shown as added. */
  const queuedIds = ref(new Set<string>())
  const myTurn = ref<MyTurn>({ waiting: false })
  let queuedTimer = 0

  function refreshQueued() {
    window.clearTimeout(queuedTimer)
    queuedTimer = window.setTimeout(() => {
      api<{ ids: string[] }>('/api/queue/songs').then(
        (r) => (queuedIds.value = new Set(r.ids)),
        () => {},
      )
      api<MyTurn>('/api/queue/mine').then(
        (r) => (myTurn.value = r),
        () => {},
      )
    }, 150)
  }

  const singingNow = computed(() => {
    const c = player.value?.current
    return !!c && (mineIds.value.has(c.itemId) || (!!c.owner && c.owner === clientTag.value))
  })
  /** Songs before this guest's, including the one playing. Null with none waiting. */
  const songsUntilMine = computed(() => {
    if (singingNow.value || !myTurn.value.waiting) return null
    return (myTurn.value.ahead ?? 0) + (player.value?.current ? 1 : 0)
  })
  const turnText = computed(() => {
    if (singingNow.value) return "You're singing now"
    const n = songsUntilMine.value
    if (n === null) return ''
    if (n <= 1) return `You're next: ${myTurn.value.title}`
    return `${n} songs until yours`
  })

  // A nudge once when this guest moves to next in line.
  watch(songsUntilMine, (now, before) => {
    if (now === 1 && (before === null || before > 1)) {
      say("You're up next!")
      navigator.vibrate?.([120, 60, 120])
    }
  })

  function markQueued(id: string) {
    queuedIds.value = new Set(queuedIds.value).add(id)
  }

  function setPin(v: string) {
    pin.value = v
    write('kd_pin', v)
    setHostPin(v)
  }

  function say(text: string) {
    toast.value = text
    window.clearTimeout(toastTimer)
    toastTimer = window.setTimeout(() => (toast.value = ''), 2600)
  }

  function handle(e: ServerEvent) {
    switch (e.type) {
      case 'snapshot':
        network.value = e.data.network
        player.value = e.data.player
        positionMs.value = e.data.player.positionMs
        void qc.invalidateQueries({ queryKey: ['queue'] })
        refreshQueued()
        break
      case 'player.state':
        player.value = e.data
        break
      case 'player.position':
        positionMs.value = e.data.ms
        break
      case 'queue.updated':
        applyQueue(qc, e.data)
        refreshQueued()
        break
      case 'song.state':
        applySong(qc, e.data.song)
        break
      case 'network.status':
        network.value = e.data
        break
    }
  }

  new Live(`/ws?client=remote&room=${encodeURIComponent(room)}`, handle, (up) => (connected.value = up))

  return { name, pin, connected, network, player, positionMs, toast, mineIds, clientTag, queuedIds, refreshQueued, markQueued, turnText, setName, setPin, say }
})
