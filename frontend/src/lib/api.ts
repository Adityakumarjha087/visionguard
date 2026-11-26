import { API_URL } from "./config";
import { TokenStore, tokenStore } from "./token-store";
import type {
  AnalyticsSummary,
  ApiErrorBody,
  AuthResponse,
  Bucket,
  Camera,
  CameraCreate,
  CameraPatch,
  CountPoint,
  Heatmap,
  Incident,
  IncidentFilters,
  IncidentStatus,
  LineCrossingPoint,
  Model,
  Page,
  Role,
  Rule,
  RuleInput,
  Sample,
  User,
  Zone,
  ZoneInput,
} from "./types";

export class ApiError extends Error {
  constructor(
    public readonly status: number,
    public readonly code: string,
    message: string,
    public readonly details?: Record<string, string>,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

type Query = Record<string, string | number | undefined | null>;

export function buildQuery(params: Query): string {
  const sp = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === "") continue;
    sp.set(k, String(v));
  }
  const s = sp.toString();
  return s ? `?${s}` : "";
}

/** Turn incident filters into the `/incidents` query string (empty filters are dropped). */
export function incidentQuery(f: IncidentFilters): string {
  return buildQuery({
    q: f.q?.trim(),
    camera_id: f.camera_id,
    severity: f.severity,
    status: f.status,
    rule_type: f.rule_type,
    from: f.from,
    to: f.to,
    page: f.page ?? 1,
    page_size: f.page_size ?? 20,
  });
}

export interface RequestOptions {
  auth?: boolean;
  signal?: AbortSignal;
}

export interface ApiClientOptions {
  baseUrl?: string;
  store?: TokenStore;
  fetchImpl?: typeof fetch;
  onAuthLost?: () => void;
}

async function parseError(res: Response): Promise<ApiError> {
  let body: ApiErrorBody | null = null;
  try {
    body = (await res.json()) as ApiErrorBody;
  } catch {
    /* non-JSON */
  }
  const e = body?.error;
  return new ApiError(res.status, e?.code ?? "internal", e?.message ?? (res.statusText || `HTTP ${res.status}`), e?.details);
}

export function createApiClient(opts: ApiClientOptions = {}) {
  const baseUrl = opts.baseUrl ?? API_URL;
  const store = opts.store ?? tokenStore;
  const doFetch: typeof fetch = (...args) => (opts.fetchImpl ?? globalThis.fetch)(...args);
  let refreshing: Promise<boolean> | null = null;
  let onAuthLost = opts.onAuthLost;

  async function refresh(): Promise<boolean> {
    const rt = store.refreshToken();
    if (!rt) return false;
    if (!refreshing) {
      refreshing = (async () => {
        try {
          const res = await doFetch(`${baseUrl}/api/v1/auth/refresh`, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ refresh_token: rt }),
          });
          if (!res.ok) return false;
          store.setFromResponse((await res.json()) as AuthResponse);
          return true;
        } catch {
          return false;
        } finally {
          // allow later refreshes once this one settles
          refreshing = null;
        }
      })();
    }
    return refreshing;
  }

  async function request<T>(method: string, path: string, body?: unknown, ro: RequestOptions = {}): Promise<T> {
    const auth = ro.auth ?? true;
    const send = () => {
      const headers: Record<string, string> = { Accept: "application/json" };
      if (body !== undefined) headers["Content-Type"] = "application/json";
      const token = auth ? store.accessToken() : null;
      if (token) headers.Authorization = `Bearer ${token}`;
      return doFetch(`${baseUrl}${path}`, {
        method,
        headers,
        body: body === undefined ? undefined : JSON.stringify(body),
        signal: ro.signal,
      });
    };

    let res = await send();
    if (res.status === 401 && auth) {
      if (await refresh()) res = await send();
      if (res.status === 401) {
        store.clear();
        onAuthLost?.();
      }
    }
    if (!res.ok) throw await parseError(res);
    if (res.status === 204) return undefined as T;
    const text = await res.text();
    return (text ? JSON.parse(text) : undefined) as T;
  }

  /** Multipart upload with progress (fetch has no upload progress, so XHR is used). */
  function upload<T>(path: string, form: FormData, onProgress?: (pct: number) => void, retried = false): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      const xhr = new XMLHttpRequest();
      xhr.open("POST", `${baseUrl}${path}`);
      const token = store.accessToken();
      if (token) xhr.setRequestHeader("Authorization", `Bearer ${token}`);
      xhr.upload.onprogress = (e) => {
        if (e.lengthComputable && onProgress) onProgress(Math.round((e.loaded / e.total) * 100));
      };
      xhr.onerror = () => reject(new ApiError(0, "network", "Network error during upload"));
      xhr.onload = async () => {
        if (xhr.status === 401 && !retried && (await refresh())) {
          upload<T>(path, form, onProgress, true).then(resolve, reject);
          return;
        }
        let parsed: unknown = null;
        try {
          parsed = xhr.responseText ? JSON.parse(xhr.responseText) : null;
        } catch {
          parsed = null;
        }
        if (xhr.status >= 200 && xhr.status < 300) resolve(parsed as T);
        else {
          const e = (parsed as ApiErrorBody | null)?.error;
          reject(new ApiError(xhr.status, e?.code ?? "internal", e?.message ?? `Upload failed (${xhr.status})`, e?.details));
        }
      };
      xhr.send(form);
    });
  }

  const get = <T>(p: string, ro?: RequestOptions) => request<T>("GET", p, undefined, ro);
  const post = <T>(p: string, b?: unknown, ro?: RequestOptions) => request<T>("POST", p, b, ro);
  const patch = <T>(p: string, b?: unknown) => request<T>("PATCH", p, b);
  const put = <T>(p: string, b?: unknown) => request<T>("PUT", p, b);
  const del = (p: string) => request<void>("DELETE", p);

  return {
    request,
    refresh,
    setOnAuthLost(fn: (() => void) | undefined) {
      onAuthLost = fn;
    },

    // auth
    login: (email: string, password: string) =>
      post<AuthResponse>("/api/v1/auth/login", { email, password }, { auth: false }),
    register: (email: string, password: string, name: string) =>
      post<AuthResponse>("/api/v1/auth/register", { email, password, name }, { auth: false }),
    logout: (refresh_token: string) => post<void>("/api/v1/auth/logout", { refresh_token }, { auth: false }),
    me: () => get<User>("/api/v1/auth/me"),

    // users
    listUsers: () => get<User[]>("/api/v1/users"),
    updateUser: (id: string, body: { role?: Role; name?: string }) => patch<User>(`/api/v1/users/${id}`, body),
    deleteUser: (id: string) => del(`/api/v1/users/${id}`),

    // cameras
    listCameras: () => get<Camera[]>("/api/v1/cameras"),
    getCamera: (id: string) => get<Camera>(`/api/v1/cameras/${id}`),
    createCamera: (body: CameraCreate) => post<Camera>("/api/v1/cameras", body),
    updateCamera: (id: string, body: CameraPatch) => patch<Camera>(`/api/v1/cameras/${id}`, body),
    deleteCamera: (id: string) => del(`/api/v1/cameras/${id}`),
    uploadCamera: (file: File, name: string, onProgress?: (pct: number) => void) => {
      const fd = new FormData();
      fd.append("file", file);
      fd.append("name", name);
      return upload<Camera>("/api/v1/cameras/upload", fd, onProgress);
    },
    listSamples: () => get<Sample[]>("/api/v1/samples"),

    // zones
    listZones: (cameraId: string) => get<Zone[]>(`/api/v1/cameras/${cameraId}/zones`),
    createZone: (cameraId: string, body: ZoneInput) => post<Zone>(`/api/v1/cameras/${cameraId}/zones`, body),
    updateZone: (id: string, body: Required<ZoneInput>) => put<Zone>(`/api/v1/zones/${id}`, body),
    deleteZone: (id: string) => del(`/api/v1/zones/${id}`),

    // rules
    listRules: (cameraId?: string) => get<Rule[]>(`/api/v1/rules${buildQuery({ camera_id: cameraId })}`),
    createRule: (cameraId: string, body: RuleInput) => post<Rule>(`/api/v1/cameras/${cameraId}/rules`, body),
    updateRule: (id: string, body: RuleInput) => put<Rule>(`/api/v1/rules/${id}`, body),
    deleteRule: (id: string) => del(`/api/v1/rules/${id}`),

    // incidents
    listIncidents: (f: IncidentFilters, signal?: AbortSignal) =>
      get<Page<Incident>>(`/api/v1/incidents${incidentQuery(f)}`, { signal }),
    getIncident: (id: string) => get<Incident>(`/api/v1/incidents/${id}`),
    setIncidentStatus: (id: string, status: IncidentStatus) => patch<Incident>(`/api/v1/incidents/${id}`, { status }),

    // analytics
    summary: (cameraId?: string) => get<AnalyticsSummary>(`/api/v1/analytics/summary${buildQuery({ camera_id: cameraId })}`),
    counts: (p: { camera_id?: string; from?: string; to?: string; bucket?: Bucket }) =>
      get<CountPoint[]>(`/api/v1/analytics/counts${buildQuery(p)}`),
    heatmap: (p: { camera_id: string; from?: string; to?: string; grid?: number }) =>
      get<Heatmap>(`/api/v1/analytics/heatmap${buildQuery(p)}`),
    lineCrossings: (p: { camera_id?: string; from?: string; to?: string; bucket?: Bucket }) =>
      get<LineCrossingPoint[]>(`/api/v1/analytics/line-crossings${buildQuery(p)}`),

    // models
    listModels: () => get<Model[]>("/api/v1/models"),
    activateModel: (id: string) => post<Model>(`/api/v1/models/${id}/activate`),
  };
}

export type ApiClient = ReturnType<typeof createApiClient>;

export const api = createApiClient();

export function errorMessage(e: unknown): string {
  if (e instanceof ApiError) return e.message;
  if (e instanceof Error) return e.message;
  return "Something went wrong";
}
