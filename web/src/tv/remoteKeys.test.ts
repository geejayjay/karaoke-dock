import { describe, expect, it } from 'vitest'
import { remoteAction } from './remoteKeys'

describe('remoteAction', () => {
  it('reads standard key names', () => {
    expect(remoteAction({ key: 'MediaPlayPause', keyCode: 0 })).toBe('toggle')
    expect(remoteAction({ key: 'MediaTrackNext', keyCode: 0 })).toBe('next')
    expect(remoteAction({ key: 'ArrowLeft', keyCode: 37 })).toBe('back')
  })
  it('reads TV-only key codes', () => {
    expect(remoteAction({ key: 'Unidentified', keyCode: 415 })).toBe('play') // LG and Samsung play
    expect(remoteAction({ key: 'Unidentified', keyCode: 413 })).toBe('stop')
    expect(remoteAction({ key: 'Unidentified', keyCode: 417 })).toBe('forward')
  })
  it('leaves other keys alone', () => {
    expect(remoteAction({ key: 'Escape', keyCode: 27 })).toBeNull()
    expect(remoteAction({ key: 'a', keyCode: 65 })).toBeNull()
  })
})
