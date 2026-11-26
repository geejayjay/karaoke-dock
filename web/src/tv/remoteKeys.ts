/**
 * Buttons on a TV remote, as TV browsers report them. Most send standard key
 * names; older LG (webOS) and Samsung (Tizen) browsers and HbbTV send only
 * their own key codes, so both are listed.
 */
export type RemoteAction = 'toggle' | 'play' | 'pause' | 'next' | 'stop' | 'restart' | 'back' | 'forward'

const BY_KEY: Record<string, RemoteAction> = {
  Enter: 'toggle',
  ' ': 'toggle',
  MediaPlayPause: 'toggle',
  MediaPlay: 'play',
  Play: 'play',
  MediaPause: 'pause',
  Pause: 'pause',
  MediaTrackNext: 'next',
  ChannelUp: 'next',
  PageUp: 'next', // some remotes send their channel buttons as page keys
  MediaStop: 'stop',
  MediaTrackPrevious: 'restart',
  ChannelDown: 'restart',
  PageDown: 'restart',
  ArrowLeft: 'back',
  MediaRewind: 'back',
  ArrowRight: 'forward',
  MediaFastForward: 'forward',
}

const BY_CODE: Record<number, RemoteAction> = {
  13: 'toggle', // OK
  179: 'toggle', // play/pause
  10252: 'toggle', // Samsung play/pause
  415: 'play', // LG, Samsung, HbbTV
  19: 'pause',
  176: 'next',
  427: 'next', // channel up
  33: 'next',
  178: 'stop',
  413: 'stop', // LG, Samsung, HbbTV
  177: 'restart',
  428: 'restart', // channel down
  34: 'restart',
  37: 'back',
  412: 'back', // rewind
  227: 'back',
  39: 'forward',
  417: 'forward', // fast forward
  228: 'forward',
}

/** The action for a key press, or null for keys the TV leaves alone. */
export function remoteAction(e: Pick<KeyboardEvent, 'key' | 'keyCode'>): RemoteAction | null {
  return BY_KEY[e.key] ?? BY_CODE[e.keyCode] ?? null
}

/** How far the rewind and fast-forward buttons jump. */
export const JUMP_MS = 10000
