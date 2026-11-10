import { describe, expect, it } from 'vitest'
import { buildSegments } from './progress'
import type { LyricLine } from '../shared/types'

const line = (startMs: number, endMs: number, voice?: string): LyricLine => ({ startMs, endMs, voice, words: [] })

describe('buildSegments', () => {
  it('merges close lines and keeps music breaks', () => {
    const segs = buildSegments([line(0, 10000), line(11000, 20000), line(50000, 60000)], 100000)
    expect(segs).toEqual([
      { from: 0, to: 0.2, color: null },
      { from: 0.5, to: 0.6, color: null },
    ])
  })

  it('splits where the part changes', () => {
    const segs = buildSegments([line(0, 10000, 'a'), line(10000, 20000, 'b')], 100000)
    expect(segs.length).toBe(2)
    expect(segs[0].color).not.toBe(segs[1].color)
  })

  it('applies the timing offset and clamps to the song', () => {
    const segs = buildSegments([line(95000, 105000)], 100000, 1000)
    expect(segs[0].from).toBeCloseTo(0.96)
    expect(segs[0].to).toBe(1)
  })
})
