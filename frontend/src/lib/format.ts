export function formatBytes(n: number): string {
  if (!n || n < 0) return "—";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = n;
  let i = 0;
  while (value >= 1024 && i < units.length - 1) {
    value /= 1024;
    i++;
  }
  return `${value >= 100 || i === 0 ? Math.round(value) : value.toFixed(1)} ${units[i]}`;
}

/** Human-readable time since `startedAtSec` (unix seconds), e.g. "2 kun 3 soat". */
export function formatUptime(startedAtSec: number, nowMs: number = Date.now()): string {
  if (!startedAtSec) return "—";
  const total = Math.max(0, Math.floor(nowMs / 1000 - startedAtSec));

  const days = Math.floor(total / 86400);
  const hours = Math.floor((total % 86400) / 3600);
  const minutes = Math.floor((total % 3600) / 60);

  if (days > 0) return `${days} kun ${hours} soat`;
  if (hours > 0) return `${hours} soat ${minutes} daqiqa`;
  if (minutes > 0) return `${minutes} daqiqa`;
  return `${total} soniya`;
}

export function formatDateTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString("en-GB", {
    day: "2-digit",
    month: "short",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

export function timeAgo(iso: string, nowMs: number = Date.now()): string {
  const secs = Math.floor((nowMs - new Date(iso).getTime()) / 1000);
  if (Number.isNaN(secs)) return "";
  if (secs < 60) return "hozirgina";
  if (secs < 3600) return `${Math.floor(secs / 60)} daqiqa oldin`;
  if (secs < 86400) return `${Math.floor(secs / 3600)} soat oldin`;
  return `${Math.floor(secs / 86400)} kun oldin`;
}
