import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { getVersion } from "@tauri-apps/api/app";
import { relaunch } from "@tauri-apps/plugin-process";
import { check, type Update } from "@tauri-apps/plugin-updater";
import packageMetadata from "../package.json";
import {
  Activity,
  ArrowLeft,
  Check,
  Clock3,
  Copy,
  Download,
  Edit3,
  GripVertical,
  Info,
  KeyRound,
  List,
  Moon,
  Play,
  Plus,
  RadioTower,
  RefreshCw,
  Search,
  Settings,
  ShieldCheck,
  ScrollText,
  Sun,
  Trash2,
  Users,
  X,
} from "lucide-react";
import { api, type ConnectivityResult, type DailyUsage, type KeyConfig, type KeyInput, type KeyStats, type QuotaRefreshRecord, type RotationConfig, type StatusResponse } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { changelog, displayVersionForRelease } from "@/lib/changelog";
import { cn } from "@/lib/utils";

type Panel =
  | { kind: "form"; key?: KeyConfig }
  | { kind: "settings" }
  | { kind: "updates"; update?: Update }
  | { kind: "changelog" }
  | { kind: "import" }
  | { kind: "usage" }
  | null;

type AccountAction = "use" | "test" | "edit" | "duplicate" | "toggle" | "delete";
type DropEdge = "before" | "after";

const emptyStats: KeyStats = {
  requests: 0,
  successes: 0,
  failures: 0,
  input_tokens: 0,
  output_tokens: 0,
  total_tokens: 0,
  cache_read_tokens: 0,
  reasoning_tokens: 0,
  estimated_tokens: 0,
  reserved_tokens: 0,
  in_flight: 0,
};

function formatNumber(value = 0) {
  return new Intl.NumberFormat("zh-CN", { notation: value >= 100000 ? "compact" : "standard", maximumFractionDigits: 1 }).format(value);
}

function toLocalDateTimeInput(iso?: string) {
  if (!iso) return "";
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "";
  const offset = date.getTimezoneOffset();
  return new Date(date.getTime() - offset * 60000).toISOString().slice(0, 16);
}

function toIsoOrEmpty(localValue: string) {
  if (!localValue) return "";
  const date = new Date(localValue);
  return Number.isNaN(date.getTime()) ? "" : date.toISOString();
}

type QuotaRefreshFormKey = "five_hour" | "weekly";

type QuotaRefreshFormState = Record<QuotaRefreshFormKey, { anchor_at: string }>;

type KeyFormState = Omit<KeyInput, "quota_refresh"> & { quota_refresh: QuotaRefreshFormState };

function buildQuotaRefreshPayload(current: KeyConfig | undefined, form: QuotaRefreshFormState, initial: QuotaRefreshFormState): KeyInput["quota_refresh"] {
  const payload: NonNullable<KeyInput["quota_refresh"]> = {};
  for (const key of ["five_hour", "weekly"] as const) {
    if (current) {
      if (form[key].anchor_at === initial[key].anchor_at) continue;
      payload[key] = { anchor_at: toIsoOrEmpty(form[key].anchor_at) };
      continue;
    }
    if (!form[key].anchor_at) continue;
    payload[key] = { anchor_at: toIsoOrEmpty(form[key].anchor_at) };
  }
  return Object.keys(payload).length ? payload : undefined;
}

function formatDateTime(value?: string) {
  if (!value) return "未记录";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "未记录";
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

function quotaReferenceLabel(record?: QuotaRefreshRecord) {
  if (!record) return "未记录";
  return record.authoritative ? "官方标准口径" : "手动参考 · 非官方";
}

function sourceLabel(key: KeyConfig) {
  if (key.source === "keychain+local") return "系统凭据库 + 本地降级副本";
  if (key.source === "keychain") return "系统凭据库";
  if (key.source === "environment") return key.api_key_env || "环境变量";
  return "本地配置";
}

function statusOf(key: KeyConfig, stats: KeyStats) {
  if (!key.enabled) return { label: "未参与轮换", color: "bg-gray-400", text: "text-gray-500" };
  const cooldownMinutes = stats.cooldown_until ? Math.max(1, Math.ceil((new Date(stats.cooldown_until).getTime() - Date.now()) / 60000)) : 0;
  if (stats.last_check_status === "quota_exhausted") return { label: "额度用尽", color: "bg-red-500", text: "text-red-600" };
  if (stats.last_check_status === "invalid_key") return { label: "Key 无效", color: "bg-red-500", text: "text-red-600" };
  if (stats.last_check_status === "account_disabled") return { label: "账号异常", color: "bg-red-500", text: "text-red-600" };
  if (stats.last_check_status === "forbidden") return { label: "权限不足", color: "bg-red-500", text: "text-red-600" };
  if (stats.last_check_status === "upstream_overloaded") return { label: "上游繁忙 · 稍后重试", color: "bg-amber-500", text: "text-amber-600" };
  if (stats.last_check_status === "rate_limited") return { label: `限流冷却 · ${cooldownMinutes || 1} 分钟`, color: "bg-amber-500", text: "text-amber-600" };
  if (stats.last_check_status === "network_error") return { label: `网络失败 · ${cooldownMinutes || 1} 分钟`, color: "bg-amber-500", text: "text-amber-600" };
  if (stats.last_check_status === "upstream_error") return { label: `上游异常 · ${cooldownMinutes || 1} 分钟`, color: "bg-amber-500", text: "text-amber-600" };
  if (stats.cooldown_until && new Date(stats.cooldown_until).getTime() > Date.now()) {
    return { label: `冷却中 · ${cooldownMinutes} 分钟`, color: "bg-amber-500", text: "text-amber-600" };
  }
  if (stats.last_error) return { label: "最近失败", color: "bg-red-500", text: "text-red-500" };
  if (stats.last_check_status === "ok") return { label: `连通 · ${stats.last_check_ms || 0} ms`, color: "bg-emerald-500", text: "text-emerald-600" };
  return { label: "待检测", color: "bg-gray-400", text: "text-gray-500" };
}

function reorderAccounts(keys: KeyConfig[], draggedName: string, targetName: string, edge: DropEdge) {
  if (draggedName === targetName) return keys;
  const dragged = keys.find((key) => key.name === draggedName);
  if (!dragged) return keys;
  const reordered = keys.filter((key) => key.name !== draggedName);
  const targetIndex = reordered.findIndex((key) => key.name === targetName);
  if (targetIndex < 0) return keys;
  reordered.splice(targetIndex + (edge === "after" ? 1 : 0), 0, dragged);
  return reordered;
}

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <label className="grid gap-2">
      <span className="text-sm font-medium">{label}</span>
      {children}
      {hint && <span className="text-xs leading-5 text-muted-foreground">{hint}</span>}
    </label>
  );
}

const fieldClass = "h-10 w-full rounded-lg border border-border bg-background px-3 text-sm shadow-sm transition focus:border-blue-500 focus:outline-none focus:ring-2 focus:ring-blue-500/15 disabled:opacity-60";

function FullScreenPanel({ title, subtitle, onBack, children, footer }: {
  title: string;
  subtitle?: string;
  onBack: () => void;
  children: React.ReactNode;
  footer?: React.ReactNode;
}) {
  return (
    <div className="fixed inset-0 z-50 flex flex-col bg-background text-foreground">
      <header className="flex h-16 flex-shrink-0 items-center gap-3 border-b border-border px-6" data-tauri-drag-region>
        <Button className="no-drag" variant="outline" size="icon" onClick={onBack} title="返回">
          <ArrowLeft className="h-4 w-4" />
        </Button>
        <div>
          <h1 className="text-lg font-semibold leading-tight">{title}</h1>
          {subtitle && <p className="mt-0.5 text-xs text-muted-foreground">{subtitle}</p>}
        </div>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto px-6 py-8">
        <div className="mx-auto max-w-3xl">{children}</div>
      </div>
      {footer && <footer className="flex flex-shrink-0 justify-end gap-2 border-t border-border bg-muted/20 px-6 py-4">{footer}</footer>}
    </div>
  );
}

type UpdateStatus = "idle" | "checking" | "available" | "downloading" | "ready" | "up-to-date" | "error";

function formatUpdateDate(value?: string) {
  if (!value) return "";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "" : date.toLocaleString("zh-CN", { dateStyle: "medium", timeStyle: "short" });
}

function UpdatePanel({ initialUpdate, onBack, notify }: {
  initialUpdate?: Update;
  onBack: () => void;
  notify: (message: string, error?: boolean) => void;
}) {
  const [status, setStatus] = useState<UpdateStatus>(initialUpdate ? "available" : "idle");
  const [update, setUpdate] = useState<Update | null>(initialUpdate || null);
  const [progress, setProgress] = useState(0);
  const [message, setMessage] = useState("");

  const checkForUpdate = useCallback(async () => {
    setStatus("checking");
    setMessage("");
    try {
      const next = await check();
      setUpdate(next);
      setStatus(next ? "available" : "up-to-date");
      if (!next) setMessage("当前已是最新版本");
    } catch (error) {
      setStatus("error");
      setMessage(error instanceof Error ? error.message : "无法检查更新");
    }
  }, []);

  useEffect(() => {
    if (!initialUpdate) void checkForUpdate();
  }, [checkForUpdate, initialUpdate]);

  const install = async () => {
    if (!update) return;
    setStatus("downloading");
    setProgress(0);
    try {
      let downloaded = 0;
      let total = 0;
      await update.downloadAndInstall((event) => {
        if (event.event === "Started") {
          total = event.data.contentLength || 0;
          setMessage(total ? `正在下载 ${(total / 1024 / 1024).toFixed(1)} MB` : "正在下载更新");
        } else if (event.event === "Progress") {
          downloaded += event.data.chunkLength;
          setProgress(total ? Math.min(100, Math.round((downloaded / total) * 100)) : 0);
        } else if (event.event === "Finished") {
          setProgress(100);
          setMessage("下载完成，准备重启应用");
        }
      });
      setStatus("ready");
      notify("更新已下载，应用将重启");
      await relaunch();
    } catch (error) {
      setStatus("error");
      setMessage(error instanceof Error ? error.message : "更新失败，可重试");
    }
  };

  return (
    <FullScreenPanel title="软件更新" subtitle="通过签名校验后下载并安装新版本" onBack={onBack}>
      <section className="rounded-xl border border-border bg-card p-6 shadow-sm">
        <div className="flex items-start justify-between gap-4">
          <div><h2 className="font-semibold">KimiSwitch 更新</h2><p className="mt-1 text-sm text-muted-foreground">更新不会修改账号、系统凭据或本地代理数据。</p></div>
          <RefreshCw className={cn("h-5 w-5 text-blue-500", status === "checking" && "animate-spin")} />
        </div>
        {update ? (
          <div className="mt-6 rounded-lg border border-blue-500/20 bg-blue-500/5 p-4">
            <div className="flex items-center justify-between gap-3"><span className="text-sm font-medium">发现新版本 {update.version}</span>{update.date && <span className="text-xs text-muted-foreground">{formatUpdateDate(update.date)}</span>}</div>
            {update.body && <p className="mt-3 whitespace-pre-wrap text-sm leading-6 text-muted-foreground">{update.body}</p>}
            {status === "downloading" && <div className="mt-4"><div className="h-2 overflow-hidden rounded-full bg-muted"><div className="h-full bg-blue-500 transition-all" style={{ width: `${progress}%` }} /></div><p className="mt-2 text-xs text-muted-foreground">{progress ? `${progress}%` : message}</p></div>}
            {status === "ready" && <p className="mt-3 text-sm text-emerald-600">更新已安装，正在重启。</p>}
            {status === "error" && <p className="mt-3 text-sm text-red-600">{message}</p>}
            {status !== "downloading" && status !== "ready" && <Button className="mt-5" onClick={() => void install()}><Download className="h-4 w-4" />下载并安装</Button>}
          </div>
        ) : (
          <div className="mt-6 rounded-lg border border-border bg-muted/30 p-4 text-sm text-muted-foreground">{message || (status === "checking" ? "正在检查更新…" : "尚未检查更新")}</div>
        )}
        {status === "error" && <Button variant="outline" className="mt-4" onClick={() => void checkForUpdate()}><RefreshCw className="h-4 w-4" />重试</Button>}
        {status !== "checking" && status !== "downloading" && status !== "ready" && status !== "error" && <Button variant="outline" className="mt-4" onClick={() => void checkForUpdate()}><RefreshCw className="h-4 w-4" />检查更新</Button>}
      </section>
    </FullScreenPanel>
  );
}

function ChangelogPanel({ appVersion, onBack }: { appVersion: string; onBack: () => void }) {
  const currentVersion = appVersion.replace(/^v/, "");
  const currentBuildVersion = displayVersionForRelease(currentVersion);
  const currentIndex = Math.max(0, changelog.findIndex((entry) => (entry.releaseVersion || entry.version) === currentVersion));
  const [selectedIndex, setSelectedIndex] = useState(currentIndex);
  const [mode, setMode] = useState<"list" | "detail">("list");
  const selected = changelog[selectedIndex] || changelog[0];
  const selectEntry = (index: number) => {
    setSelectedIndex(index);
    setMode("detail");
  };
  return (
    <FullScreenPanel title="更新日志" subtitle={`当前构建 v${currentBuildVersion} · 发布版本 v${currentVersion}`} onBack={onBack}>
      <div className="mb-4 flex items-center justify-between gap-3">
        <span className="text-xs text-muted-foreground">{mode === "list" ? `${changelog.length} 个版本` : `v${selected.version}`}</span>
        <div className="flex rounded-lg border border-border p-1" role="tablist" aria-label="更新日志视图">
          <button type="button" role="tab" aria-selected={mode === "list"} onClick={() => setMode("list")} className={cn("flex h-8 items-center gap-1.5 rounded-md px-2.5 text-xs", mode === "list" ? "bg-blue-500 text-white" : "text-muted-foreground hover:bg-muted")}><List className="h-3.5 w-3.5" />列表</button>
          <button type="button" role="tab" aria-selected={mode === "detail"} onClick={() => setMode("detail")} className={cn("flex h-8 items-center gap-1.5 rounded-md px-2.5 text-xs", mode === "detail" ? "bg-blue-500 text-white" : "text-muted-foreground hover:bg-muted")}>详情</button>
        </div>
      </div>
      {mode === "list" ? (
        <div className="overflow-hidden rounded-xl border border-border bg-card shadow-sm" role="list">
          {changelog.map((entry, index) => {
            const current = (entry.releaseVersion || entry.version) === currentVersion;
            return <div key={entry.version} role="listitem" className="border-b border-border last:border-b-0"><button type="button" aria-current={current ? "true" : undefined} onClick={() => selectEntry(index)} className={cn("flex w-full items-center gap-3 px-4 py-4 text-left transition hover:bg-muted/50 sm:px-5", current && "bg-blue-500/5")}>
              <span className={cn("h-2 w-2 flex-shrink-0 rounded-full", current ? "bg-blue-500" : "bg-border")} />
              <span className={cn("w-28 flex-shrink-0 text-sm font-semibold tabular-nums", current && "text-blue-600 dark:text-blue-400")}>v{entry.version}</span>
              <span className="min-w-0 flex-1 truncate text-sm font-medium">{entry.title}</span>
              <span className="flex-shrink-0 text-xs tabular-nums text-muted-foreground">{entry.date.replaceAll("-", ".")}</span>
              <span className="hidden flex-shrink-0 text-xs text-muted-foreground sm:inline">{entry.changes.length} 项</span>
            </button></div>;
          })}
        </div>
      ) : (
        <article className="overflow-hidden rounded-xl border border-border bg-card shadow-sm">
          <div className="border-b border-border bg-blue-500/5 px-5 py-5 sm:px-7">
            <button type="button" onClick={() => setMode("list")} className="mb-4 text-xs font-medium text-muted-foreground hover:text-foreground">返回列表</button>
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1"><h2 className="text-lg font-semibold">{selected.title}</h2>{(selected.releaseVersion || selected.version) === currentVersion && <span className="rounded-md bg-blue-500/10 px-1.5 py-0.5 text-[10px] font-semibold text-blue-600 dark:text-blue-400">当前版本</span>}</div>
            <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs tabular-nums text-muted-foreground"><time dateTime={selected.date}>{selected.date.replaceAll("-", ".")}</time><span>v{selected.version}</span>{selected.releaseVersion && selected.releaseVersion !== selected.version && <span>发布版本 v{selected.releaseVersion}</span>}</div>
          </div>
          <ul className="grid gap-3 px-5 py-6 text-sm leading-6 text-muted-foreground sm:px-7">{selected.changes.map((change) => <li key={change} className="list-disc pl-1 marker:text-muted-foreground/60">{change}</li>)}</ul>
        </article>
      )}
    </FullScreenPanel>
  );
}

function AccountForm({ current, onBack, onSaved, notify }: {
  current?: KeyConfig;
  onBack: () => void;
  onSaved: () => Promise<void>;
  notify: (message: string, error?: boolean) => void;
}) {
  const initialQuotaRefresh = useRef<QuotaRefreshFormState>({
    five_hour: { anchor_at: toLocalDateTimeInput(current?.quota_refresh?.five_hour?.anchor_at) },
    weekly: { anchor_at: toLocalDateTimeInput(current?.quota_refresh?.weekly?.anchor_at) },
  });
  const [form, setForm] = useState<KeyFormState>({
    name: current?.name || "",
    account: current?.account || "",
    notes: current?.notes || "",
    api_key: "",
    api_key_env: current?.api_key_env || "",
    enabled: current?.enabled ?? true,
    quota_refresh: initialQuotaRefresh.current,
  });
  const [saving, setSaving] = useState(false);
  const update = <K extends keyof KeyFormState>(key: K, value: KeyFormState[K]) => setForm((old) => ({ ...old, [key]: value }));
  const updateQuotaRefresh = (key: "five_hour" | "weekly", value: string) =>
    setForm((old) => ({ ...old, quota_refresh: { ...old.quota_refresh, [key]: { anchor_at: value } } }));

  const save = async () => {
    if (!form.name.trim()) return notify("请填写账号名称", true);
    if (!current && !form.api_key.trim() && !form.api_key_env.trim()) return notify("请填写 API Key 或环境变量", true);
    setSaving(true);
    try {
      const payload: KeyInput = {
        ...form,
        quota_refresh: buildQuotaRefreshPayload(current, form.quota_refresh, initialQuotaRefresh.current),
      };
      if (current) await api.updateKey(current.name, payload);
      else await api.createKey(payload);
      notify(current ? "账号已更新" : "账号已创建");
      await onSaved();
      onBack();
    } catch (error) {
      notify(error instanceof Error ? error.message : "保存失败", true);
    } finally {
      setSaving(false);
    }
  };

  return (
    <FullScreenPanel
      title={current ? "编辑账号" : "添加账号"}
      subtitle="Kimi Code API Key 会保存到系统凭据库"
      onBack={onBack}
      footer={<><Button variant="outline" onClick={onBack}>取消</Button><Button onClick={save} disabled={saving}>{saving ? "保存中…" : current ? "保存修改" : "添加账号"}</Button></>}
    >
      <section className="rounded-xl border border-border bg-card p-6 shadow-sm">
        <div className="mb-6 flex items-center gap-3">
          <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-blue-500/10 text-blue-500"><KeyRound className="h-5 w-5" /></div>
          <div><h2 className="font-semibold">基本信息</h2><p className="text-sm text-muted-foreground">用于区分公司、个人或不同套餐账号</p></div>
        </div>
        <div className="grid gap-5 sm:grid-cols-2">
          <Field label="名称"><input className={fieldClass} value={form.name} onChange={(e) => update("name", e.target.value)} placeholder="例如：公司主账号" autoFocus /></Field>
          <Field label="账号标识"><input className={fieldClass} value={form.account} onChange={(e) => update("account", e.target.value)} placeholder="邮箱、部门或用途（可选）" /></Field>
          <div className="sm:col-span-2"><Field label="备注"><textarea className={cn(fieldClass, "h-24 resize-none py-2.5")} value={form.notes} onChange={(e) => update("notes", e.target.value)} placeholder="套餐、归属或使用说明" /></Field></div>
        </div>
      </section>

      <section className="mt-5 rounded-xl border border-border bg-card p-6 shadow-sm">
        <div className="mb-6 flex items-center gap-3">
          <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-emerald-500/10 text-emerald-600"><ShieldCheck className="h-5 w-5" /></div>
          <div><h2 className="font-semibold">认证信息</h2><p className="text-sm text-muted-foreground">API Key 和环境变量二选一</p></div>
        </div>
        <div className="grid gap-5">
          <Field label="Kimi Code API Key" hint={current ? "编辑时留空表示保留原 Key。新 Key 只写入系统凭据库，不写入配置文件。" : "保存后写入 macOS Keychain 或 Windows Credential Manager，不会明文出现在 KimiSwitch 配置中。"}>
            <input className={fieldClass} type="password" value={form.api_key} onChange={(e) => update("api_key", e.target.value)} placeholder={current ? "留空以保留原 Key" : "粘贴 API Key"} autoComplete="new-password" />
          </Field>
          <div className="flex items-center gap-3 text-xs text-muted-foreground"><span className="h-px flex-1 bg-border" />或者<span className="h-px flex-1 bg-border" /></div>
          <Field label="环境变量名" hint="适合由公司终端配置或密码管理器注入，例如 KIMI_WORK_KEY。">
            <input className={fieldClass} value={form.api_key_env} onChange={(e) => update("api_key_env", e.target.value)} placeholder="KIMI_WORK_KEY" />
          </Field>
          <div className="flex items-center justify-between rounded-lg border border-border bg-muted/30 px-4 py-3">
            <div><div className="text-sm font-medium">参与代理与轮换</div><div className="mt-0.5 text-xs text-muted-foreground">关闭后保留账号，但不会被自动选择</div></div>
            <Switch checked={form.enabled} onCheckedChange={(value) => update("enabled", value)} />
          </div>
        </div>
      </section>

      <section className="mt-5 rounded-xl border border-border bg-card p-6 shadow-sm">
        <div className="mb-6 flex items-center gap-3">
          <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-amber-500/10 text-amber-600"><Clock3 className="h-5 w-5" /></div>
          <div><h2 className="font-semibold">额度刷新参考时间</h2><p className="text-sm text-muted-foreground">手工记录，用于安排后续真实测试。</p></div>
        </div>
        <div className="grid gap-5 sm:grid-cols-2">
          <Field label="5 小时额度刷新参考时间" hint="留空可清除。">
            <input className={fieldClass} type="datetime-local" value={form.quota_refresh.five_hour.anchor_at || ""} onChange={(e) => updateQuotaRefresh("five_hour", e.target.value)} />
          </Field>
          <Field label="周额度刷新参考时间" hint="留空可清除。">
            <input className={fieldClass} type="datetime-local" value={form.quota_refresh.weekly.anchor_at || ""} onChange={(e) => updateQuotaRefresh("weekly", e.target.value)} />
          </Field>
        </div>
        <div className="mt-5 rounded-lg border border-amber-500/20 bg-amber-500/5 px-4 py-3 text-sm leading-6 text-amber-700 dark:text-amber-300">
          <Info className="mr-2 inline h-4 w-4 align-[-2px]" />
          仅作参考，不代表 Kimi 官方额度或刷新承诺，也不参与自动轮换与健康判断。
        </div>
      </section>
    </FullScreenPanel>
  );
}

function SettingsPanel({ status, appVersion, onBack, onSaved, notify, dark, setDark, onUpdates, onChangelog }: {
  status: StatusResponse;
  appVersion: string;
  onBack: () => void;
  onSaved: () => Promise<void>;
  notify: (message: string, error?: boolean) => void;
  dark: boolean;
  setDark: (value: boolean) => void;
  onUpdates: () => void;
  onChangelog: () => void;
}) {
  const buildVersion = displayVersionForRelease(appVersion);
  const [upstream, setUpstream] = useState(status.config.upstream_base_url);
  const [rotation, setRotation] = useState<RotationConfig>({ ...status.config.rotation });
  const [saving, setSaving] = useState(false);
  const save = async () => {
    setSaving(true);
    try {
      await api.updateSettings(upstream, rotation);
      await onSaved();
      notify("设置已保存");
      onBack();
    } catch (error) {
      notify(error instanceof Error ? error.message : "保存失败", true);
    } finally { setSaving(false); }
  };
  return (
    <FullScreenPanel title="设置" subtitle="代理、轮换和外观" onBack={onBack} footer={<><Button variant="outline" onClick={onBack}>取消</Button><Button onClick={save} disabled={saving}>{saving ? "保存中…" : "保存设置"}</Button></>}>
      <div className="grid gap-5">
        <section className="rounded-xl border border-border bg-card p-6 shadow-sm">
          <h2 className="font-semibold">本地代理</h2><p className="mt-1 text-sm text-muted-foreground">只监听 127.0.0.1，不向局域网暴露。</p>
          <div className="mt-5 grid gap-5 sm:grid-cols-2">
            <Field label="本地地址"><input className={fieldClass} value={`http://${status.config.listen_addr}/v1`} disabled /></Field>
            <Field label="Kimi 上游地址"><input className={fieldClass} value={upstream} onChange={(e) => setUpstream(e.target.value)} /></Field>
          </div>
        </section>
        <section className="rounded-xl border border-border bg-card p-6 shadow-sm">
          <div className="flex items-start justify-between gap-4"><div><h2 className="font-semibold">自动轮换</h2><p className="mt-1 text-sm text-muted-foreground">默认持续使用当前账号；只有额度耗尽、鉴权失败、限流或上游不可用时才切到下一个可用账号。</p></div><Switch checked={rotation.enabled} onCheckedChange={(enabled) => setRotation((old) => ({ ...old, enabled }))} /></div>
          <div className="mt-5 grid gap-5 sm:grid-cols-2">
            <Field label="切换策略" hint="当前账号健康时不会主动换 Key；切走后会保持在新的账号上。">
              <input className={fieldClass} value="故障时切换" disabled />
            </Field>
            <Field label="单次最大尝试账号数"><input className={fieldClass} type="number" min={1} max={20} value={rotation.max_attempts} onChange={(e) => setRotation((old) => ({ ...old, max_attempts: Number(e.target.value) }))} /></Field>
            <Field label="失败冷却（秒）" hint="至少 600 秒；失败账号在仍有其他可用账号时不会被选择。"><input className={fieldClass} type="number" min={600} value={rotation.cooldown_seconds} onChange={(e) => setRotation((old) => ({ ...old, cooldown_seconds: Number(e.target.value) }))} /></Field>
            <div className="flex items-center justify-between rounded-lg border border-border bg-muted/30 px-4 py-3 self-end"><div><div className="text-sm font-medium">429 时尝试下一个账号</div><div className="text-xs text-muted-foreground">仅在响应流开始前切换</div></div><Switch checked={rotation.retry_rate_limited} onCheckedChange={(retry_rate_limited) => setRotation((old) => ({ ...old, retry_rate_limited }))} /></div>
          </div>
        </section>
        <section className="rounded-xl border border-border bg-card p-6 shadow-sm">
          <div className="flex items-center justify-between"><div><h2 className="font-semibold">深色模式</h2><p className="mt-1 text-sm text-muted-foreground">只影响 KimiSwitch 桌面界面。</p></div><Button variant="outline" onClick={() => setDark(!dark)}>{dark ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}{dark ? "切换浅色" : "切换深色"}</Button></div>
        </section>
        <section className="rounded-xl border border-border bg-card p-6 shadow-sm">
          <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between"><div><h2 className="font-semibold">版本与更新</h2><p className="mt-1 text-sm text-muted-foreground">当前构建 v{buildVersion}；发布版本 v{appVersion}。查看版本变化或检查签名更新包。</p></div><div className="flex flex-wrap gap-2"><Button variant="outline" onClick={onChangelog}><ScrollText className="h-4 w-4" />更新日志</Button><Button variant="outline" onClick={onUpdates}><Download className="h-4 w-4" />检查更新</Button></div></div>
        </section>
        <section className="rounded-xl border border-border bg-muted/30 p-5 text-sm text-muted-foreground">
          <p>KimiSwitch 的桌面界面排布和部分 UI 组件基于 CC Switch（MIT License）调整；代理与 Kimi 配置逻辑为 KimiSwitch 实现。</p>
          <p className="mt-3 text-xs font-semibold uppercase tracking-[0.18em] text-foreground/70">Powered by kyuiazusa</p>
        </section>
      </div>
    </FullScreenPanel>
  );
}

function ImportPanel({ onBack, onImported, notify }: { onBack: () => void; onImported: () => Promise<void>; notify: (message: string, error?: boolean) => void }) {
  const [path, setPath] = useState("~/.kimi-code/config.toml");
  const [busy, setBusy] = useState(false);
  const run = async () => {
    setBusy(true);
    try {
      const result = await api.importKimi(path === "~/.kimi-code/config.toml" ? "" : path);
      await onImported();
      notify(result.imported.length ? `已导入 ${result.imported.length} 个账号` : "配置中没有发现可导入的 API Key 账号");
      onBack();
    } catch (error) {
      notify(error instanceof Error ? error.message : "导入失败", true);
    } finally { setBusy(false); }
  };
  return (
    <FullScreenPanel title="导入 Kimi 配置" subtitle="读取现有 provider，原配置不会被修改" onBack={onBack} footer={<><Button variant="outline" onClick={onBack}>取消</Button><Button onClick={run} disabled={busy}><Download className="h-4 w-4" />{busy ? "正在导入…" : "开始导入"}</Button></>}>
      <section className="rounded-xl border border-border bg-card p-7 shadow-sm">
        <div className="flex items-start gap-4"><div className="flex h-12 w-12 flex-shrink-0 items-center justify-center rounded-xl bg-blue-500/10 text-blue-500"><Download className="h-6 w-6" /></div><div><h2 className="text-lg font-semibold">从 Kimi Code 导入</h2><p className="mt-1 max-w-xl text-sm leading-6 text-muted-foreground">识别 config.toml 中使用 API Key 的 providers，把 Key 安全地复制到系统凭据库。OAuth 登录项会跳过。</p></div></div>
        <div className="mt-7"><Field label="配置文件路径" hint="默认读取 ~/.kimi-code/config.toml；如公司有单独配置，可以填写绝对路径。"><input className={fieldClass} value={path} onChange={(e) => setPath(e.target.value)} /></Field></div>
        <div className="mt-6 rounded-lg border border-emerald-500/20 bg-emerald-500/5 p-4 text-sm leading-6 text-emerald-700 dark:text-emerald-300"><ShieldCheck className="mr-2 inline h-4 w-4" />只读 Kimi 配置，不会覆盖、删除或重写原文件。</div>
      </section>
    </FullScreenPanel>
  );
}

function UsagePanel({ status, onBack }: { status: StatusResponse; onBack: () => void }) {
  const [daily, setDaily] = useState<Record<string, Record<string, DailyUsage>>>({});
  const [days, setDays] = useState<7 | 30>(7);
  const rows = status.config.keys.map((item) => ({ item, stats: status.keys[item.name] || emptyStats }));
  const totals = rows.reduce(
    (sum, row) => ({
      requests: sum.requests + row.stats.requests,
      successes: sum.successes + row.stats.successes,
      failures: sum.failures + row.stats.failures,
      input: sum.input + row.stats.input_tokens,
      output: sum.output + row.stats.output_tokens,
      cacheRead: sum.cacheRead + row.stats.cache_read_tokens,
      reasoning: sum.reasoning + row.stats.reasoning_tokens,
      tokens: sum.tokens + row.stats.total_tokens,
      estimated: sum.estimated + row.stats.estimated_tokens,
    }),
    { requests: 0, successes: 0, failures: 0, input: 0, output: 0, cacheRead: 0, reasoning: 0, tokens: 0, estimated: 0 },
  );

  useEffect(() => {
    api.usage().then((result) => setDaily(result.daily || {})).catch(() => setDaily({}));
  }, []);

  const trend = useMemo(() => {
    const result: Array<{ date: string; input: number; output: number; cache: number; total: number; requests: number }> = [];
    for (let offset = days - 1; offset >= 0; offset--) {
      const date = new Date();
      date.setHours(12, 0, 0, 0);
      date.setDate(date.getDate() - offset);
      const key = `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`;
      const values = Object.values(daily[key] || {});
      result.push({
        date: key,
        input: values.reduce((sum, item) => sum + item.input_tokens, 0),
        output: values.reduce((sum, item) => sum + item.output_tokens, 0),
        cache: values.reduce((sum, item) => sum + item.cache_read_tokens, 0),
        total: values.reduce((sum, item) => sum + item.total_tokens, 0),
        requests: values.reduce((sum, item) => sum + item.requests, 0),
      });
    }
    return result;
  }, [daily, days]);
  const maxTrend = Math.max(1, ...trend.map((item) => item.total));

  return (
    <FullScreenPanel title="Token 用量统计" subtitle="适配 CC Switch 口径 · 代理请求、输入、输出、缓存与每日趋势" onBack={onBack}>
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-5">
        <div className="rounded-xl border border-border bg-card p-5 shadow-sm"><div className="text-xs text-muted-foreground">真实消耗 Tokens</div><div className="mt-2 text-2xl font-semibold">{formatNumber(totals.tokens)}</div><div className="mt-1 text-[11px] text-muted-foreground">上游 usage.total</div></div>
        <div className="rounded-xl border border-border bg-card p-5 shadow-sm"><div className="text-xs text-muted-foreground">输入 Tokens</div><div className="mt-2 text-2xl font-semibold text-blue-500">{formatNumber(totals.input)}</div></div>
        <div className="rounded-xl border border-border bg-card p-5 shadow-sm"><div className="text-xs text-muted-foreground">输出 Tokens</div><div className="mt-2 text-2xl font-semibold text-violet-500">{formatNumber(totals.output)}</div><div className="mt-1 text-[11px] text-muted-foreground">其中推理 {formatNumber(totals.reasoning)}</div></div>
        <div className="rounded-xl border border-border bg-card p-5 shadow-sm"><div className="text-xs text-muted-foreground">缓存命中</div><div className="mt-2 text-2xl font-semibold text-emerald-600">{formatNumber(totals.cacheRead)}</div></div>
        <div className="rounded-xl border border-border bg-card p-5 shadow-sm"><div className="text-xs text-muted-foreground">请求</div><div className="mt-2 text-2xl font-semibold">{formatNumber(totals.requests)}</div><div className="mt-1 text-[11px] text-muted-foreground">成功 {formatNumber(totals.successes)} · 失败 {formatNumber(totals.failures)}</div></div>
      </div>

      <section className="mt-5 rounded-xl border border-border bg-card p-5 shadow-sm">
        <div className="flex items-start justify-between gap-4"><div><h2 className="font-semibold">每日趋势</h2><p className="mt-1 text-xs text-muted-foreground">升级后的请求开始按天持久化，旧累计数据仍保留在上方总计。</p></div><div className="flex rounded-lg border border-border p-1"><button onClick={() => setDays(7)} className={cn("rounded-md px-3 py-1 text-xs", days === 7 ? "bg-blue-500 text-white" : "text-muted-foreground hover:bg-muted")}>7 天</button><button onClick={() => setDays(30)} className={cn("rounded-md px-3 py-1 text-xs", days === 30 ? "bg-blue-500 text-white" : "text-muted-foreground hover:bg-muted")}>30 天</button></div></div>
        <div className="mt-5 flex h-36 items-end gap-1.5 border-b border-border px-1">
          {trend.map((item) => {
            const barHeight = item.total ? Math.max(5, Math.round((item.total / maxTrend) * 118)) : 2;
            const inputRatio = item.total ? Math.min(100, (item.input / item.total) * 100) : 0;
            const outputRatio = item.total ? Math.min(100 - inputRatio, (item.output / item.total) * 100) : 0;
            return <div key={item.date} className="group relative flex h-full min-w-0 flex-1 items-end justify-center" title={`${item.date} · ${item.requests} 请求 · ${formatNumber(item.total)} Tokens`}><div className="relative w-full max-w-7 overflow-hidden rounded-t bg-gray-200 dark:bg-gray-700" style={{ height: barHeight }}><div className="absolute bottom-0 w-full bg-blue-500" style={{ height: `${inputRatio}%` }} /><div className="absolute w-full bg-violet-500" style={{ bottom: `${inputRatio}%`, height: `${outputRatio}%` }} /></div><span className={cn("absolute -bottom-5 text-[9px] text-muted-foreground", days === 30 && "hidden group-[&:nth-child(5n+1)]:block")}>{item.date.slice(5)}</span></div>;
          })}
        </div>
        <div className="mt-7 flex items-center gap-4 text-xs text-muted-foreground"><span className="flex items-center gap-1.5"><i className="h-2.5 w-2.5 rounded-sm bg-blue-500" />输入</span><span className="flex items-center gap-1.5"><i className="h-2.5 w-2.5 rounded-sm bg-violet-500" />输出</span><span>缓存命中作为输入明细单独统计，不重复叠加总 Token。</span></div>
      </section>

      <section className="mt-5 overflow-hidden rounded-xl border border-border bg-card shadow-sm">
        <div className="border-b border-border px-5 py-4"><h2 className="font-semibold">按账号</h2><p className="mt-1 text-xs text-muted-foreground">真实 Token 来自 Kimi 上游 usage；没有 usage 的成功请求只进入估算列。</p></div>
        {rows.length ? (
          <div className="divide-y divide-border">
            {rows.map(({ item, stats }) => (
              <div key={item.name} className="grid grid-cols-[minmax(0,1.5fr)_repeat(5,minmax(64px,1fr))] items-center gap-3 px-5 py-4 text-sm">
                <div className="min-w-0"><div className="truncate font-medium">{item.name}</div><div className="mt-1 truncate text-xs text-muted-foreground">{item.account || sourceLabel(item)}</div></div>
                <div className="text-right"><div className="text-[10px] text-muted-foreground">请求</div><div className="mt-1 font-medium tabular-nums">{formatNumber(stats.requests)}</div></div>
                <div className="text-right"><div className="text-[10px] text-muted-foreground">输入</div><div className="mt-1 font-medium tabular-nums text-blue-500">{formatNumber(stats.input_tokens)}</div></div>
                <div className="text-right"><div className="text-[10px] text-muted-foreground">输出</div><div className="mt-1 font-medium tabular-nums text-violet-500">{formatNumber(stats.output_tokens)}</div></div>
                <div className="text-right"><div className="text-[10px] text-muted-foreground">缓存</div><div className="mt-1 font-medium tabular-nums text-emerald-600">{formatNumber(stats.cache_read_tokens)}</div></div>
                <div className="text-right"><div className="text-[10px] text-muted-foreground">总 Token</div><div className="mt-1 font-medium tabular-nums">{formatNumber(stats.total_tokens)}</div></div>
                <div className="text-right"><div className="text-[10px] text-muted-foreground">估算 Token</div><div className="mt-1 font-medium tabular-nums">{formatNumber(stats.estimated_tokens)}</div></div>
              </div>
            ))}
          </div>
        ) : <div className="p-10 text-center text-sm text-muted-foreground">添加账号并通过本地代理发起请求后，这里会显示统计。</div>}
      </section>
      <div className="mt-5 rounded-xl border border-amber-500/20 bg-amber-500/5 p-4 text-sm leading-6 text-amber-700 dark:text-amber-300">这些数字是本地观察值，不代表 Kimi 官方剩余额度，也不能保证不同套餐的剩余额度完全相等。</div>
    </FullScreenPanel>
  );
}

function AccountCard({ item, stats, active, rotationEnabled, testing, sortable, dragging, dropEdge, onAction, onPointerDown, onPointerMove, onPointerUp, onPointerCancel, onMove }: {
  item: KeyConfig;
  stats: KeyStats;
  active: boolean;
  rotationEnabled: boolean;
  testing: boolean;
  sortable: boolean;
  dragging: boolean;
  dropEdge: DropEdge | null;
  onAction: (action: AccountAction, item: KeyConfig) => void;
  onPointerDown: (event: React.PointerEvent<HTMLButtonElement>, item: KeyConfig) => void;
  onPointerMove: (event: React.PointerEvent<HTMLButtonElement>) => void;
  onPointerUp: (event: React.PointerEvent<HTMLButtonElement>) => void;
  onPointerCancel: (event: React.PointerEvent<HTMLButtonElement>) => void;
  onMove: (item: KeyConfig, direction: -1 | 1) => void;
}) {
  const state = statusOf(item, stats);
  const quotaCards = [
    { key: "five_hour", label: "5 小时窗口", record: item.quota_refresh?.five_hour },
    { key: "weekly", label: "周额度窗口", record: item.quota_refresh?.weekly },
  ] as const;
  return (
    <article
      className={cn(
        "group relative overflow-hidden rounded-xl border border-border bg-card p-4 text-card-foreground transition-all duration-200 hover:border-blue-500/40 hover:shadow-sm",
        active && "border-blue-500/60 shadow-sm shadow-blue-500/10",
        !item.enabled && "opacity-65",
        dragging && "scale-[0.995] opacity-45",
        dropEdge && "border-blue-500/70 shadow-sm shadow-blue-500/10",
      )}
      data-account-name={item.name}
    >
      <div className={cn("pointer-events-none absolute inset-0 bg-gradient-to-r from-blue-500/10 to-transparent transition-opacity", active ? "opacity-100" : "opacity-0")} />
      {dropEdge && <div className={cn("pointer-events-none absolute inset-x-2 z-20 h-0.5 rounded-full bg-blue-500", dropEdge === "before" ? "top-0" : "bottom-0")} />}
      <div className="relative flex min-w-0 flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="flex min-w-0 flex-1 items-center gap-2">
          <button
            type="button"
            className={cn("-ml-1.5 flex h-8 w-7 touch-none select-none flex-shrink-0 items-center justify-center text-muted-foreground transition-colors", sortable ? "cursor-grab hover:text-foreground active:cursor-grabbing" : "cursor-default opacity-35")}
            disabled={!sortable}
            title="调整顺序"
            aria-label={`调整 ${item.name} 的顺序`}
            aria-keyshortcuts="ArrowUp ArrowDown"
            onPointerDown={(event) => onPointerDown(event, item)}
            onPointerMove={onPointerMove}
            onPointerUp={onPointerUp}
            onPointerCancel={onPointerCancel}
            onLostPointerCapture={onPointerCancel}
            onKeyDown={(event) => {
              if (event.key !== "ArrowUp" && event.key !== "ArrowDown") return;
              event.preventDefault();
              onMove(item, event.key === "ArrowUp" ? -1 : 1);
            }}
          >
            <GripVertical className="h-4 w-4" />
          </button>
          <div className="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-lg border border-border bg-muted text-blue-500 transition-transform group-hover:scale-105"><KeyRound className="h-5 w-5" /></div>
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2"><h3 className="truncate text-base font-semibold">{item.name}</h3>{active && <span className="rounded-md bg-blue-500/10 px-1.5 py-0.5 text-[10px] font-semibold text-blue-600 dark:text-blue-400">当前</span>}{rotationEnabled && item.enabled && <span className="rounded-md bg-emerald-500/10 px-1.5 py-0.5 text-[10px] font-semibold text-emerald-600 dark:text-emerald-400">轮换池</span>}</div>
            <div className="mt-1 flex min-w-0 items-center gap-2 text-xs text-muted-foreground"><span className={cn("h-1.5 w-1.5 flex-shrink-0 rounded-full", state.color)} /><span className={state.text}>{state.label}</span><span>·</span><span className="truncate">{item.account || sourceLabel(item)}</span></div>
            {(item.notes || stats.last_error) && <p className={cn("mt-1 max-w-lg truncate text-xs", stats.last_error ? "text-red-500/85" : "text-muted-foreground")} title={stats.last_error || item.notes}>{stats.last_error || item.notes}</p>}
          </div>
        </div>
        <div className="hidden flex-shrink-0 items-center gap-5 lg:flex">
          <div className="text-right"><div className="text-[10px] uppercase tracking-wide text-muted-foreground">请求</div><div className="mt-1 text-sm font-semibold tabular-nums">{formatNumber(stats.requests)}</div></div>
          <div className="text-right"><div className="text-[10px] uppercase tracking-wide text-muted-foreground">Token</div><div className="mt-1 text-sm font-semibold tabular-nums">{formatNumber(stats.total_tokens || stats.estimated_tokens)}</div></div>
          <div className="text-right"><div className="text-[10px] uppercase tracking-wide text-muted-foreground">失败</div><div className="mt-1 text-sm font-semibold tabular-nums">{formatNumber(stats.failures)}</div></div>
        </div>
        <div className="flex flex-shrink-0 flex-wrap items-center justify-end gap-1.5 opacity-100 transition-opacity sm:pointer-events-none sm:opacity-0 sm:group-hover:pointer-events-auto sm:group-hover:opacity-100 sm:group-has-[:focus-visible]:pointer-events-auto sm:group-has-[:focus-visible]:opacity-100">
          <Button size="sm" variant={active ? "secondary" : "default"} disabled={active || !item.enabled} onClick={() => onAction("use", item)} className="w-[4.5rem] px-2.5">{active ? <Check className="h-4 w-4" /> : <Play className="h-4 w-4" />}{active ? "使用中" : "启用"}</Button>
          <Button size="icon" variant="ghost" title="真实可用性测试（发送最多 1 个输出 Token）" disabled={testing} onClick={() => onAction("test", item)}><RadioTower className={cn("h-4 w-4", testing && "animate-pulse text-blue-500")} /></Button>
          <Button size="icon" variant="ghost" title="编辑" onClick={() => onAction("edit", item)}><Edit3 className="h-4 w-4" /></Button>
          <Button size="icon" variant="ghost" title="复制" onClick={() => onAction("duplicate", item)}><Copy className="h-4 w-4" /></Button>
          <Button size="icon" variant="ghost" title={item.enabled ? "移出轮换池" : "加入轮换池"} onClick={() => onAction("toggle", item)}><ShieldCheck className={cn("h-4 w-4", item.enabled && "text-emerald-500")} /></Button>
          <Button size="icon" variant="ghost" title="删除" onClick={() => onAction("delete", item)} className="hover:text-red-500"><Trash2 className="h-4 w-4" /></Button>
        </div>
      </div>
      <div className="relative mt-3 grid divide-y divide-border border-t border-border sm:grid-cols-2 sm:divide-x sm:divide-y-0">
        {quotaCards.map(({ key, label, record }) => (
          <div key={key} className="min-w-0 py-2.5 sm:px-3 sm:first:pl-0 sm:last:pr-0">
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-xs font-medium text-foreground/85">{label}</span>
              <span className={cn("rounded-full px-2 py-0.5 text-[10px] font-medium", !record ? "bg-muted text-muted-foreground" : record.authoritative ? "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300" : "bg-amber-500/10 text-amber-700 dark:text-amber-300")}>
                {quotaReferenceLabel(record)}
              </span>
            </div>
            <div className="mt-2 text-sm font-semibold tabular-nums">{formatDateTime(record?.next_refresh_at)}</div>
          </div>
        ))}
      </div>
      <div className="relative mt-2 flex items-start gap-1.5 text-[11px] leading-5 text-muted-foreground"><Info className="mt-0.5 h-3 w-3 flex-shrink-0" />刷新时间不直接决定账号可用性，请以真实测试结果为准。</div>
    </article>
  );
}

export default function App() {
  const [status, setStatus] = useState<StatusResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [connectionError, setConnectionError] = useState("");
  const [search, setSearch] = useState("");
  const [panel, setPanel] = useState<Panel>(null);
  const [toast, setToast] = useState<{ message: string; error?: boolean } | null>(null);
  const [dark, setDarkState] = useState(() => localStorage.getItem("kimi-switch-theme") === "dark");
  const [testingKeys, setTestingKeys] = useState<Set<string>>(() => new Set());
  const [deleteTarget, setDeleteTarget] = useState<KeyConfig | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [availableUpdate, setAvailableUpdate] = useState<Update | null>(null);
  const [appVersion, setAppVersion] = useState(packageMetadata.version);
  const buildVersion = displayVersionForRelease(appVersion);
  const [draggedAccount, setDraggedAccount] = useState<string | null>(null);
  const [dropTarget, setDropTarget] = useState<{ name: string; edge: DropEdge } | null>(null);
  const [reordering, setReordering] = useState(false);
  const searchRef = useRef<HTMLInputElement>(null);
  const reorderingRef = useRef(false);
  const dropTargetRef = useRef<{ name: string; edge: DropEdge } | null>(null);
  const pointerDragRef = useRef<{ name: string; startX: number; startY: number; active: boolean } | null>(null);

  const notify = useCallback((message: string, error = false) => {
    setToast({ message, error });
    window.setTimeout(() => setToast(null), 3200);
  }, []);

  const refresh = useCallback(async (silent = false, force = false) => {
    if (!silent) setLoading(true);
    try {
      const nextStatus = await api.status();
      if (!reorderingRef.current || force) setStatus(nextStatus);
      setConnectionError("");
    } catch (error) {
      setConnectionError(error instanceof Error ? error.message : "无法连接本地代理");
    } finally { if (!silent) setLoading(false); }
  }, []);

  useEffect(() => {
    void refresh();
    const timer = window.setInterval(() => void refresh(true), 5000);
    return () => window.clearInterval(timer);
  }, [refresh]);

  useEffect(() => {
    let cancelled = false;
    void getVersion().then((version) => {
      if (!cancelled) setAppVersion(version);
    }).catch(() => undefined);
    return () => { cancelled = true; };
  }, []);

  useEffect(() => {
    let cancelled = false;
    void check().then((update) => {
      if (!cancelled && update) setAvailableUpdate(update);
    }).catch(() => undefined);
    return () => { cancelled = true; };
  }, []);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "f") {
        event.preventDefault(); searchRef.current?.focus();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const setDark = (value: boolean) => {
    setDarkState(value);
    document.documentElement.classList.toggle("dark", value);
    localStorage.setItem("kimi-switch-theme", value ? "dark" : "light");
  };
  useEffect(() => {
    document.documentElement.classList.toggle("dark", dark);
  }, [dark]);

  const accounts = useMemo(() => {
    const q = search.trim().toLowerCase();
    return (status?.config.keys || []).filter((item) => !q || [item.name, item.account, item.notes, item.api_key_env].some((value) => value?.toLowerCase().includes(q)));
  }, [status, search]);

  const canReorder = Boolean(status && status.config.keys.length > 1 && !search.trim() && !reordering);

  const persistAccountOrder = async (nextKeys: KeyConfig[], movedName: string) => {
    if (!status || reorderingRef.current) return;
    const previousKeys = status.config.keys;
    if (nextKeys.every((key, index) => key.name === previousKeys[index]?.name)) return;
    reorderingRef.current = true;
    setReordering(true);
    setStatus((current) => current ? { ...current, config: { ...current.config, keys: nextKeys } } : current);
    try {
      await api.reorderKeys(nextKeys.map((key) => key.name));
      await refresh(true, true);
      notify(`${movedName} 已移动到第 ${nextKeys.findIndex((key) => key.name === movedName) + 1} 位`);
    } catch (error) {
      setStatus((current) => current ? { ...current, config: { ...current.config, keys: previousKeys } } : current);
      notify(error instanceof Error ? error.message : "保存账号顺序失败", true);
    } finally {
      reorderingRef.current = false;
      setReordering(false);
    }
  };

  const setAccountDropTarget = (target: { name: string; edge: DropEdge } | null) => {
    dropTargetRef.current = target;
    setDropTarget(target);
  };

  const finishAccountDrag = () => {
    setDraggedAccount(null);
    setAccountDropTarget(null);
  };

  const startAccountPointerDrag = (event: React.PointerEvent<HTMLButtonElement>, item: KeyConfig) => {
    if (!canReorder || event.button !== 0) return;
    event.preventDefault();
    event.currentTarget.focus();
    event.currentTarget.setPointerCapture(event.pointerId);
    pointerDragRef.current = { name: item.name, startX: event.clientX, startY: event.clientY, active: false };
    setAccountDropTarget(null);
  };

  const moveAccountPointer = (event: React.PointerEvent<HTMLButtonElement>) => {
    const drag = pointerDragRef.current;
    if (!drag || !canReorder) return;
    if (!drag.active && Math.hypot(event.clientX - drag.startX, event.clientY - drag.startY) < 5) return;
    if (!drag.active) {
      drag.active = true;
      setDraggedAccount(drag.name);
    }
    event.preventDefault();
    const card = document.elementFromPoint(event.clientX, event.clientY)?.closest<HTMLElement>("[data-account-name]");
    const targetName = card?.dataset.accountName;
    if (!targetName || targetName === drag.name) {
      setAccountDropTarget(null);
      return;
    }
    const bounds = card.getBoundingClientRect();
    const edge: DropEdge = event.clientY < bounds.top + bounds.height / 2 ? "before" : "after";
    const current = dropTargetRef.current;
    if (current?.name !== targetName || current.edge !== edge) setAccountDropTarget({ name: targetName, edge });
  };

  const endAccountPointerDrag = (event: React.PointerEvent<HTMLButtonElement>) => {
    const drag = pointerDragRef.current;
    const target = dropTargetRef.current;
    pointerDragRef.current = null;
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
    finishAccountDrag();
    if (!status || !canReorder || !drag?.active || !target) return;
    void persistAccountOrder(reorderAccounts(status.config.keys, drag.name, target.name, target.edge), drag.name);
  };

  const cancelAccountPointerDrag = (event: React.PointerEvent<HTMLButtonElement>) => {
    pointerDragRef.current = null;
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
    finishAccountDrag();
  };

  const moveAccount = (item: KeyConfig, direction: -1 | 1) => {
    if (!status || !canReorder) return;
    const index = status.config.keys.findIndex((key) => key.name === item.name);
    const target = status.config.keys[index + direction];
    if (!target) return;
    void persistAccountOrder(reorderAccounts(status.config.keys, item.name, target.name, direction < 0 ? "before" : "after"), item.name);
  };

  const testAccount = async (item: KeyConfig): Promise<ConnectivityResult> => {
    setTestingKeys((old) => new Set(old).add(item.name));
    try {
      return await api.testKey(item.name);
    } finally {
      setTestingKeys((old) => {
        const next = new Set(old);
        next.delete(item.name);
        return next;
      });
    }
  };

  const action = async (kind: AccountAction, item: KeyConfig) => {
    if (kind === "edit") return setPanel({ kind: "form", key: item });
    if (kind === "delete") {
      setDeleteTarget(item);
      return;
    }
    try {
      if (kind === "test") {
        const result = await testAccount(item);
        notify(result.ok ? `${item.name} 连接正常 · ${result.latency_ms} ms` : `${item.name} 测试失败：${result.message}`, !result.ok);
        await refresh(true);
        return;
      }
      if (kind === "use") await api.useKey(item.name);
      if (kind === "duplicate") await api.duplicateKey(item.name);
      if (kind === "toggle") await api.toggleKey(item.name, !item.enabled);
      notify("操作已完成");
      await refresh(true);
    } catch (error) { notify(error instanceof Error ? error.message : "操作失败", true); }
  };

  const confirmDelete = async () => {
    if (!deleteTarget || deleting) return;
    setDeleting(true);
    try {
      await api.deleteKey(deleteTarget.name);
      setDeleteTarget(null);
      await refresh(true);
      notify("账号已删除");
    } catch (error) {
      notify(error instanceof Error ? error.message : "删除失败", true);
    } finally {
      setDeleting(false);
    }
  };

  const testAllAccounts = async () => {
    if (!status || testingKeys.size) return;
    const results = await Promise.all(status.config.keys.map(async (item) => {
      try { return await testAccount(item); }
      catch (error) { return { name: item.name, ok: false, status: "network_error", latency_ms: 0, message: error instanceof Error ? error.message : "测试失败", tested_at: new Date().toISOString() } satisfies ConnectivityResult; }
    }));
    await refresh(true);
    const passed = results.filter((result) => result.ok).length;
    notify(`连通性测试完成：${passed}/${results.length} 个账号可用`, passed !== results.length);
  };

  const toggleRotation = async (enabled: boolean) => {
    if (!status) return;
    try {
      await api.setRotation(enabled, status.config.rotation.mode);
      await refresh(true);
      notify(enabled ? "自动轮换已开启" : "自动轮换已关闭");
    } catch (error) { notify(error instanceof Error ? error.message : "切换失败", true); }
  };

  const toggleTakeover = async (enabled: boolean) => {
    try {
      const result = await api.setTakeover(enabled);
      await refresh(true);
      notify(`${enabled ? "已切换为 KimiSwitch 接管" : "已恢复 Kimi Code 直连"}；请在正在运行的 Kimi Code 中输入 /reload`);
      if (!result.reload_required) notify(enabled ? "KimiSwitch 已接管 Kimi Code" : "Kimi Code 已恢复直连");
    } catch (error) { notify(error instanceof Error ? error.message : "接管切换失败", true); }
  };

  if (loading && !status) {
    return <div className="flex h-full items-center justify-center bg-background text-muted-foreground"><RefreshCw className="mr-2 h-4 w-4 animate-spin" />正在启动 KimiSwitch…</div>;
  }

  if (!status) {
    return <div className="flex h-full flex-col items-center justify-center bg-background p-8 text-center"><div className="flex h-14 w-14 items-center justify-center rounded-2xl bg-red-500/10 text-red-500"><X className="h-7 w-7" /></div><h1 className="mt-5 text-xl font-semibold">本地代理没有启动</h1><p className="mt-2 max-w-md text-sm text-muted-foreground">{connectionError || "Tauri 正在等待 Go sidecar。"}</p><Button className="mt-5" onClick={() => void refresh()}><RefreshCw className="h-4 w-4" />重试</Button></div>;
  }

  return (
    <div className="flex h-full flex-col overflow-hidden bg-background text-foreground">
      <header className="flex h-16 flex-shrink-0 items-center justify-between gap-2 border-b border-border bg-background/85 px-3 backdrop-blur-md sm:px-6" data-tauri-drag-region>
        <div className="no-drag flex min-w-0 items-center gap-1"><div className="mr-1 flex min-w-0 items-baseline gap-2"><span className="truncate text-base font-semibold text-blue-500 sm:text-xl">KimiSwitch</span><span className="hidden min-w-12 text-xs tabular-nums text-muted-foreground sm:inline">v{buildVersion}</span></div><Button className="h-8 w-8" variant="ghost" size="icon" title="设置" onClick={() => setPanel({ kind: "settings" })}><Settings className="h-4 w-4" /></Button>{availableUpdate && <Button className="h-8 w-8" variant="ghost" size="icon" title={`发现新版本 ${availableUpdate.version}`} onClick={() => setPanel({ kind: "updates", update: availableUpdate })}><Download className="h-4 w-4 text-blue-500" /></Button>}<Button className="h-8 w-8" variant="ghost" size="icon" title="用量统计" onClick={() => setPanel({ kind: "usage" })}><Activity className="h-4 w-4" /></Button></div>
        <div className="no-drag flex flex-shrink-0 items-center gap-1 sm:gap-2">
          <span className="hidden items-center gap-1.5 rounded-full bg-emerald-500/10 px-2.5 py-1 text-xs font-medium text-emerald-600 sm:flex"><span className="h-1.5 w-1.5 rounded-full bg-emerald-500" />代理运行中</span>
          <div className={cn("flex items-center gap-2 rounded-lg border px-2 py-1.5 sm:px-3", status.kimi_takeover.enabled ? "border-emerald-500/30 bg-emerald-500/5" : "border-border")} title={status.kimi_takeover.error || (status.kimi_takeover.config_error && `配置异常：${status.kimi_takeover.config_error}；重新切换开关可自动修复`) || `配置：${status.kimi_takeover.config_path}`}><span className="hidden text-xs text-muted-foreground lg:inline">{status.kimi_takeover.enabled ? "KimiSwitch 接管" : "Kimi Code 直连"}</span>{status.kimi_takeover.config_error && <span className="hidden text-xs text-amber-500 xl:inline">配置异常</span>}<Switch aria-label="KimiSwitch 接管" checked={status.kimi_takeover.enabled} disabled={!status.kimi_takeover.enabled && (status.config.keys.length === 0 || Boolean(status.kimi_takeover.error))} onCheckedChange={toggleTakeover} /></div>
          <div className="flex items-center gap-2 rounded-lg border border-border px-2 py-1.5 sm:px-3"><span className="hidden text-xs text-muted-foreground lg:inline">自动轮换</span><Switch aria-label="自动轮换" checked={status.config.rotation.enabled} onCheckedChange={toggleRotation} /></div>
          <Button size="icon" title="添加账号" className="h-8 w-8 rounded-full bg-orange-500 shadow-lg shadow-orange-500/25 hover:bg-orange-600" onClick={() => setPanel({ kind: "form" })}><Plus className="h-4 w-4" /></Button>
        </div>
      </header>

      <main className="min-h-0 flex-1 overflow-y-auto px-3 py-5 sm:px-6">
        <div className="mx-auto max-w-6xl">
          <div className="mb-5 flex flex-wrap items-end justify-between gap-4">
            <div><h1 className="text-2xl font-semibold tracking-tight">Kimi Code 账号</h1><p className="mt-1 text-sm text-muted-foreground">管理已授权的账号、切换当前 Key，并在故障时自动切到下一个可用账号。</p></div>
            <div className="flex w-full items-center gap-2 sm:w-auto"><Button className="min-w-0 flex-1 sm:flex-none" variant="outline" title="逐个发送最多 1 个输出 Token，检测真实额度与鉴权状态" disabled={testingKeys.size > 0} onClick={() => void testAllAccounts()}><RadioTower className={cn("h-4 w-4", testingKeys.size > 0 && "animate-pulse")} /><span className="hidden sm:inline">{testingKeys.size > 0 ? `测试中 ${testingKeys.size}` : "测试全部"}</span></Button><Button className="min-w-0 flex-1 sm:flex-none" variant="outline" title="导入当前配置" onClick={() => setPanel({ kind: "import" })}><Download className="h-4 w-4" /><span className="hidden sm:inline">导入当前配置</span></Button><Button className="min-w-0 flex-1 sm:flex-none" title="添加账号" onClick={() => setPanel({ kind: "form" })}><Plus className="h-4 w-4" /><span className="hidden sm:inline">添加账号</span></Button></div>
          </div>

          <div className="mb-4 grid grid-cols-2 gap-3 lg:grid-cols-4">
            <div className="rounded-xl border border-border bg-card p-4"><div className="text-xs text-muted-foreground">账号</div><div className="mt-1 text-xl font-semibold">{status.config.keys.length}</div></div>
            <div className="rounded-xl border border-border bg-card p-4"><div className="text-xs text-muted-foreground">轮换模式</div><div className="mt-1 truncate text-sm font-semibold">{status.config.rotation.enabled ? "故障时切换" : "未开启"}</div></div>
            <div className="rounded-xl border border-border bg-card p-4"><div className="text-xs text-muted-foreground">总请求</div><div className="mt-1 text-xl font-semibold">{formatNumber(Object.values(status.keys).reduce((sum, value) => sum + value.requests, 0))}</div></div>
            <div className="rounded-xl border border-border bg-card p-4"><div className="text-xs text-muted-foreground">已观察 Token</div><div className="mt-1 text-xl font-semibold">{formatNumber(Object.values(status.keys).reduce((sum, value) => sum + value.total_tokens, 0))}</div></div>
          </div>

          <div className="mb-4 flex items-center gap-2">
            <div className="relative flex-1"><Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" /><input ref={searchRef} className={cn(fieldClass, "pl-9 pr-9")} value={search} onChange={(e) => setSearch(e.target.value)} placeholder="搜索账号、用途或备注…" />{search && <button onClick={() => setSearch("")} className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"><X className="h-4 w-4" /></button>}</div>
            <Button variant="outline" size="icon" onClick={() => void refresh(true)} title="刷新"><RefreshCw className="h-4 w-4" /></Button>
          </div>

          {!status.config.keys.length ? (
            <div className="flex flex-col items-center justify-center rounded-lg border border-dashed border-border p-10 text-center"><div className="mb-4 flex h-16 w-16 items-center justify-center rounded-full bg-muted"><Users className="h-7 w-7 text-muted-foreground" /></div><h3 className="text-lg font-semibold">还没有账号</h3><p className="mt-2 max-w-lg text-sm text-muted-foreground">导入 Kimi Code 当前配置，或手动添加公司授权的 API Key。</p><div className="mt-6 flex flex-col gap-2"><Button onClick={() => setPanel({ kind: "import" })}><Download className="h-4 w-4" />导入当前配置</Button><Button variant="outline" onClick={() => setPanel({ kind: "form" })}>添加账号</Button></div></div>
          ) : accounts.length ? (
            <div className="grid gap-3">{accounts.map((item) => <AccountCard key={item.name} item={item} stats={status.keys[item.name] || emptyStats} active={status.config.active_key === item.name} rotationEnabled={status.config.rotation.enabled} testing={testingKeys.has(item.name)} sortable={canReorder} dragging={draggedAccount === item.name} dropEdge={dropTarget?.name === item.name ? dropTarget.edge : null} onAction={action} onPointerDown={startAccountPointerDrag} onPointerMove={moveAccountPointer} onPointerUp={endAccountPointerDrag} onPointerCancel={cancelAccountPointerDrag} onMove={moveAccount} />)}</div>
          ) : <div className="rounded-lg border border-dashed border-border p-10 text-center text-sm text-muted-foreground">没有匹配的账号</div>}

          <div className="mt-5 flex flex-wrap items-center justify-between gap-2 rounded-xl border border-border bg-muted/25 px-4 py-3 text-xs text-muted-foreground"><span>客户端地址：http://127.0.0.1:15722/v1</span><span>{status.config.rotation.enabled ? "轮换已开启 · 故障时切换" : `当前账号：${status.config.active_key || "未选择"}`}</span><span className="font-medium text-foreground/70">Powered by kyuiazusa</span></div>
        </div>
      </main>

      {panel?.kind === "form" && <AccountForm current={panel.key} onBack={() => setPanel(null)} onSaved={() => refresh(true)} notify={notify} />}
      {panel?.kind === "settings" && <SettingsPanel status={status} appVersion={appVersion} onBack={() => setPanel(null)} onSaved={() => refresh(true)} notify={notify} dark={dark} setDark={setDark} onUpdates={() => setPanel({ kind: "updates", update: availableUpdate || undefined })} onChangelog={() => setPanel({ kind: "changelog" })} />}
      {panel?.kind === "updates" && <UpdatePanel initialUpdate={panel.update} onBack={() => setPanel(null)} notify={notify} />}
      {panel?.kind === "changelog" && <ChangelogPanel appVersion={appVersion} onBack={() => setPanel({ kind: "settings" })} />}
      {panel?.kind === "import" && <ImportPanel onBack={() => setPanel(null)} onImported={() => refresh(true)} notify={notify} />}
      {panel?.kind === "usage" && <UsagePanel status={status} onBack={() => setPanel(null)} />}
      {deleteTarget && (
        <div className="fixed inset-0 z-[90] flex items-center justify-center bg-black/45 p-5 backdrop-blur-sm" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget && !deleting) setDeleteTarget(null); }}>
          <section className="w-full max-w-md rounded-2xl border border-border bg-card p-6 text-card-foreground shadow-2xl" role="dialog" aria-modal="true" aria-labelledby="delete-title">
            <div className="flex h-11 w-11 items-center justify-center rounded-xl bg-red-500/10 text-red-500"><Trash2 className="h-5 w-5" /></div>
            <h2 id="delete-title" className="mt-4 text-lg font-semibold">删除账号“{deleteTarget.name}”？</h2>
            <p className="mt-2 text-sm leading-6 text-muted-foreground">这会删除 KimiSwitch 中的账号记录和本地统计。系统凭据库中的原始 Key 不会被删除。</p>
            {status.config.active_key === deleteTarget.name && <p className="mt-3 rounded-lg border border-amber-500/20 bg-amber-500/5 px-3 py-2 text-sm text-amber-700 dark:text-amber-300">这是当前账号；删除后会自动切换到下一条可用记录。</p>}
            <div className="mt-6 flex justify-end gap-2"><Button variant="outline" disabled={deleting} onClick={() => setDeleteTarget(null)}>取消</Button><Button disabled={deleting} className="bg-red-500 text-white hover:bg-red-600" onClick={() => void confirmDelete()}>{deleting ? "删除中…" : "确认删除"}</Button></div>
          </section>
        </div>
      )}
      {toast && <div role={toast.error ? "alert" : "status"} aria-live={toast.error ? "assertive" : "polite"} className={cn("fixed bottom-5 right-5 z-[80] max-w-sm rounded-xl border px-4 py-3 text-sm shadow-xl", toast.error ? "border-red-500/30 bg-red-50 text-red-700 dark:bg-red-950 dark:text-red-200" : "border-border bg-card text-card-foreground")}><div className="flex items-center gap-2">{toast.error ? <X className="h-4 w-4" /> : <Check className="h-4 w-4 text-emerald-500" />}{toast.message}</div></div>}
    </div>
  );
}
