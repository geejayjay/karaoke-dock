import { wsUrl } from './endpoint'
import type { ServerEvent } from './types'

/** Reconnecting WebSocket. Every connect delivers a fresh snapshot. */
export class Live {
  private ws: WebSocket | null = null
  private retry = 500
  private timer = 0
  private closed = false

  constructor(
    private url: string,
    private onEvent: (e: ServerEvent) => void,
    private onConnection?: (up: boolean) => void,
  ) {
    this.connect()
  }

  private connect() {
    if (this.closed) return
    const ws = new WebSocket(wsUrl(this.url))
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
      const jitter = this.retry * (0.8 + Math.random() * 0.4)
      this.timer = window.setTimeout(() => this.connect(), Math.round(jitter))
      this.retry = Math.min(this.retry * 2, 5000)
    }
  }

  send(data: unknown) {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(data))
  }

  close() {
    this.closed = true
    window.clearTimeout(this.timer)
    this.ws?.close()
  }
}
