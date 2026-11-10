import { ref } from 'vue'
import type { Page } from '../shared/types'

export const signedIn = ref<boolean | null>(null)

export class ApiError extends Error {
  constructor(
    message: string,
    public fields?: Record<string, string>,
  ) {
    super(message)
  }
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  if (init.body && !(init.body instanceof FormData)) headers.set('Content-Type', 'application/json')
  const res = await fetch(path, { ...init, headers, credentials: 'same-origin' })
  if (res.status === 401 && path !== '/api/admin/login' && path !== '/api/admin/password') signedIn.value = false
  if (!res.ok) {
    const body = await res.json().catch(() => null)
    throw new ApiError(body?.error ?? 'Please fix the highlighted fields.', body?.fields)
  }
  return (await res.json()) as T
}

export function send<T>(method: string, path: string, body: unknown = {}): Promise<T> {
  return api<T>(path, { method, body: JSON.stringify(body) })
}

export function pageFetcher<T>(path: string, params: Record<string, string | undefined> = {}) {
  return (cursor: string | null) => {
    const q = new URLSearchParams()
    for (const [k, v] of Object.entries(params)) if (v) q.set(k, v)
    if (cursor) q.set('cursor', cursor)
    return api<Page<T>>(`${path}?${q.toString()}`)
  }
}
