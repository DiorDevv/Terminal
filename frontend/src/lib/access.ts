// Types and labels for the access-rule pages. The API is language-neutral; the
// texts the administrator reads live here.

export interface AclType {
  type: string;
  regex: boolean;
  managed: boolean;
}

export interface Acl {
  id: number;
  name: string;
  type: string;
  values: string[];
  case_insensitive: boolean;
  description: string;
  used_by: number;
}

export interface RuleTerm {
  acl_id: number;
  negate: boolean;
  acl_name?: string;
  acl_type?: string;
}

export interface Rule {
  id: number;
  position: number;
  action: "allow" | "deny";
  terms: RuleTerm[];
  enabled: boolean;
  comment: string;
}

export interface PolicyRule {
  index: number;
  line: number;
  action: "allow" | "deny";
  terms: { acl: string; negate: boolean }[];
  raw: string;
  source: string;
  comment?: string;
}

export interface PolicyAcl {
  name: string;
  type: string;
  args: string[];
}

export interface TermResult {
  acl: string;
  negate: boolean;
  result: "match" | "no-match" | "unknown" | "skipped";
  detail: string;
}

export interface RuleTrace {
  rule: PolicyRule;
  result: "match" | "no-match" | "unknown" | "auth-required" | "not-reached";
  terms: TermResult[];
}

export interface Verdict {
  decision: "allow" | "deny" | "auth_required" | "unknown";
  rule_index: number;
  trace: RuleTrace[];
  notes: string[];
}

export interface BlocklistSource {
  id: number;
  name: string;
  url: string;
  enabled: boolean;
  interval_hours: number;
  last_fetched: number;
  last_status: string;
  entry_count: number;
  acl_id: number;
  acl_name: string;
}

export const aclTypeMeta: Record<string, { label: string; hint: string; placeholder: string }> = {
  src: {
    label: "Manba IP (kim)",
    hint: "So'rov yuborayotgan qurilma. IP, tarmoq yoki oraliq; har qatorga bittadan.",
    placeholder: "10.0.0.5\n192.168.1.0/24\n10.0.0.10-10.0.0.20",
  },
  dst: {
    label: "Manzil IP (qayerga)",
    hint: "Sayt joylashgan server IP'si. Squid domenni avval aniqlaydi (DNS).",
    placeholder: "203.0.113.0/24",
  },
  dstdomain: {
    label: "Domenlar",
    hint: "example.com yozilsa, uning barcha subdomenlari ham qamraladi.",
    placeholder: "facebook.com\ntiktok.com",
  },
  dstdom_regex: {
    label: "Domen (regex)",
    hint: "Domen nomiga qo'llanadigan regular ifoda (POSIX; \\d o'rniga [0-9]).",
    placeholder: "^ads[0-9]+[.]",
  },
  url_regex: {
    label: "URL (kalit so'z / regex)",
    hint: "To'liq URL ichidan qidiriladi. Oddiy so'zlar ham bo'ladi. Bo'shliq mumkin emas.",
    placeholder: "casino\nbet365",
  },
  urlpath_regex: {
    label: "URL yo'li (regex)",
    hint: "Domendan keyingi qism (/private/...).",
    placeholder: "^/private/",
  },
  browser: {
    label: "Brauzer (User-Agent)",
    hint: "So'rovni yuborgan dastur nomi bo'yicha (regex).",
    placeholder: "curl\nPython-urllib",
  },
  port: {
    label: "Port",
    hint: "Bitta port yoki oraliq.",
    placeholder: "8080\n8000-8100",
  },
  method: {
    label: "HTTP usul",
    hint: "GET, POST, CONNECT (HTTPS ulanishi) va boshqalar.",
    placeholder: "POST\nPUT",
  },
  time: {
    label: "Vaqt oynasi",
    hint: "Kunlar (S=yakshanba, M, T, W, H=payshanba, F, A=shanba) va soat oralig'i. Yarim tundan o'tadigan oyna ikkiga bo'linadi.",
    placeholder: "MTWHF 09:00-18:00\nSA 10:00-14:00",
  },
  proxy_auth: {
    label: "Proksi foydalanuvchilari",
    hint: "REQUIRED: istalgan kirgan foydalanuvchi. Yoki foydalanuvchi nomlari. Avval Foydalanuvchilar sahifasida proksi foydalanuvchisi bo'lishi kerak.",
    placeholder: "alice\nbob",
  },
  blocklist: {
    label: "Blocklist manbasi",
    hint: "Yuklab olinadigan ro'yxat (Blocklist manbalari sahifasi).",
    placeholder: "",
  },
};

export const sourceLabels: Record<string, { label: string; tone: string }> = {
  stock: { label: "Squid asosiy", tone: "bg-white/[0.06] text-neutral-300" },
  blacklist: { label: "Bloklangan domenlar", tone: "bg-red-500/15 text-red-300" },
  time_restriction: { label: "Vaqt cheklovi", tone: "bg-amber-500/15 text-amber-300" },
  user_policy: { label: "Hisob holati", tone: "bg-red-500/15 text-red-300" },
  rule: { label: "Sizning qoidangiz", tone: "bg-indigo-500/15 text-indigo-300" },
  proxy_auth: { label: "Proksi login", tone: "bg-sky-500/15 text-sky-300" },
  lan: { label: "LAN ruxsati", tone: "bg-emerald-500/15 text-emerald-300" },
  other: { label: "Boshqa", tone: "bg-white/[0.06] text-neutral-300" },
};

export const decisionMeta: Record<string, { label: string; tone: string; text: string }> = {
  allow: {
    label: "RUXSAT",
    tone: "border-emerald-500/30 bg-emerald-500/10 text-emerald-300",
    text: "So'rov o'tkaziladi.",
  },
  deny: {
    label: "RAD ETILADI",
    tone: "border-red-500/30 bg-red-500/10 text-red-300",
    text: "Squid so'rovni rad etadi (403).",
  },
  auth_required: {
    label: "LOGIN SO'RALADI",
    tone: "border-sky-500/30 bg-sky-500/10 text-sky-300",
    text: "Squid foydalanuvchidan login va parol so'raydi (407). Foydalanuvchi nomini kiritib qayta tekshiring.",
  },
  unknown: {
    label: "ANIQLAB BO'LMADI",
    tone: "border-amber-500/30 bg-amber-500/10 text-amber-300",
    text: "Qoidalardan biri panel baholay olmaydigan shartga tayanadi.",
  },
};

/** Escapes a plain keyword so it is safe inside a squid (POSIX) pattern. */
export function keywordToPattern(word: string): string {
  return word.replace(/[.]/g, "[.]");
}
