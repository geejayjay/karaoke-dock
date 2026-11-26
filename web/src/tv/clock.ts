/**
 * Smooths the media element's clock. currentTime updates in coarse steps, so
 * between readings the clock runs on performance.now(), and it only corrects
 * when off by more than 30 ms.
 */
export class SmoothClock {
  private baseMedia = 0
  private basePerf = 0
  private running = false

  constructor(private readPositionMs: () => number) {}

  start() {
    this.resync()
    this.running = true
  }

  stop() {
    this.resync()
    this.running = false
  }

  resync() {
    this.baseMedia = this.readPositionMs()
    this.basePerf = performance.now()
  }

  /** Current position in ms. */
  now(): number {
    if (!this.running) return this.baseMedia
    return this.baseMedia + (performance.now() - this.basePerf)
  }

  /** Compares with the real clock. Returns true when it had to correct. */
  correct(): boolean {
    if (!this.running) return false
    if (Math.abs(this.now() - this.readPositionMs()) > 30) {
      this.resync()
      return true
    }
    return false
  }
}
