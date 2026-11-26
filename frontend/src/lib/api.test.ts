import { describe, expect, it, vi } from "vitest";
import { ApiError, buildQuery, createApiClient, incidentQuery } from "./api";
import { TokenStore } from "./token-store";
import type { AuthResponse, User } from "./types";

const user: User = { id: "u1", email: "op@x.dev", name: "Op", role: "operator", created_at: "2026-01-01T00:00:00Z" };

function memoryStore() {
  const map = new Map<string, string>();
  const storage = {
    getItem: (k: string) => map.get(k) ?? null,
    setItem: (k: string, v: string) => void map.set(k, v),
    removeItem: (k: string) => void map.delete(k),
  } as unknown as Storage;
  const s = new TokenStore(() => storage);
  s.set({ access_token: "old-access", refresh_token: "old-refresh", user });
  return s;
}

const json = (status: number, body: unknown) =>
  new Response(body === undefined ? null : JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const authRes = (n: string): AuthResponse => ({
  access_token: `access-${n}`,
  refresh_token: `refresh-${n}`,
  token_type: "Bearer",
  expires_in: 900,
  user,
});

describe("api client", () => {
  it("sends the bearer token", async () => {
    const store = memoryStore();
    const fetchImpl = vi.fn(async () => json(200, []));
    const api = createApiClient({ baseUrl: "http://api", store, fetchImpl });
    await api.listCameras();
    const [url, init] = fetchImpl.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("http://api/api/v1/cameras");
    expect((init.headers as Record<string, string>).Authorization).toBe("Bearer old-access");
  });

  it("refreshes once on 401 and retries the request with the new token", async () => {
    const store = memoryStore();
    const fetchImpl = vi.fn(async (url: string | URL | Request, init?: RequestInit) => {
      const u = String(url);
      if (u.endsWith("/auth/refresh")) {
        expect(JSON.parse(String(init?.body))).toEqual({ refresh_token: "old-refresh" });
        return json(200, authRes("new"));
      }
      const auth = (init?.headers as Record<string, string>).Authorization;
      return auth === "Bearer access-new" ? json(200, [{ id: "c1" }]) : json(401, { error: { code: "unauthorized", message: "expired" } });
    });
    const api = createApiClient({ baseUrl: "http://api", store, fetchImpl });
    const cams = await api.listCameras();
    expect(cams).toEqual([{ id: "c1" }]);
    expect(fetchImpl).toHaveBeenCalledTimes(3);
    expect(store.accessToken()).toBe("access-new");
    expect(store.refreshToken()).toBe("refresh-new");
  });

  it("shares one refresh between concurrent 401s", async () => {
    const store = memoryStore();
    let refreshes = 0;
    const fetchImpl = vi.fn(async (url: string | URL | Request, init?: RequestInit) => {
      if (String(url).endsWith("/auth/refresh")) {
        refreshes++;
        return json(200, authRes("n"));
      }
      return (init?.headers as Record<string, string>).Authorization === "Bearer access-n" ? json(200, {}) : json(401, {});
    });
    const api = createApiClient({ baseUrl: "http://api", store, fetchImpl });
    await Promise.all([api.listCameras(), api.listModels(), api.summary()]);
    expect(refreshes).toBe(1);
  });

  it("clears the session and notifies when refresh fails", async () => {
    const store = memoryStore();
    const onAuthLost = vi.fn();
    const fetchImpl = vi.fn(async (url: string | URL | Request) =>
      String(url).endsWith("/auth/refresh") ? json(401, { error: { code: "unauthorized", message: "bad refresh" } }) : json(401, { error: { code: "unauthorized", message: "expired" } }),
    );
    const api = createApiClient({ baseUrl: "http://api", store, fetchImpl, onAuthLost });
    await expect(api.listCameras()).rejects.toMatchObject({ status: 401, code: "unauthorized" });
    expect(store.get()).toBeNull();
    expect(onAuthLost).toHaveBeenCalledTimes(1);
    expect(fetchImpl).toHaveBeenCalledTimes(2); // original + refresh, no retry
  });

  it("does not try to refresh on login failures", async () => {
    const store = memoryStore();
    const fetchImpl = vi.fn(async () => json(401, { error: { code: "unauthorized", message: "invalid credentials" } }));
    const api = createApiClient({ baseUrl: "http://api", store, fetchImpl });
    const err = await api.login("a@b.co", "x").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).message).toBe("invalid credentials");
    expect(fetchImpl).toHaveBeenCalledTimes(1);
  });

  it("parses the error envelope with details and handles 204", async () => {
    const store = memoryStore();
    const fetchImpl = vi
      .fn()
      .mockResolvedValueOnce(json(422, { error: { code: "validation_error", message: "bad", details: { name: "required" } } }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }));
    const api = createApiClient({ baseUrl: "http://api", store, fetchImpl });
    await expect(api.createCamera({ name: "", source_type: "file", source_uri: "" })).rejects.toMatchObject({
      code: "validation_error",
      details: { name: "required" },
    });
    await expect(api.deleteCamera("c1")).resolves.toBeUndefined();
  });
});

describe("query building", () => {
  it("drops empty params", () => {
    expect(buildQuery({ a: "1", b: "", c: undefined, d: null, e: 0 })).toBe("?a=1&e=0");
    expect(buildQuery({})).toBe("");
  });
  it("builds the incidents query string from filters", () => {
    const qs = incidentQuery({
      q: "  helmet ",
      camera_id: "c1",
      severity: "high",
      status: "",
      rule_type: "missing_ppe",
      from: "2026-11-26T00:00:00.000Z",
      page: 3,
    });
    const sp = new URLSearchParams(qs);
    expect(Object.fromEntries(sp)).toEqual({
      q: "helmet",
      camera_id: "c1",
      severity: "high",
      rule_type: "missing_ppe",
      from: "2026-11-26T00:00:00.000Z",
      page: "3",
      page_size: "20",
    });
  });
});
