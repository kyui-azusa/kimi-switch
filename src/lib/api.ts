const API_BASE = import.meta.env.VITE_API_BASE || "http://127.0.0.1:15722";
const LOCAL_KEY = "local-kimi-switch";

export interface RotationConfig {
  enabled: boolean;
  mode: "sticky";
  max_attempts: number;
  cooldown_seconds: number;
  retry_rate_limited: boolean;
}

export interface QuotaRefreshRecord {
  anchor_at?: string;
  source: string;
  recorded_at?: string;
  authoritative: boolean;
  next_refresh_at?: string;
}

export interface KeyQuotaRefresh {
  five_hour?: QuotaRefreshRecord;
  weekly?: QuotaRefreshRecord;
}

export interface KeyConfig {
  name: string;
  account: string;
  notes: string;
  enabled: boolean;
  created_at: number;
  source: "keychain" | "keychain+local" | "environment" | "inline";
  api_key_env: string;
  api_key_set: boolean;
  quota_refresh?: KeyQuotaRefresh;
}

export interface KeyStats {
  requests: number;
  successes: number;
  failures: number;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  cache_read_tokens: number;
  reasoning_tokens: number;
  estimated_tokens: number;
  reserved_tokens: number;
  in_flight: number;
  cooldown_until?: string;
  last_used_at?: string;
  last_error?: string;
  last_check_at?: string;
  last_check_status?: "ok" | "quota_exhausted" | "rate_limited" | "upstream_overloaded" | "invalid_key" | "account_disabled" | "forbidden" | "network_error" | "upstream_error" | "failed";
  last_check_ms?: number;
}

export interface StatusResponse {
  config: {
    listen_addr: string;
    upstream_base_url: string;
    active_key: string;
    rotation: RotationConfig;
    keys: KeyConfig[];
  };
  keys: Record<string, KeyStats>;
  kimi_takeover: TakeoverStatus;
}

export interface TakeoverStatus {
  enabled: boolean;
  config_path: string;
  original_provider?: string;
  affected_models: number;
  reload_required: boolean;
  config_error?: string;
  error?: string;
}

export interface DailyUsage {
  requests: number;
  successes: number;
  failures: number;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  cache_read_tokens: number;
  reasoning_tokens: number;
  estimated_tokens: number;
}

export interface UsageHistoryResponse {
  daily: Record<string, Record<string, DailyUsage>>;
}

export interface KeyInput {
  name: string;
  account: string;
  notes: string;
  api_key: string;
  api_key_env: string;
  enabled: boolean;
  quota_refresh?: {
    five_hour?: { anchor_at: string };
    weekly?: { anchor_at: string };
  };
}

export interface ConnectivityResult {
  name: string;
  ok: boolean;
  status: KeyStats["last_check_status"];
  status_code?: number;
  latency_ms: number;
  message: string;
  tested_at: string;
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${API_BASE}${path}`, {
    ...init,
    headers: {
      Authorization: `Bearer ${LOCAL_KEY}`,
      "Content-Type": "application/json",
      ...init?.headers,
    },
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(data.error || `请求失败 (${response.status})`);
  return data as T;
}

export const api = {
  status: () => request<StatusResponse>("/api/status"),
  usage: () => request<UsageHistoryResponse>("/api/usage"),
  setTakeover: (enabled: boolean) => request<TakeoverStatus>("/api/kimi/takeover", { method: "PUT", body: JSON.stringify({ enabled }) }),
  importKimi: (path = "") => request<{ imported: string[]; path: string }>("/api/import/kimi", { method: "POST", body: JSON.stringify({ path }) }),
  createKey: (input: KeyInput) => request("/api/keys", { method: "POST", body: JSON.stringify(input) }),
  updateKey: (name: string, input: KeyInput) => request(`/api/keys/${encodeURIComponent(name)}`, { method: "PUT", body: JSON.stringify(input) }),
  deleteKey: (name: string) => request(`/api/keys/${encodeURIComponent(name)}`, { method: "DELETE" }),
  duplicateKey: (name: string) => request(`/api/keys/${encodeURIComponent(name)}/duplicate`, { method: "POST" }),
  reorderKeys: (names: string[]) => request("/api/keys/reorder", { method: "POST", body: JSON.stringify({ names }) }),
  testKey: (name: string) => request<ConnectivityResult>(`/api/keys/${encodeURIComponent(name)}/test`, { method: "POST" }),
  useKey: (name: string) => request(`/api/keys/${encodeURIComponent(name)}/use`, { method: "POST" }),
  toggleKey: (name: string, enabled: boolean) => request(`/api/keys/${encodeURIComponent(name)}/toggle`, { method: "POST", body: JSON.stringify({ enabled }) }),
  setRotation: (enabled: boolean, mode: RotationConfig["mode"]) => request("/api/rotation", { method: "POST", body: JSON.stringify({ enabled, mode }) }),
  updateSettings: (upstream_base_url: string, rotation: RotationConfig) => request("/api/settings", { method: "PUT", body: JSON.stringify({ upstream_base_url, rotation }) }),
};
