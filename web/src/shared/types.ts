export interface Page<T> {
  items: T[]
  nextCursor: string | null
}

export interface SongRow {
  id: string
  title: string
  artist: string
  album?: string
  durationMs: number
  state: string
  hasLyrics: boolean
  karaoke?: boolean
  coverId?: string
}

export interface QueueItem {
  id: number
  singer: string
  status: string
  song: SongRow
  mine?: boolean
}

export interface ArtistRow {
  id: string
  name: string
  gender?: string
  coverId?: string
  hasCustom?: boolean
  songCount?: number
}

export interface AlbumRow {
  id: string
  title: string
  artistId: string
  artist: string
  year?: number
  hasCover: boolean
  songCount?: number
}

export interface SearchResult {
  videoId: string
  title: string
  channel: string
  durationMs: number
  karaoke: boolean
  /** Set when this song is already in the library. Use it instead of YouTube's details. */
  song?: SongRow
}

export interface Source {
  kind: 'audio' | 'video'
  audioUrl?: string
  videoUrl?: string
  videoMuted?: boolean
  quickMode?: boolean
  trimStartMs?: number
  trimEndMs?: number
}

export interface CurrentSong {
  itemId: number
  singer: string
  owner?: string
  songId: string
  title: string
  artist: string
  durationMs: number
  state: string
  accentColor: string
  coverId?: string
  hasBg: boolean
  hasLyrics: boolean
  hasGuide: boolean
  karaoke: boolean
  source: Source
}

export interface PlayerState {
  current: CurrentSong | null
  paused: boolean
  positionMs: number
  seekSeq: number
  seekMs: number
  audioMode: string
}

export interface Network {
  online: boolean
  mediaOk: boolean
}

export interface PlaybackSettings {
  animationLevel: 'auto' | 'full' | 'lite'
  countdown: boolean
  countdownSeconds: number
}

export interface Snapshot {
  room: string
  joinUrl: string
  player: PlayerState
  upNext: QueueItem[]
  network: Network
  playback: PlaybackSettings
}

export interface LyricWord {
  text: string
  startMs: number
  endMs: number
}

export interface LyricLine {
  startMs: number
  endMs: number
  voice?: string
  words: LyricWord[]
  backing?: LyricWord[]
  backingOnly?: boolean
  plain?: boolean
}

export interface Voice {
  id: string
  label: string
  source: string
}

export interface LyricsDoc {
  version: number
  source: string
  timing: 'line' | 'word'
  language?: string
  offsetMs: number
  voices: Voice[]
  lines: LyricLine[]
}

export type ServerEvent =
  | { type: 'snapshot'; data: Snapshot }
  | { type: 'player.state'; data: PlayerState }
  | { type: 'player.position'; data: { ms: number } }
  | { type: 'queue.updated'; data: QueueUpdate }
  | { type: 'song.state'; data: { song: SongRow; error?: string } }
  | { type: 'song.progress'; data: { songId: string; stage: string; percent: number } }
  | { type: 'song.failed'; data: { songId: string; error: string } }
  | { type: 'song.lyrics'; data: { songId: string } }
  | { type: 'network.status'; data: Network }
  | { type: 'reaction'; data: { emoji: string } }
  | { type: 'settings.playback'; data: { playback: PlaybackSettings; joinUrl: string } }

export interface QueueUpdate {
  op: 'add' | 'remove' | 'move' | 'start'
  item?: QueueItem
  id?: number
  endedId?: number
  startedId?: number
  upNext: QueueItem[]
}

/** Short, plain status label for a song. */
export function badge(state: string): string {
  switch (state) {
    case 'queued':
    case 'downloading':
      return 'Getting song'
    case 'playable':
    case 'separating':
      return 'Quick mode'
    case 'karaoke_ready':
    case 'aligning':
    case 'complete':
      return 'Ready to sing'
    case 'failed':
      return "Couldn't add"
    default:
      return ''
  }
}

export function formatTime(ms: number): string {
  const total = Math.max(0, Math.round(ms / 1000))
  const m = Math.floor(total / 60)
  const s = total % 60
  return `${m}:${s < 10 ? '0' : ''}${s}`
}
