import type { SongRow } from '../shared/types'

export type Filter = 'all' | 'working' | 'done' | 'problem'
export type Part = 'waiting' | 'working' | 'done' | 'none' | 'failed'

export interface SongArtist {
  id: string
  name: string
  gender?: string
}

export interface StatusRow extends SongRow {
  albumId?: string
  genre?: string
  language?: string
  artists?: SongArtist[]
  audio: Part
  lyrics: Part
  overall: Exclude<Filter, 'all'>
  step?: string
  percent?: number
  message?: string
  timedLines?: number
  lyricLines?: number
}

export const REFRESH_MS = 3000

/** What's happening to a song, in a few words. Empty once it's processed. */
export function statusLine(s: StatusRow): string {
  if (s.overall === 'problem') return s.message ?? ''
  if (s.overall === 'done') return ''
  return s.percent ? `${s.step} · ${Math.round(s.percent)}%` : (s.step ?? '')
}
