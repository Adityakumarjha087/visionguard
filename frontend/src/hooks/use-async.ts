"use client";
import { useCallback, useEffect, useRef, useState } from "react";

export interface AsyncState<T> {
  data: T | undefined;
  error: unknown;
  loading: boolean;
  reload: () => Promise<void>;
  setData: React.Dispatch<React.SetStateAction<T | undefined>>;
}

/** Minimal data-fetching hook: runs `fn` when deps change, ignores stale responses. */
export function useAsync<T>(fn: (signal: AbortSignal) => Promise<T>, deps: React.DependencyList, enabled = true): AsyncState<T> {
  const [data, setData] = useState<T | undefined>(undefined);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState<boolean>(enabled);
  const seq = useRef(0);
  const fnRef = useRef(fn);
  fnRef.current = fn;
  const ctrlRef = useRef<AbortController | null>(null);

  const run = useCallback(async () => {
    const id = ++seq.current;
    ctrlRef.current?.abort();
    const ctrl = new AbortController();
    ctrlRef.current = ctrl;
    setLoading(true);
    setError(null);
    try {
      const res = await fnRef.current(ctrl.signal);
      if (id === seq.current) setData(res);
    } catch (e) {
      if (id === seq.current && !(e instanceof DOMException && e.name === "AbortError")) setError(e);
    } finally {
      if (id === seq.current) setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (!enabled) {
      setLoading(false);
      return;
    }
    void run();
    return () => ctrlRef.current?.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, enabled]);

  return { data, error, loading, reload: run, setData };
}
