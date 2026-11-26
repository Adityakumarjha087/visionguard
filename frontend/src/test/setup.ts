import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach, vi } from "vitest";
import { tokenStore } from "@/lib/token-store";

// jsdom has no canvas implementation; drawing code no-ops on a null context.
Object.defineProperty(HTMLCanvasElement.prototype, "getContext", { value: vi.fn(() => null), writable: true });

// Radix Switch measures itself with ResizeObserver, which jsdom lacks.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
if (!("ResizeObserver" in globalThis)) {
  (globalThis as unknown as { ResizeObserver: typeof ResizeObserverStub }).ResizeObserver = ResizeObserverStub;
}

afterEach(() => {
  cleanup();
  window.localStorage.clear();
  tokenStore.invalidate();
});
