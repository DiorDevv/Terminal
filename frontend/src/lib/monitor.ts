// Types for /api/monitor/* and small formatters shared by the statistics and
// alert pages.

export type RangeName = "24h" | "7d" | "30d";

export const ranges: { value: RangeName; label: string }[] = [
  { value: "24h", label: "24 soat" },
  { value: "7d", label: "7 kun" },
  { value: "30d", label: "30 kun" },
];

export interface Bucket {
  t: number;
  requests: number;
  bytes: number;
  hits: number;
  denied: number;
  errors: number;
}

export interface Summary {
  range: RangeName;
  from: number;
  to: number;
  requests: number;
  bytes: number;
  hits: number;
  hit_bytes: number;
  denied: number;
  errors: number;
  clients: number;
  users: number;
  domains: number;
  timeline: Bucket[];
}

export interface TopRow {
  key: string;
  requests: number;
  bytes: number;
  hits: number;
  denied: number;
}

export interface DeniedRow {
  time: number;
  client: string;
  user: string;
  method: string;
  url: string;
}

export interface SquidInfo {
  available: boolean;
  error?: string;
  version: string;
  clients: number;
  requests_total: number;
  avg_per_minute: number;
  hit_ratio_5m: number;
  hit_ratio_60m: number;
  byte_hit_ratio_5m: number;
  byte_hit_ratio_60m: number;
  swap_kb: number;
  swap_used_pct: number;
  mem_kb: number;
  mem_used_pct: number;
  uptime_seconds: number;
  cpu_pct: number;
  rss_kb: number;
  fd_used: number;
  fd_max: number;
}

export interface DiskUsage {
  path: string;
  total_bytes: number;
  used_bytes: number;
  free_bytes: number;
  used_percent: number;
}

export interface ReaderStatus {
  path: string;
  lines: number;
  last_entry: number;
  last_poll: number;
  last_error: string;
  file_size: number;
  readable: boolean;
}

export interface Live {
  squid: { running: boolean; detail: string };
  info: SquidInfo;
  reader: ReaderStatus;
  disks: DiskUsage[];
}

export interface LogFile {
  name: string;
  size: number;
  modified: number;
}

export function formatCount(n: number): string {
  return Math.round(n).toLocaleString("en-US");
}

export function formatPercent(part: number, whole: number): string {
  if (!whole) return "—";
  const p = (part / whole) * 100;
  return `${p >= 10 ? p.toFixed(0) : p.toFixed(1)}%`;
}

/** "3 daqiqa oldin" for a unix timestamp in seconds; "—" when unset. */
export function agoFromUnix(sec: number, nowMs: number = Date.now()): string {
  if (!sec) return "—";
  const s = Math.max(0, Math.floor(nowMs / 1000 - sec));
  if (s < 60) return "hozirgina";
  if (s < 3600) return `${Math.floor(s / 60)} daqiqa oldin`;
  if (s < 86400) return `${Math.floor(s / 3600)} soat oldin`;
  return `${Math.floor(s / 86400)} kun oldin`;
}

/** A duration in seconds, e.g. "2 kun 3 soat". */
export function formatSeconds(total: number): string {
  const t = Math.max(0, Math.floor(total));
  const days = Math.floor(t / 86400);
  const hours = Math.floor((t % 86400) / 3600);
  const minutes = Math.floor((t % 3600) / 60);
  if (days > 0) return `${days} kun ${hours} soat`;
  if (hours > 0) return `${hours} soat ${minutes} daqiqa`;
  if (minutes > 0) return `${minutes} daqiqa`;
  return `${t} soniya`;
}

export function formatUnix(sec: number): string {
  return new Date(sec * 1000).toLocaleString("en-GB", {
    day: "2-digit",
    month: "short",
    hour: "2-digit",
    minute: "2-digit",
  });
}

/** Clean upper bound and tick step for a y axis that starts at zero. */
export function niceScale(max: number, ticks = 4): { top: number; step: number } {
  if (max <= 0) return { top: ticks, step: 1 };
  const raw = max / ticks;
  const pow = 10 ** Math.floor(Math.log10(raw));
  const f = raw / pow;
  const nice = f <= 1 ? 1 : f <= 2 ? 2 : f <= 5 ? 5 : 10;
  const step = nice * pow;
  return { top: step * Math.ceil(max / step), step };
}
