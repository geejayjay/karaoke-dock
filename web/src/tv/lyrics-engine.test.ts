import { describe, expect, it } from 'vitest'
import { closeGaps, lineIndexAt, needsSpace } from './lyrics-engine'
import type { LyricLine } from '../shared/types'

const line = (startMs: number, endMs: number): LyricLine => ({ startMs, endMs, words: [] })

describe('lineIndexAt', () => {
  const lines = [line(1000, 2000), line(3000, 4000), line(5000, 6000)]
  it('finds the current line', () => {
    expect(lineIndexAt(lines, 0)).toBe(0)
    expect(lineIndexAt(lines, 3500)).toBe(1)
    expect(lineIndexAt(lines, 4500)).toBe(1)
    expect(lineIndexAt(lines, 99999)).toBe(2)
  })
})

describe('closeGaps', () => {
  it('joins words closer than 120 ms and keeps real gaps', () => {
    const out = closeGaps([
      { text: 'a', startMs: 0, endMs: 100 },
      { text: 'b', startMs: 150, endMs: 200 },
      { text: 'c', startMs: 500, endMs: 600 },
    ])
    expect(out[0].endMs).toBe(150)
    expect(out[1].endMs).toBe(200)
  })
})

describe('needsSpace', () => {
  it('adds spaces only between non-CJK words', () => {
    expect(needsSpace('hello', 'world')).toBe(true)
    expect(needsSpace('你', '好')).toBe(false)
  })
})
