// Types and helpers for proxy accounts, user groups and limits
// (/api/squid/proxy-users, /user-groups, /limits).

export type AccountStatus = "active" | "disabled" | "expired" | "quota_exceeded";

export interface GroupRef {
  id: number;
  name: string;
}

export interface ProxyUser {
  username: string;
  disabled: boolean;
  expires_at: number; // unix seconds, 0 = never
  daily_quota_mb: number; // 0 = unlimited
  note: string;
  created_at: number;
  groups: GroupRef[];
  used_today: number; // bytes
  status: AccountStatus;
}

export interface UserGroup {
  id: number;
  name: string;
  members: string[];
}

export interface IPGroup {
  id: number;
  name: string;
  members: string[];
}

export type LimitKind = "speed" | "download";
export type SpeedScope = "each_user" | "each_client" | "shared";

export interface LimitSpec {
  everyone: boolean;
  users: string[] | null;
  user_groups: number[] | null;
  ip_groups: number[] | null;
  scope?: SpeedScope;
  rate_kbps?: number;
  burst_kb?: number;
  max_mb?: number;
}

export interface LimitRule {
  id: number;
  name: string;
  kind: LimitKind;
  enabled: boolean;
  spec: LimitSpec;
}

export interface ImportRow {
  line: number;
  username: string;
  action: "create" | "update" | "error";
  error?: string;
}

export interface ImportReport {
  dry_run: boolean;
  applied: boolean;
  created: number;
  updated: number;
  errors: number;
  rows: ImportRow[];
}

export const statusMeta: Record<AccountStatus, { label: string; tone: string }> = {
  active: { label: "Faol", tone: "bg-emerald-500/15 text-emerald-300" },
  disabled: { label: "O'chirilgan", tone: "bg-neutral-500/20 text-neutral-300" },
  expired: { label: "Muddati tugagan", tone: "bg-amber-500/15 text-amber-300" },
  quota_exceeded: { label: "Kvota to'lgan", tone: "bg-red-500/15 text-red-300" },
};

const pad = (n: number) => String(n).padStart(2, "0");

/** The date (local) on which an account stops working, as YYYY-MM-DD, or "". */
export function expiryToInput(expiresAt: number): string {
  if (!expiresAt) return "";
  // The stored instant is the start of the day AFTER the last valid day.
  const d = new Date((expiresAt - 1) * 1000);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

/** YYYY-MM-DD → unix seconds of local midnight after that day; "" → 0 (never). */
export function inputToExpiry(value: string): number {
  if (!value) return 0;
  const [y, m, d] = value.split("-").map(Number);
  return Math.floor(new Date(y, m - 1, d + 1, 0, 0, 0).getTime() / 1000);
}

export function formatExpiry(expiresAt: number): string {
  return expiresAt ? expiryToInput(expiresAt) : "cheksiz";
}

export function describeWho(spec: LimitSpec, users: UserGroup[], ipGroups: IPGroup[]): string {
  if (spec.everyone) return "Hamma";
  const parts: string[] = [];
  if (spec.users?.length) parts.push(spec.users.join(", "));
  for (const id of spec.user_groups ?? []) {
    const g = users.find((x) => x.id === id);
    parts.push(`guruh: ${g?.name ?? `#${id}`}`);
  }
  for (const id of spec.ip_groups ?? []) {
    const g = ipGroups.find((x) => x.id === id);
    parts.push(`IP: ${g?.name ?? `#${id}`}`);
  }
  return parts.join(" · ") || "—";
}

export const scopeLabels: Record<SpeedScope, string> = {
  each_user: "Har bir foydalanuvchiga alohida",
  each_client: "Har bir qurilmaga (IP) alohida",
  shared: "Hamma uchun umumiy",
};
