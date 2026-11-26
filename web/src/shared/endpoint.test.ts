import { describe, expect, it } from 'vitest'
import { parseServerInput, serverOrigin, serverUrl } from './endpoint'

describe('endpoint helpers', () => {
  it('parses bare IP and adds default KaraokeDock port 7464', () => {
    expect(parseServerInput('192.168.1.42')).toEqual({
      origin: 'http://192.168.1.42:7464',
      room: '',
    })
  })

  it('parses full join link and extracts room code', () => {
    expect(parseServerInput('http://192.168.1.42:7464/r/SING')).toEqual({
      origin: 'http://192.168.1.42:7464',
      room: 'SING',
    })
  })

  it('preserves custom ports and https domains', () => {
    expect(parseServerInput('https://karaoke.example.com/r/party1')).toEqual({
      origin: 'https://karaoke.example.com',
      room: 'party1',
    })
    expect(parseServerInput('10.0.0.8:9000')).toEqual({
      origin: 'http://10.0.0.8:9000',
      room: '',
    })
  })

  it('prefixes relative paths only when serverOrigin is set', () => {
    serverOrigin.value = ''
    expect(serverUrl('/api/songs')).toBe('/api/songs')
    serverOrigin.value = 'http://192.168.1.10:7464'
    expect(serverUrl('/covers/abc')).toBe('http://192.168.1.10:7464/covers/abc')
    expect(serverUrl('https://i.ytimg.com/vi/abc/default.jpg')).toBe('https://i.ytimg.com/vi/abc/default.jpg')
    serverOrigin.value = ''
  })
})
