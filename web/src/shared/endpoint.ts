import { computed, ref } from 'vue'

export type ClientMode = '' | 'mobile' | 'tv'

const rawMode = ((import.meta as unknown as { env?: Record<string, string> }).env?.VITE_CLIENT_MODE ?? '') as ClientMode

export const clientMode: ClientMode = rawMode === 'mobile' || rawMode === 'tv' ? rawMode : ''
export const isClientApp = clientMode !== ''

const SERVER_KEY = 'kd_server'
const ROOM_KEY = 'kd_room'
const DEVICE_KEY = 'kd_client_id'
const DEFAULT_PORT = 7464

function readStorage(key: string): string {
  try {
    return localStorage.getItem(key) ?? ''
  } catch {
    return ''
  }
}

function writeStorage(key: string, value: string) {
  try {
    if (value) localStorage.setItem(key, value)
    else localStorage.removeItem(key)
  } catch {
    // Storage may be restricted in private browsing.
  }
}

export const serverOrigin = ref(isClientApp ? readStorage(SERVER_KEY) : '')
export const savedRoom = ref(isClientApp ? readStorage(ROOM_KEY) : (typeof location !== 'undefined' ? (location.pathname.split('/')[2] ?? '') : ''))
export const switchedManually = ref(false)
export const needsServer = computed(() => isClientApp && !serverOrigin.value)

export function activeRoom(): string {
  return isClientApp ? savedRoom.value : (location.pathname.split('/')[2] ?? '')
}

/** Stable guest ID for standalone client apps where cross-origin cookies aren't used. */
export function deviceClientId(): string {
  let id = readStorage(DEVICE_KEY)
  if (id.length >= 16 && id.length <= 64) return id
  const bytes = new Uint8Array(16)
  if (typeof crypto !== 'undefined' && crypto.getRandomValues) {
    crypto.getRandomValues(bytes)
  } else {
    for (let i = 0; i < bytes.length; i++) bytes[i] = Math.floor(Math.random() * 256)
  }
  id = Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
  writeStorage(DEVICE_KEY, id)
  return id
}

/** Resolves an API, media, or cover path against the configured KaraokeDock server. */
export function serverUrl(path: string): string {
  if (!path || /^(?:https?:|data:|blob:)/i.test(path)) return path
  const base = serverOrigin.value
  if (!base) return path
  return `${base}${path.startsWith('/') ? path : `/${path}`}`
}

/** Resolves a WebSocket path against the configured server or current page host. */
export function wsUrl(path: string): string {
  const rel = path.startsWith('/') ? path : `/${path}`
  if (serverOrigin.value) {
    const u = new URL(serverOrigin.value)
    const scheme = u.protocol === 'https:' ? 'wss' : 'ws'
    return `${scheme}://${u.host}${rel}`
  }
  const scheme = location.protocol === 'https:' ? 'wss' : 'ws'
  return `${scheme}://${location.host}${rel}`
}

/** Parses an IP, host:port, or full join link (http://host:7464/r/CODE). */
export function parseServerInput(raw: string): { origin: string; room: string } | null {
  let text = raw.trim()
  if (!text) return null
  if (!/^https?:\/\//i.test(text)) {
    text = `http://${text}`
  }
  try {
    const u = new URL(text)
    if (!u.hostname) return null
    if (!u.port && u.protocol === 'http:' && (/^\d+\.\d+\.\d+\.\d+$/.test(u.hostname) || u.hostname === 'localhost' || u.hostname.endsWith('.local'))) {
      u.port = String(DEFAULT_PORT)
    }
    const m = /^\/r\/([^/?#]+)/i.exec(u.pathname)
    return { origin: `${u.protocol}//${u.host}`, room: m ? decodeURIComponent(m[1]) : '' }
  } catch {
    return null
  }
}

export interface ConnectResult {
  ok: boolean
  needRoom?: boolean
  error?: string
}

/** Verifies and saves a KaraokeDock server (and room code on mobile). */
export async function connectServer(rawInput: string, roomInput = ''): Promise<ConnectResult> {
  const parsed = parseServerInput(rawInput)
  if (!parsed) {
    return { ok: false, error: 'Enter a valid server address, like 192.168.1.50' }
  }
  const room = (roomInput.trim() || parsed.room).trim()
  const ctrl = new AbortController()
  const timer = setTimeout(() => ctrl.abort(), 5000)
  try {
    const ping = await fetch(`${parsed.origin}/api/ping`, { signal: ctrl.signal, cache: 'no-store' })
    if (!ping.ok) {
      return { ok: false, error: 'Could not reach KaraokeDock at that address.' }
    }
    const info = (await ping.json().catch(() => null)) as { app?: string; roomRequired?: boolean } | null
    if (info?.app !== 'karaokedock') {
      return { ok: false, error: 'That address is not running KaraokeDock.' }
    }
    if (clientMode === 'mobile' && info.roomRequired) {
      if (!room) {
        return { ok: false, needRoom: true, error: 'Enter the room code shown on the TV.' }
      }
      const check = await fetch(`${parsed.origin}/api/session`, {
        headers: { 'X-Room': room, 'X-Client-ID': deviceClientId() },
        signal: ctrl.signal,
        cache: 'no-store',
      })
      if (check.status === 403) {
        return { ok: false, needRoom: true, error: 'Wrong room code. Check the TV screen.' }
      }
      if (!check.ok) {
        return { ok: false, error: 'Server error while joining room.' }
      }
    }
    writeStorage(SERVER_KEY, parsed.origin)
    writeStorage(ROOM_KEY, room)
    switchedManually.value = false
    savedRoom.value = room
    serverOrigin.value = parsed.origin
    return { ok: true }
  } catch {
    return { ok: false, error: "Couldn't connect. Make sure you're on the same Wi-Fi." }
  } finally {
    clearTimeout(timer)
  }
}

export function disconnectServer() {
  switchedManually.value = true
  writeStorage(SERVER_KEY, '')
  serverOrigin.value = ''
}
