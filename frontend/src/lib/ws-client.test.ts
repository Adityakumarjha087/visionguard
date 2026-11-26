import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ReconnectingWsClient, backoffDelay } from "./ws-client";

class MockWebSocket {
  static instances: MockWebSocket[] = [];
  readyState = 0;
  sent: string[] = [];
  onopen: ((e: Event) => void) | null = null;
  onclose: ((e: CloseEvent) => void) | null = null;
  onmessage: ((e: MessageEvent) => void) | null = null;
  onerror: ((e: Event) => void) | null = null;
  constructor(public url: string) {
    MockWebSocket.instances.push(this);
  }
  send(d: string) {
    this.sent.push(d);
  }
  close() {
    this.readyState = 3;
  }
  // helpers
  open() {
    this.readyState = 1;
    this.onopen?.(new Event("open"));
  }
  drop() {
    this.readyState = 3;
    this.onclose?.(new CloseEvent("close"));
  }
  emit(obj: unknown) {
    this.onmessage?.(new MessageEvent("message", { data: JSON.stringify(obj) }));
  }
  get messages() {
    return this.sent.map((s) => JSON.parse(s) as Record<string, unknown>);
  }
}

const last = () => MockWebSocket.instances[MockWebSocket.instances.length - 1]!;

function make(url: () => string | null = () => "ws://x/api/v1/ws?access_token=t") {
  return new ReconnectingWsClient({
    url,
    WebSocketImpl: MockWebSocket as unknown as typeof WebSocket,
    baseDelayMs: 100,
    maxDelayMs: 1_000,
    jitter: 0,
  });
}

beforeEach(() => {
  vi.useFakeTimers();
  MockWebSocket.instances = [];
});
afterEach(() => vi.useRealTimers());

describe("backoffDelay", () => {
  it("doubles per attempt and caps at max", () => {
    expect([0, 1, 2, 3, 4, 10].map((a) => backoffDelay(a, 100, 1000))).toEqual([100, 200, 400, 800, 1000, 1000]);
  });
  it("applies bounded jitter", () => {
    expect(backoffDelay(2, 100, 1000, 0.5, () => 1)).toBe(600);
    expect(backoffDelay(2, 100, 1000, 0.5, () => 0)).toBe(200);
  });
});

describe("ReconnectingWsClient", () => {
  it("connects, reports status and dispatches typed messages by type", async () => {
    const c = make();
    const statuses: string[] = [];
    c.onStatus((s) => statuses.push(s));
    const alerts = vi.fn();
    const frames = vi.fn();
    c.on("alert", alerts);
    c.on("frame", frames);
    c.start();
    await vi.advanceTimersByTimeAsync(0);
    last().open();
    last().emit({ type: "alert", data: { id: "i1" } });
    last().emit({ type: "pong" });
    expect(alerts).toHaveBeenCalledWith({ type: "alert", data: { id: "i1" } });
    expect(frames).not.toHaveBeenCalled();
    expect(statuses).toEqual(["idle", "connecting", "open"]);
    c.stop();
  });

  it("ignores malformed messages", async () => {
    const c = make();
    const any = vi.fn();
    c.on("alert", any);
    c.start();
    await vi.advanceTimersByTimeAsync(0);
    last().open();
    last().onmessage?.(new MessageEvent("message", { data: "not json" }));
    expect(any).not.toHaveBeenCalled();
    c.stop();
  });

  it("reconnects with exponential backoff and resubscribes cameras", async () => {
    const c = make();
    c.start();
    await vi.advanceTimersByTimeAsync(0);
    last().open();
    c.subscribe("cam-1");
    expect(last().messages).toContainEqual({ type: "subscribe", camera_id: "cam-1" });

    last().drop();
    expect(c.getStatus()).toBe("closed");
    await vi.advanceTimersByTimeAsync(99);
    expect(MockWebSocket.instances).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(MockWebSocket.instances).toHaveLength(2);

    // second failure before open -> 200ms delay
    last().drop();
    await vi.advanceTimersByTimeAsync(199);
    expect(MockWebSocket.instances).toHaveLength(2);
    await vi.advanceTimersByTimeAsync(1);
    expect(MockWebSocket.instances).toHaveLength(3);

    last().open();
    expect(last().messages).toEqual([{ type: "subscribe", camera_id: "cam-1" }]);
    c.stop();
  });

  it("sends a ping every 25 seconds while open", async () => {
    const c = new ReconnectingWsClient({ url: () => "ws://x", WebSocketImpl: MockWebSocket as unknown as typeof WebSocket });
    c.start();
    await vi.advanceTimersByTimeAsync(0);
    last().open();
    await vi.advanceTimersByTimeAsync(25_000);
    expect(last().messages).toEqual([{ type: "ping" }]);
    await vi.advanceTimersByTimeAsync(25_000);
    expect(last().messages).toHaveLength(2);
    c.stop();
  });

  it("ref-counts subscriptions and unsubscribes on last release", async () => {
    const c = make();
    c.start();
    await vi.advanceTimersByTimeAsync(0);
    last().open();
    const a = c.subscribe("cam");
    const b = c.subscribe("cam");
    a();
    a(); // idempotent
    expect(last().messages.filter((m) => m.type === "unsubscribe")).toHaveLength(0);
    b();
    expect(last().messages).toEqual([
      { type: "subscribe", camera_id: "cam" },
      { type: "unsubscribe", camera_id: "cam" },
    ]);
    c.stop();
  });

  it("does not reconnect after stop() and retries later when no token is available", async () => {
    let token: string | null = null;
    const c = make(() => (token ? `ws://x?access_token=${token}` : null));
    c.start();
    await vi.advanceTimersByTimeAsync(0);
    expect(MockWebSocket.instances).toHaveLength(0);
    token = "abc";
    await vi.advanceTimersByTimeAsync(100);
    expect(MockWebSocket.instances).toHaveLength(1);
    expect(last().url).toContain("access_token=abc");
    c.stop();
    last().drop();
    await vi.advanceTimersByTimeAsync(5_000);
    expect(MockWebSocket.instances).toHaveLength(1);
  });
});
