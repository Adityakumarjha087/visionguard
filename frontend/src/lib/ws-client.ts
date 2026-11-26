import type { WsClientMessage, WsMessage } from "./types";

export type WsStatus = "idle" | "connecting" | "open" | "closed";

type MessageOf<T extends WsMessage["type"]> = Extract<WsMessage, { type: T }>;
type Handler<T extends WsMessage["type"]> = (msg: MessageOf<T>) => void;
type AnyHandler = (msg: WsMessage) => void;

export interface WsClientOptions {
  /** Returns the URL to connect to (called on every (re)connect so fresh tokens are used). null = skip. */
  url: () => string | null | Promise<string | null>;
  WebSocketImpl?: typeof WebSocket;
  pingIntervalMs?: number;
  baseDelayMs?: number;
  maxDelayMs?: number;
  /** jitter factor in [0, 1); 0 disables jitter (useful for tests) */
  jitter?: number;
}

/** Exponential backoff: base * 2^attempt capped at max, with optional +/- jitter. */
export function backoffDelay(attempt: number, base: number, max: number, jitter = 0, rand: () => number = Math.random): number {
  const raw = Math.min(max, base * 2 ** attempt);
  if (!jitter) return raw;
  const delta = raw * jitter * (rand() * 2 - 1);
  return Math.max(0, Math.round(raw + delta));
}

/**
 * Small typed WebSocket client:
 * - reconnects with exponential backoff
 * - sends {"type":"ping"} every 25s while open
 * - ref-counted camera subscriptions that are replayed after reconnect
 * - typed dispatch per message type
 */
export class ReconnectingWsClient {
  private ws: WebSocket | null = null;
  private status: WsStatus = "idle";
  private attempt = 0;
  private stopped = true;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private pingTimer: ReturnType<typeof setInterval> | null = null;
  private handlers = new Map<string, Set<AnyHandler>>();
  private statusListeners = new Set<(s: WsStatus) => void>();
  private subs = new Map<string, number>();
  private readonly opts: Required<Omit<WsClientOptions, "WebSocketImpl">> & { WebSocketImpl?: typeof WebSocket };

  constructor(opts: WsClientOptions) {
    this.opts = {
      pingIntervalMs: 25_000,
      baseDelayMs: 1_000,
      maxDelayMs: 30_000,
      jitter: 0.2,
      ...opts,
    };
  }

  getStatus(): WsStatus {
    return this.status;
  }

  start(): void {
    if (!this.stopped) return;
    this.stopped = false;
    void this.connect();
  }

  stop(): void {
    this.stopped = true;
    this.clearTimers();
    const ws = this.ws;
    this.ws = null;
    if (ws) {
      ws.onopen = ws.onclose = ws.onmessage = ws.onerror = null;
      try {
        ws.close(1000, "client stop");
      } catch {
        /* ignore */
      }
    }
    this.setStatus("closed");
  }

  /** Force a fresh connection (e.g. after login with a new token). */
  restart(): void {
    this.stop();
    this.attempt = 0;
    this.start();
  }

  on<T extends WsMessage["type"]>(type: T, handler: Handler<T>): () => void {
    let set = this.handlers.get(type);
    if (!set) {
      set = new Set();
      this.handlers.set(type, set);
    }
    const h = handler as AnyHandler;
    set.add(h);
    return () => set.delete(h);
  }

  onStatus(listener: (s: WsStatus) => void): () => void {
    this.statusListeners.add(listener);
    listener(this.status);
    return () => this.statusListeners.delete(listener);
  }

  subscribe(cameraId: string): () => void {
    const n = this.subs.get(cameraId) ?? 0;
    this.subs.set(cameraId, n + 1);
    if (n === 0) this.send({ type: "subscribe", camera_id: cameraId });
    let done = false;
    return () => {
      if (done) return;
      done = true;
      const cur = this.subs.get(cameraId) ?? 0;
      if (cur <= 1) {
        this.subs.delete(cameraId);
        this.send({ type: "unsubscribe", camera_id: cameraId });
      } else this.subs.set(cameraId, cur - 1);
    };
  }

  send(msg: WsClientMessage): boolean {
    const ws = this.ws;
    if (!ws || ws.readyState !== 1) return false;
    ws.send(JSON.stringify(msg));
    return true;
  }

  private setStatus(s: WsStatus) {
    if (this.status === s) return;
    this.status = s;
    this.statusListeners.forEach((l) => l(s));
  }

  private clearTimers() {
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer);
    if (this.pingTimer) clearInterval(this.pingTimer);
    this.reconnectTimer = null;
    this.pingTimer = null;
  }

  private async connect(): Promise<void> {
    if (this.stopped) return;
    this.setStatus("connecting");
    let url: string | null = null;
    try {
      url = await this.opts.url();
    } catch {
      url = null;
    }
    if (this.stopped) return;
    if (!url) {
      this.scheduleReconnect();
      return;
    }
    const Impl = this.opts.WebSocketImpl ?? globalThis.WebSocket;
    let ws: WebSocket;
    try {
      ws = new Impl(url);
    } catch {
      this.scheduleReconnect();
      return;
    }
    this.ws = ws;
    ws.onopen = () => {
      this.attempt = 0;
      this.setStatus("open");
      for (const id of this.subs.keys()) this.send({ type: "subscribe", camera_id: id });
      this.pingTimer = setInterval(() => this.send({ type: "ping" }), this.opts.pingIntervalMs);
    };
    ws.onmessage = (ev: MessageEvent) => this.dispatch(ev.data);
    ws.onerror = () => {
      /* onclose follows */
    };
    ws.onclose = () => {
      if (this.ws !== ws) return;
      this.ws = null;
      if (this.pingTimer) clearInterval(this.pingTimer);
      this.pingTimer = null;
      this.setStatus("closed");
      this.scheduleReconnect();
    };
  }

  private scheduleReconnect() {
    if (this.stopped) return;
    this.setStatus("closed");
    const delay = backoffDelay(this.attempt, this.opts.baseDelayMs, this.opts.maxDelayMs, this.opts.jitter);
    this.attempt++;
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      void this.connect();
    }, delay);
  }

  private dispatch(raw: unknown) {
    if (typeof raw !== "string") return;
    let msg: WsMessage;
    try {
      msg = JSON.parse(raw) as WsMessage;
    } catch {
      return;
    }
    if (!msg || typeof msg.type !== "string") return;
    this.handlers.get(msg.type)?.forEach((h) => {
      try {
        h(msg);
      } catch (e) {
        console.error("ws handler failed", e);
      }
    });
    this.handlers.get("*")?.forEach((h) => h(msg));
  }
}
