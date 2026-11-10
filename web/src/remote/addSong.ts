import { post } from './api'
import { useParty } from './party'
import type { SongRow } from '../shared/types'

/** Adds a song to the queue once, marking it as added straight away. */
export function useAddToQueue() {
  const party = useParty()
  return async (id: string, body: object, done: string) => {
    if (party.queuedIds.has(id)) {
      party.say('Already in the queue')
      return
    }
    party.markQueued(id)
    try {
      const res = await post<{ id: number }>('/api/queue', { ...body, singer: party.name })
      party.mineIds.add(res.id)
      party.say(done)
    } catch (e) {
      party.say((e as Error).message)
      party.refreshQueued()
    }
  }
}

/** Adds a library song to the queue under the guest's name. */
export function useAddSong() {
  const add = useAddToQueue()
  return (song: SongRow) => add(song.id, { songId: song.id }, `Added ${song.title}`)
}
