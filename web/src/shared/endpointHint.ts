import { connectServer as doConnect, parseServerInput } from './endpoint'

const HINT_KEY = 'kd_last_server'
const DEFAULT_PORT = 7464

export interface DiscoveredServer {
  origin: string
  name: string
  roomRequired: boolean
}

interface NativeBridge {
  discover?: (port: number) => string
}

export function readSavedHint(): string {
  try {
    return localStorage.getItem(HINT_KEY) ?? ''
  } catch {
    return ''
  }
}

export async function connectServer(rawInput: string, roomInput = '') {
  const res = await doConnect(rawInput, roomInput)
  if (res.ok) {
    try {
      localStorage.setItem(HINT_KEY, rawInput.trim())
    } catch {
      // ignore
    }
  }
  return res
}

async function pingOrigin(origin: string, timeoutMs = 1200): Promise<DiscoveredServer | null> {
  const ctrl = new AbortController()
  const t = setTimeout(() => ctrl.abort(), timeoutMs)
  try {
    const res = await fetch(`${origin}/api/ping`, { signal: ctrl.signal, cache: 'no-store' })
    if (!res.ok) return null
    const data = (await res.json().catch(() => null)) as { app?: string; name?: string; roomRequired?: boolean } | null
    if (data?.app !== 'karaokedock') return null
    const host = new URL(origin).host
    return { origin, name: data.name || host, roomRequired: !!data.roomRequired }
  } catch {
    return null
  } finally {
    clearTimeout(t)
  }
}

/**
 * Automatically looks for KaraokeDock servers on the local Wi-Fi network:
 * 1. Native Android UDP broadcast discovery (instant across any LAN subnet)
 * 2. Saved server hint & .local mDNS hostnames
 * 3. Fast HTTP subnet probe if UDP broadcast is filtered by the router
 */
export async function discoverServers(port = DEFAULT_PORT): Promise<DiscoveredServer[]> {
  const map = new Map<string, DiscoveredServer>()
  const add = (s: DiscoveredServer | null) => {
    if (s && !map.has(s.origin)) map.set(s.origin, s)
  }

  // Yield a frame so the UI renders its scanning state before native bridge blocks.
  await new Promise((r) => setTimeout(r, 20))

  const native = (globalThis as unknown as { KaraokeDockNative?: NativeBridge }).KaraokeDockNative
  if (native?.discover) {
    try {
      const list = JSON.parse(native.discover(port)) as DiscoveredServer[]
      for (const item of list) add(item)
    } catch {
      // ignore malformed bridge output
    }
  }

  const hintParsed = parseServerInput(readSavedHint())
  const quickCandidates = new Set<string>([
    `http://karaokedock.local:${port}`,
    `http://karaoke.local:${port}`,
  ])
  if (hintParsed?.origin) quickCandidates.add(hintParsed.origin)

  const quick = await Promise.all([...quickCandidates].map((o) => pingOrigin(o, 1100)))
  for (const item of quick) add(item)

  if (map.size > 0) return [...map.values()]

  // Fallback HTTP subnet scan when UDP broadcast is blocked on guest/mesh Wi-Fi.
  const prefixes = new Set<string>()
  if (hintParsed?.origin) {
    const m = /^http:\/\/(\d+\.\d+\.\d+)\.\d+/.exec(hintParsed.origin)
    if (m) prefixes.add(m[1])
  }
  prefixes.add('192.168.1')
  prefixes.add('192.168.0')

  for (const prefix of prefixes) {
    for (let start = 1; start <= 254; start += 42) {
      const batch: Promise<DiscoveredServer | null>[] = []
      for (let host = start; host < start + 42 && host <= 254; host++) {
        batch.push(pingOrigin(`http://${prefix}.${host}:${port}`, 420))
      }
      const found = await Promise.all(batch)
      for (const item of found) add(item)
      if (map.size > 0) return [...map.values()]
    }
  }

  return [...map.values()]
}
