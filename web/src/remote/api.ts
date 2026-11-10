import { room } from './party'
import type { Page } from '../shared/types'

let hostPin = ''
export function setHostPin(pin: string) {
  hostPin = pin
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  headers.set('X-Room', room)
  if (hostPin) headers.set('X-Host-Pin', hostPin)
  if (init.body) headers.set('Content-Type', 'application/json')
  const res = await fetch(path, { ...init, headers, credentials: 'same-origin' })
  if (!res.ok) {
    const body = await res.json().catch(() => null)
    throw new Error(body?.error ?? "That didn't work. Try again.")
  }
  return (await res.json()) as T
}

export function pageFetcher<T>(path: string, params: Record<string, string | undefined> = {}) {
  return (cursor: string | null) => {
    const q = new URLSearchParams()
    for (const [k, v] of Object.entries(params)) if (v) q.set(k, v)
    if (cursor) q.set('cursor', cursor)
    return api<Page<T>>(`${path}?${q.toString()}`)
  }
}

export function post<T>(path: string, body: unknown = {}): Promise<T> {
  return api<T>(path, { method: 'POST', body: JSON.stringify(body) })
}
