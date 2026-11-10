import type { ServerEvent } from './types'

/** Reconnecting WebSocket. Every connect delivers a fresh snapshot. */
export class Live {
  private ws: WebSocket | null = null
  private retry = 500
  private closed = false

  constructor(
    private url: string,
    private onEvent: (e: ServerEvent) => void,
    private onConnection?: (up: boolean) => void,
  ) {
    this.connect()
  }

  private connect() {
    const scheme = location.protocol === 'https:' ? 'wss' : 'ws'
    const ws = new WebSocket(`${scheme}://${location.host}${this.url}`)
    this.ws = ws
    ws.onopen = () => {
      this.retry = 500
      this.onConnection?.(true)
    }
    ws.onmessage = (m) => {
      try {
        this.onEvent(JSON.parse(m.data) as ServerEvent)
      } catch (err) {
        console.error('bad server message', err)
      }
    }
    ws.onclose = () => {
      this.onConnection?.(false)
      if (this.closed) return
      setTimeout(() => this.connect(), this.retry)
      this.retry = Math.min(this.retry * 2, 5000)
    }
  }

  send(data: unknown) {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(data))
  }

  close() {
    this.closed = true
    this.ws?.close()
  }
}
