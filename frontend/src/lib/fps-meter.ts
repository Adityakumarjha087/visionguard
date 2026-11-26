/** Measures message arrival rate over a sliding window (client-side FPS). */
export class FpsMeter {
  private stamps: number[] = [];
  constructor(private readonly windowMs = 2_000) {}

  tick(now: number = performance.now()): number {
    this.stamps.push(now);
    while (this.stamps.length && now - this.stamps[0]! > this.windowMs) this.stamps.shift();
    return this.value(now);
  }

  value(now: number = performance.now()): number {
    const recent = this.stamps.filter((t) => now - t <= this.windowMs);
    if (recent.length < 2) return 0;
    const span = recent[recent.length - 1]! - recent[0]!;
    return span > 0 ? ((recent.length - 1) * 1000) / span : 0;
  }
}
