import { Bell, BellRing, CheckCircle2, Send, Siren } from "lucide-react";
import { type ReactNode, useCallback, useEffect, useState } from "react";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";
import { Input, Label } from "../components/ui/Input";
import { PageHeader } from "../components/ui/PageHeader";
import { ListSkeleton } from "../components/ui/Skeleton";
import { api, errorMessage } from "../lib/api";
import { formatUnix } from "../lib/monitor";
import { useToast } from "../lib/toast";

interface Spike {
  enabled: boolean;
  threshold: number;
  window_minutes: number;
}

// What the server sends: secrets are replaced by "…_set" flags.
interface ConfigView {
  squid_down: { enabled: boolean };
  disk_full: { enabled: boolean; percent: number };
  denied_spike: Spike;
  error_spike: Spike;
  telegram: { enabled: boolean; token_set: boolean; chat_id: string };
  webhook: { enabled: boolean; url_set: boolean; host: string };
  email: {
    enabled: boolean;
    host: string;
    port: number;
    username: string;
    password_set: boolean;
    from: string;
    to: string[];
  };
}

// The editable form: the view plus the secret fields, which start empty. An
// empty secret on save means "keep the stored one".
interface Form extends ConfigView {
  telegramToken: string;
  webhookUrl: string;
  emailPassword: string;
  emailTo: string;
}

interface Condition {
  key: string;
  enabled: boolean;
  firing: boolean;
  since: number;
  detail: string;
}

interface AlertEvent {
  id: number;
  time: number;
  key: string;
  kind: "firing" | "reminder" | "resolved" | "test";
  message: string;
  delivery: string;
}

const conditionLabels: Record<string, string> = {
  squid_down: "Squid to'xtab qolishi",
  disk_full: "Disk to'lishi",
  denied_spike: "Bloklashlar ko'payishi",
  error_spike: "Server xatolari ko'payishi",
};

const kindStyle: Record<AlertEvent["kind"], { label: string; cls: string }> = {
  firing: { label: "Muammo", cls: "bg-red-500/15 text-red-300" },
  reminder: { label: "Eslatma", cls: "bg-amber-500/15 text-amber-300" },
  resolved: { label: "Hal bo'ldi", cls: "bg-emerald-500/15 text-emerald-300" },
  test: { label: "Sinov", cls: "bg-white/[0.08] text-neutral-300" },
};

function toForm(c: ConfigView): Form {
  return { ...c, telegramToken: "", webhookUrl: "", emailPassword: "", emailTo: c.email.to.join(", ") };
}

function toPayload(f: Form) {
  return {
    squid_down: f.squid_down,
    disk_full: f.disk_full,
    denied_spike: f.denied_spike,
    error_spike: f.error_spike,
    telegram: { enabled: f.telegram.enabled, token: f.telegramToken, chat_id: f.telegram.chat_id.trim() },
    webhook: { enabled: f.webhook.enabled, url: f.webhookUrl.trim() },
    email: {
      enabled: f.email.enabled,
      host: f.email.host.trim(),
      port: f.email.port,
      username: f.email.username,
      password: f.emailPassword,
      from: f.email.from.trim(),
      to: f.emailTo
        .split(/[,\s]+/)
        .map((x) => x.trim())
        .filter(Boolean),
    },
  };
}

export default function Alerts() {
  const toast = useToast();
  const [form, setForm] = useState<Form | null>(null);
  const [status, setStatus] = useState<Condition[]>([]);
  const [events, setEvents] = useState<AlertEvent[]>([]);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState<"" | "save" | "test" | "check">("");
  const [dirty, setDirty] = useState(false);

  const load = useCallback(
    async (resetForm: boolean) => {
      try {
        const r = await api.get<{ config: ConfigView; status: Condition[]; events: AlertEvent[] }>("/monitor/alerts");
        if (resetForm) {
          setForm(toForm(r.data.config));
          setDirty(false);
        }
        setStatus(r.data.status);
        setEvents(r.data.events);
      } catch (e) {
        toast.error(errorMessage(e, "Sozlamalarni yuklab bo'lmadi"));
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );

  useEffect(() => {
    load(true);
  }, [load]);

  function patch(fn: (f: Form) => Form) {
    setForm((f) => (f ? fn(f) : f));
    setDirty(true);
  }

  async function save() {
    if (!form) return;
    setBusy("save");
    setFieldErrors({});
    try {
      await api.put("/monitor/alerts", toPayload(form));
      toast.success("Saqlandi");
      await load(true);
    } catch (e) {
      const data = (e as { response?: { data?: { fields?: Record<string, string> } } }).response?.data;
      if (data?.fields) setFieldErrors(data.fields);
      toast.error(errorMessage(e, "Saqlab bo'lmadi"));
    } finally {
      setBusy("");
    }
  }

  async function sendTest() {
    setBusy("test");
    try {
      const r = await api.post<{ results: Record<string, string> }>("/monitor/alerts/test");
      const failed = Object.entries(r.data.results).filter(([, v]) => v !== "ok");
      if (failed.length === 0) toast.success(`Sinov xabari yuborildi: ${Object.keys(r.data.results).join(", ")}`);
      else toast.error(failed.map(([k, v]) => `${k}: ${v}`).join(" · "));
      await load(false);
    } catch (e) {
      toast.error(errorMessage(e, "Sinov xabarini yuborib bo'lmadi"));
    } finally {
      setBusy("");
    }
  }

  async function checkNow() {
    setBusy("check");
    try {
      const r = await api.post<{ events: AlertEvent[] }>("/monitor/alerts/check");
      toast.success(r.data.events.length ? `${r.data.events.length} ta yangi hodisa` : "Hammasi joyida, yangi hodisa yo'q");
      await load(false);
    } catch (e) {
      toast.error(errorMessage(e, "Tekshirib bo'lmadi"));
    } finally {
      setBusy("");
    }
  }

  const err = (k: string) => fieldErrors[k] && <p className="mt-1 text-xs text-red-400">{fieldErrors[k]}</p>;
  const noChannel = form && !form.telegram.enabled && !form.webhook.enabled && !form.email.enabled;

  return (
    <div>
      <PageHeader
        icon={<Bell size={20} />}
        title="Ogohlantirishlar"
        description="Muammo bo'lganda Telegram, webhook yoki e-pochta orqali xabar oling. Har bir muammo bir marta yuboriladi, davom etsa 6 soatda bir eslatiladi, hal bo'lganda esa xabar keladi."
        action={
          <div className="flex items-center gap-2">
            <Button variant="secondary" icon={<Send size={14} />} onClick={sendTest} disabled={busy !== "" || dirty}>
              Sinov xabari
            </Button>
            <Button variant="secondary" icon={<BellRing size={14} />} onClick={checkNow} disabled={busy !== ""}>
              Hozir tekshirish
            </Button>
          </div>
        }
      />

      {!form ? (
        <ListSkeleton rows={4} />
      ) : (
        <>
          <Card className="mb-6 p-5">
            <h3 className="mb-4 flex items-center gap-2 text-sm font-semibold text-white">
              <Siren size={16} className="text-indigo-400" />
              Holat
            </h3>
            <ul className="divide-y divide-white/[0.05]">
              {status.map((c) => (
                <li key={c.key} className="flex items-start justify-between gap-4 py-2.5 text-sm">
                  <div>
                    <div className="text-neutral-200">{conditionLabels[c.key] ?? c.key}</div>
                    {c.firing && c.detail && <div className="mt-0.5 text-xs text-neutral-500">{c.detail}</div>}
                  </div>
                  <span
                    className={`shrink-0 rounded-full px-2.5 py-0.5 text-xs font-medium ${
                      !c.enabled
                        ? "bg-white/[0.06] text-neutral-500"
                        : c.firing
                          ? "bg-red-500/15 text-red-300"
                          : "bg-emerald-500/15 text-emerald-300"
                    }`}
                  >
                    {!c.enabled ? "o'chirilgan" : c.firing ? `muammo · ${formatUnix(c.since)}` : "normal"}
                  </span>
                </li>
              ))}
            </ul>
          </Card>

          {noChannel && (
            <div className="mb-6 rounded-lg border border-amber-500/20 bg-amber-500/10 px-4 py-3 text-sm text-amber-200">
              Hech bir kanal yoqilmagan: muammolar tarixda ko'rinadi, lekin hech kimga yuborilmaydi.
            </div>
          )}

          <Card className="mb-6 p-5">
            <h3 className="mb-1 text-sm font-semibold text-white">Nimalar haqida xabar berilsin</h3>
            <p className="mb-4 text-xs text-neutral-500">Chegara qiymatlariga yetganda (teng bo'lsa ham) ogohlantirish yuboriladi.</p>
            <div className="space-y-4">
              <Row title="Squid to'xtab qolsa" hint="Ketma-ket ikki tekshiruvda ishlamasa yuboriladi.">
                <Switch checked={form.squid_down.enabled} onChange={(v) => patch((f) => ({ ...f, squid_down: { enabled: v } }))} label="Squid to'xtab qolsa" />
              </Row>
              <Row title="Disk to'lib borsa" hint="Log va ma'lumotlar diskining eng to'lasi.">
                <Switch checked={form.disk_full.enabled} onChange={(v) => patch((f) => ({ ...f, disk_full: { ...f.disk_full, enabled: v } }))} label="Disk to'lib borsa" />
                <Num label="Chegara, %" value={form.disk_full.percent} onChange={(n) => patch((f) => ({ ...f, disk_full: { ...f.disk_full, percent: n } }))} />
                {err("disk_full.percent")}
              </Row>
              <SpikeRow
                title="Bloklashlar ko'paysa"
                what="bloklangan so'rov"
                value={form.denied_spike}
                onChange={(s) => patch((f) => ({ ...f, denied_spike: s }))}
                errors={{ threshold: fieldErrors["denied_spike.threshold"], window: fieldErrors["denied_spike.window_minutes"] }}
              />
              <SpikeRow
                title="Server xatolari ko'paysa"
                what="5xx xato"
                value={form.error_spike}
                onChange={(s) => patch((f) => ({ ...f, error_spike: s }))}
                errors={{ threshold: fieldErrors["error_spike.threshold"], window: fieldErrors["error_spike.window_minutes"] }}
              />
            </div>
          </Card>

          <Card className="mb-6 p-5">
            <h3 className="mb-4 text-sm font-semibold text-white">Kanallar</h3>

            <Channel title="Telegram" enabled={form.telegram.enabled} onToggle={(v) => patch((f) => ({ ...f, telegram: { ...f.telegram, enabled: v } }))}>
              <div className="grid gap-3 sm:grid-cols-2">
                <div>
                  <Label>Bot tokeni</Label>
                  <Input
                    type="password"
                    autoComplete="off"
                    value={form.telegramToken}
                    placeholder={form.telegram.token_set ? "saqlangan (o'zgartirish uchun yangisini kiriting)" : "123456789:AAE…"}
                    onChange={(e) => patch((f) => ({ ...f, telegramToken: e.target.value }))}
                  />
                  {err("telegram.token")}
                </div>
                <div>
                  <Label>Chat ID yoki @kanal</Label>
                  <Input value={form.telegram.chat_id} placeholder="-1001234567890" onChange={(e) => patch((f) => ({ ...f, telegram: { ...f.telegram, chat_id: e.target.value } }))} />
                  {err("telegram.chat_id")}
                </div>
              </div>
              <p className="mt-2 text-xs text-neutral-500">Token BotFather orqali olinadi. Bot xabar yuboradigan chatga a'zo bo'lishi kerak.</p>
            </Channel>

            <Channel title="Webhook (Slack, Discord, Mattermost…)" enabled={form.webhook.enabled} onToggle={(v) => patch((f) => ({ ...f, webhook: { ...f.webhook, enabled: v } }))}>
              <Label>Webhook manzili</Label>
              <Input
                type="password"
                autoComplete="off"
                value={form.webhookUrl}
                placeholder={form.webhook.url_set ? `saqlangan (${form.webhook.host}) — o'zgartirish uchun yangisini kiriting` : "https://hooks.example.com/…"}
                onChange={(e) => patch((f) => ({ ...f, webhookUrl: e.target.value }))}
              />
              {err("webhook.url")}
              <p className="mt-2 text-xs text-neutral-500">Manzil yashirin kalit bo'lishi mumkin, shuning uchun qayta ko'rsatilmaydi. Xavfsizlik uchun 127.0.0.1 va bulut metadata manzillariga yuborish taqiqlangan.</p>
            </Channel>

            <Channel title="E-pochta (SMTP)" enabled={form.email.enabled} onToggle={(v) => patch((f) => ({ ...f, email: { ...f.email, enabled: v } }))} last>
              <div className="grid gap-3 sm:grid-cols-3">
                <div className="sm:col-span-2">
                  <Label>SMTP server</Label>
                  <Input value={form.email.host} placeholder="smtp.example.com" onChange={(e) => patch((f) => ({ ...f, email: { ...f.email, host: e.target.value } }))} />
                  {err("email.host")}
                </div>
                <div>
                  <Label>Port</Label>
                  <Input type="number" value={form.email.port} onChange={(e) => patch((f) => ({ ...f, email: { ...f.email, port: Number(e.target.value) } }))} />
                  {err("email.port")}
                </div>
                <div>
                  <Label>Login</Label>
                  <Input autoComplete="off" value={form.email.username} onChange={(e) => patch((f) => ({ ...f, email: { ...f.email, username: e.target.value } }))} />
                  {err("email.username")}
                </div>
                <div>
                  <Label>Parol</Label>
                  <Input
                    type="password"
                    autoComplete="new-password"
                    value={form.emailPassword}
                    placeholder={form.email.password_set ? "saqlangan" : ""}
                    onChange={(e) => patch((f) => ({ ...f, emailPassword: e.target.value }))}
                  />
                </div>
                <div>
                  <Label>Kimdan</Label>
                  <Input value={form.email.from} placeholder="squid@example.com" onChange={(e) => patch((f) => ({ ...f, email: { ...f.email, from: e.target.value } }))} />
                  {err("email.from")}
                </div>
                <div className="sm:col-span-3">
                  <Label>Kimga (vergul bilan ajrating)</Label>
                  <Input value={form.emailTo} placeholder="admin@example.com, it@example.com" onChange={(e) => patch((f) => ({ ...f, emailTo: e.target.value }))} />
                  {err("email.to")}
                </div>
              </div>
              <p className="mt-2 text-xs text-neutral-500">Port 465 da ulanish darhol shifrlanadi, boshqa portlarda server taklif qilsa STARTTLS ishlatiladi.</p>
            </Channel>

            <div className="mt-5 flex items-center justify-end gap-3">
              {dirty && <span className="text-xs text-neutral-500">Saqlanmagan o'zgarishlar bor — sinov xabari saqlangandan keyin yuboriladi</span>}
              <Button onClick={save} disabled={busy !== "" || !dirty} icon={<CheckCircle2 size={15} />}>
                Saqlash
              </Button>
            </div>
          </Card>

          <Card className="p-5">
            <h3 className="mb-4 text-sm font-semibold text-white">Tarix</h3>
            {events.length === 0 ? (
              <p className="py-6 text-center text-xs text-neutral-500">Hali hodisa bo'lmagan</p>
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full text-left text-xs">
                  <thead className="text-neutral-500">
                    <tr>
                      <th className="py-1.5 pr-4 font-medium">Vaqt</th>
                      <th className="py-1.5 pr-4 font-medium">Turi</th>
                      <th className="py-1.5 pr-4 font-medium">Xabar</th>
                      <th className="py-1.5 font-medium">Yuborilishi</th>
                    </tr>
                  </thead>
                  <tbody className="text-neutral-300">
                    {events.map((e) => (
                      <tr key={e.id} className="border-t border-white/[0.05] align-top">
                        <td className="whitespace-nowrap py-2 pr-4 text-neutral-400">{formatUnix(e.time)}</td>
                        <td className="py-2 pr-4">
                          <span className={`rounded-full px-2 py-0.5 ${kindStyle[e.kind]?.cls ?? ""}`}>{kindStyle[e.kind]?.label ?? e.kind}</span>
                        </td>
                        <td className="py-2 pr-4">{e.message}</td>
                        <td className="py-2 text-neutral-400">{e.delivery}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </Card>
        </>
      )}
    </div>
  );
}

// ---------------------------------------------------------------- pieces

function Row({ title, hint, children }: { title: string; hint?: string; children: ReactNode }) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-3">
      <div>
        <div className="text-sm text-neutral-200">{title}</div>
        {hint && <div className="text-xs text-neutral-500">{hint}</div>}
      </div>
      <div className="flex items-center gap-4">{children}</div>
    </div>
  );
}

function Switch({ checked, onChange, label }: { checked: boolean; onChange: (v: boolean) => void; label: string }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      onClick={() => onChange(!checked)}
      className={`relative h-6 w-11 shrink-0 rounded-full transition-colors ${checked ? "bg-indigo-500" : "bg-white/[0.12]"}`}
    >
      <span className={`absolute top-0.5 h-5 w-5 rounded-full bg-white transition-all ${checked ? "left-[22px]" : "left-0.5"}`} />
    </button>
  );
}

function Num({ label, value, onChange, width = "w-24" }: { label: string; value: number; onChange: (n: number) => void; width?: string }) {
  return (
    <label className="flex items-center gap-2 whitespace-nowrap text-xs text-neutral-400">
      {label}
      <Input type="number" className={width} value={Number.isFinite(value) ? value : ""} onChange={(e) => onChange(Number(e.target.value))} />
    </label>
  );
}

function SpikeRow({
  title,
  what,
  value,
  onChange,
  errors,
}: {
  title: string;
  what: string;
  value: Spike;
  onChange: (s: Spike) => void;
  errors: { threshold?: string; window?: string };
}) {
  return (
    <div>
      <Row title={title} hint={`Belgilangan vaqt ichida ${what} soni chegaraga yetsa.`}>
        <Switch checked={value.enabled} onChange={(v) => onChange({ ...value, enabled: v })} label={title} />
        <Num label="Soni" value={value.threshold} onChange={(n) => onChange({ ...value, threshold: n })} />
        <Num label="Daqiqa" value={value.window_minutes} onChange={(n) => onChange({ ...value, window_minutes: n })} width="w-20" />
      </Row>
      {(errors.threshold || errors.window) && <p className="mt-1 text-right text-xs text-red-400">{errors.threshold ?? errors.window}</p>}
    </div>
  );
}

function Channel({
  title,
  enabled,
  onToggle,
  children,
  last,
}: {
  title: string;
  enabled: boolean;
  onToggle: (v: boolean) => void;
  children: ReactNode;
  last?: boolean;
}) {
  return (
    <div className={`py-4 ${last ? "" : "border-b border-white/[0.06]"} first:pt-0`}>
      <div className="mb-3 flex items-center justify-between">
        <span className="text-sm font-medium text-neutral-200">{title}</span>
        <Switch checked={enabled} onChange={onToggle} label={title} />
      </div>
      <div className={enabled ? "" : "opacity-60"}>{children}</div>
    </div>
  );
}
