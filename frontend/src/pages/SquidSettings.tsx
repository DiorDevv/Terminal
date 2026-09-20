import {
  AlertTriangle,
  Plus,
  RotateCcw,
  SlidersHorizontal,
  Trash2,
  Undo2,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";
import { Input, Select } from "../components/ui/Input";
import { PageHeader } from "../components/ui/PageHeader";
import { Skeleton } from "../components/ui/Skeleton";
import { api, errorMessage } from "../lib/api";
import { useAuth } from "../lib/auth";
import { groupMeta, settingMeta, sourceLabels } from "../lib/settingsMeta";
import { useToast } from "../lib/toast";

interface Setting {
  key: string;
  group: string;
  kind: "text" | "select" | "list" | "toggle";
  options?: string[];
  placeholder?: string;
  default: string;
  required: boolean;
  multi: boolean;
  restart: boolean;
  values: string[];
  source: "panel" | "squid.conf" | "default";
}

interface Change {
  key: string;
  from: string[];
  to: string[];
  restart: boolean;
}

interface Preview {
  changes: Change[];
  restart_required: boolean;
}

const same = (a: string[], b: string[]) => a.length === b.length && a.every((v, i) => v === b[i]);
const clean = (v: string[]) => v.map((s) => s.trim()).filter(Boolean);

function Switch({ on, disabled, onClick }: { on: boolean; disabled?: boolean; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      role="switch"
      aria-checked={on}
      className={`relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition-colors disabled:opacity-50 ${
        on ? "bg-emerald-500" : "bg-white/10"
      }`}
    >
      <span
        className={`inline-block h-4 w-4 transform rounded-full bg-white shadow transition-transform ${
          on ? "translate-x-6" : "translate-x-1"
        }`}
      />
    </button>
  );
}

function ValueList({ values }: { values: string[] }) {
  if (values.length === 0) return <span className="text-neutral-500">yo'q</span>;
  return (
    <span className="font-mono">
      {values.map((v, i) => (
        <span key={i} className="block">
          {v}
        </span>
      ))}
    </span>
  );
}

export default function SquidSettings() {
  const { can } = useAuth();
  const editable = can("admin");
  const toast = useToast();

  const [settings, setSettings] = useState<Setting[] | null>(null);
  const [groups, setGroups] = useState<string[]>([]);
  const [draft, setDraft] = useState<Record<string, string[]>>({});
  const [resets, setResets] = useState<Set<string>>(new Set());
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [preview, setPreview] = useState<Preview | null>(null);
  const [busy, setBusy] = useState(false);
  const [restartPending, setRestartPending] = useState(false);
  const [restarting, setRestarting] = useState(false);

  const load = useCallback(async () => {
    const [res, svc] = await Promise.all([
      api.get<{ settings: Setting[]; groups: string[] }>("/squid/settings"),
      api.get<{ restart_pending: boolean }>("/squid/service"),
    ]);
    setSettings(res.data.settings);
    setGroups(res.data.groups);
    setDraft(Object.fromEntries(res.data.settings.map((s) => [s.key, [...s.values]])));
    setResets(new Set());
    setErrors({});
    setRestartPending(svc.data.restart_pending);
  }, []);

  useEffect(() => {
    load().catch((err) => toast.error(errorMessage(err, "Sozlamalarni yuklab bo'lmadi")));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const byKey = useMemo(() => new Map((settings ?? []).map((s) => [s.key, s])), [settings]);

  // The update to send: changed values, plus the fields staged for reset.
  const update = useMemo(() => {
    const values: Record<string, string[]> = {};
    for (const s of settings ?? []) {
      if (resets.has(s.key)) continue;
      const now = clean(draft[s.key] ?? []);
      if (!same(now, s.values)) values[s.key] = now;
    }
    return { values, reset: [...resets] };
  }, [settings, draft, resets]);

  const dirtyCount = Object.keys(update.values).length + update.reset.length;

  function setField(key: string, values: string[]) {
    setDraft((d) => ({ ...d, [key]: values }));
    setErrors((e) => {
      if (!e[key]) return e;
      const { [key]: _drop, ...rest } = e;
      return rest;
    });
  }

  function toggleReset(key: string) {
    setResets((r) => {
      const next = new Set(r);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }

  function discard() {
    if (!settings) return;
    setDraft(Object.fromEntries(settings.map((s) => [s.key, [...s.values]])));
    setResets(new Set());
    setErrors({});
  }

  async function review() {
    setBusy(true);
    setErrors({});
    try {
      const res = await api.put<Preview>("/squid/settings?dry_run=1", update);
      setPreview(res.data);
    } catch (err) {
      const data = (err as { response?: { data?: { error?: string; fields?: Record<string, string> } } }).response?.data;
      if (data?.fields) {
        setErrors(data.fields);
        toast.error("Ba'zi maydonlar noto'g'ri to'ldirilgan");
      } else {
        toast.error(data?.error ?? "Tekshirib bo'lmadi");
      }
    } finally {
      setBusy(false);
    }
  }

  async function apply() {
    setBusy(true);
    try {
      const res = await api.put<{
        restart_required: boolean;
        reloaded?: boolean;
        reload_error?: string;
        changed: string[];
      }>("/squid/settings", update);
      setPreview(null);
      await load();

      if (res.data.reloaded === false) {
        toast.error(res.data.reload_error ?? "Saqlandi, lekin Squid qayta yuklanmadi");
      } else if (res.data.restart_required) {
        toast.success("Saqlandi. O'zgarishlar kuchga kirishi uchun Squid'ni qayta ishga tushiring.");
      } else if (res.data.changed.length === 0) {
        toast.success("O'zgarish yo'q edi");
      } else {
        toast.success("Sozlamalar saqlandi va Squid'ga qo'llandi");
      }
    } catch (err) {
      setPreview(null);
      toast.error(errorMessage(err, "Saqlab bo'lmadi"));
    } finally {
      setBusy(false);
    }
  }

  async function restart() {
    setRestarting(true);
    try {
      await api.post("/squid/settings/restart");
      toast.success("Squid qayta ishga tushirildi va yangi sozlamalar bilan ishlayapti");
    } catch (err) {
      toast.error(errorMessage(err, "Qayta ishga tushirib bo'lmadi"));
    } finally {
      setRestarting(false);
      await load().catch(() => {});
    }
  }

  function renderField(s: Setting) {
    const values = draft[s.key] ?? [];
    const disabled = !editable || busy || resets.has(s.key);
    const invalid = !!errors[s.key];
    const inputClass = invalid ? "!border-red-500/60" : "";

    switch (s.kind) {
      case "toggle":
        return (
          <Switch
            on={values.length > 0}
            disabled={disabled}
            onClick={() => setField(s.key, values.length > 0 ? [] : ["allow all"])}
          />
        );

      case "select":
        return (
          <Select
            value={values[0] ?? ""}
            disabled={disabled}
            onChange={(e) => setField(s.key, e.target.value ? [e.target.value] : [])}
            className={`!w-56 ${inputClass}`}
          >
            <option value="">Standart ({s.default})</option>
            {s.options?.map((o) => (
              <option key={o} value={o}>
                {o}
              </option>
            ))}
          </Select>
        );

      case "list": {
        const rows = values.length > 0 ? values : [""];
        return (
          <div className="grid gap-2">
            {rows.map((v, i) => (
              <div key={i} className="flex gap-2">
                <Input
                  value={v}
                  disabled={disabled}
                  placeholder={s.placeholder}
                  className={`font-mono ${inputClass}`}
                  onChange={(e) => setField(s.key, rows.map((r, j) => (j === i ? e.target.value : r)))}
                />
                {editable && (
                  <button
                    type="button"
                    disabled={disabled || (rows.length === 1 && s.required)}
                    onClick={() => setField(s.key, rows.filter((_, j) => j !== i))}
                    className="shrink-0 rounded-lg px-2 text-neutral-500 transition-colors hover:bg-white/[0.06] hover:text-red-400 disabled:opacity-30"
                    aria-label="Qatorni olib tashlash"
                  >
                    <Trash2 size={15} />
                  </button>
                )}
              </div>
            ))}
            {editable && s.multi && (
              <Button
                type="button"
                variant="ghost"
                disabled={disabled}
                onClick={() => setField(s.key, [...values, ""])}
                icon={<Plus size={13} />}
                className="w-fit !px-2.5 !py-1.5 text-xs"
              >
                Qator qo'shish
              </Button>
            )}
          </div>
        );
      }

      default:
        return (
          <Input
            value={values[0] ?? ""}
            disabled={disabled}
            placeholder={s.placeholder ?? `Standart: ${s.default}`}
            className={`font-mono ${inputClass}`}
            onChange={(e) => setField(s.key, e.target.value ? [e.target.value] : [])}
          />
        );
    }
  }

  return (
    <div className={dirtyCount > 0 ? "pb-24" : ""}>
      <PageHeader
        icon={<SlidersHorizontal size={19} />}
        title="Squid sozlamalari"
        description="Asosiy sozlamalar shakl orqali. Har bir o'zgarish saqlashdan oldin tekshiriladi, tarixga yoziladi va Squid buzilsa avtomatik bekor qilinadi."
      />

      {!editable && (
        <div className="mb-5 rounded-lg border border-amber-500/20 bg-amber-500/10 px-4 py-2.5 text-sm text-amber-300">
          Faqat o'qish rejimi: sozlamalarni faqat administrator o'zgartira oladi.
        </div>
      )}

      {restartPending && (
        <div className="mb-5 flex flex-wrap items-center justify-between gap-3 rounded-xl border border-amber-500/25 bg-amber-500/10 px-4 py-3">
          <div className="flex items-start gap-2.5 text-sm text-amber-200">
            <AlertTriangle size={16} className="mt-0.5 shrink-0" />
            <span>
              Saqlangan o'zgarish Squid'ni <b>qayta ishga tushirishni</b> talab qiladi. Hozircha eski
              sozlamalar bilan ishlayapti.
            </span>
          </div>
          {editable && (
            <Button onClick={restart} disabled={restarting} icon={<RotateCcw size={14} className={restarting ? "animate-spin" : ""} />}>
              {restarting ? "Qayta ishga tushirilmoqda…" : "Hozir qayta ishga tushirish"}
            </Button>
          )}
        </div>
      )}
      {restarting && (
        <p className="mb-5 text-xs text-neutral-500">
          Squid ochiq ulanishlar yopilishini kutadi, so'ng yangi sozlamalar bilan ishga tushadi va
          tekshiriladi. Ishlamasa, eski sozlamalar avtomatik tiklanadi. Bu bir necha soniya olishi mumkin.
        </p>
      )}

      {settings === null ? (
        <div className="grid gap-5">
          <Skeleton className="h-56 w-full" />
          <Skeleton className="h-40 w-full" />
        </div>
      ) : (
        <div className="grid gap-5">
          {groups.map((g) => (
            <Card key={g} className="overflow-hidden">
              <div className="border-b border-white/[0.06] px-6 py-4">
                <h3 className="text-sm font-semibold text-white">{groupMeta[g]?.label ?? g}</h3>
                <p className="mt-0.5 text-xs text-neutral-500">{groupMeta[g]?.description}</p>
              </div>
              <div className="divide-y divide-white/[0.05]">
                {settings
                  .filter((s) => s.group === g)
                  .map((s) => {
                    const meta = settingMeta[s.key];
                    const staged = resets.has(s.key);
                    const changed = !staged && !same(clean(draft[s.key] ?? []), s.values);
                    return (
                      <div key={s.key} className="grid gap-3 px-6 py-5 md:grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)] md:gap-8">
                        <div>
                          <div className="flex flex-wrap items-center gap-2">
                            <span className="text-sm font-medium text-neutral-100">{meta?.label ?? s.key}</span>
                            <code className="rounded bg-white/[0.05] px-1.5 py-0.5 text-[10px] text-neutral-500">{s.key}</code>
                            {s.restart && (
                              <span className="rounded-full bg-amber-500/15 px-2 py-0.5 text-[10px] font-medium text-amber-300">
                                qayta ishga tushirish kerak
                              </span>
                            )}
                            {changed && (
                              <span className="rounded-full bg-indigo-500/15 px-2 py-0.5 text-[10px] font-medium text-indigo-300">
                                o'zgardi
                              </span>
                            )}
                          </div>
                          <p className="mt-1.5 text-xs leading-relaxed text-neutral-500">{meta?.help}</p>
                          <p className="mt-1.5 text-[11px] text-neutral-600">
                            Manba: {sourceLabels[s.source]} · standart: {s.default}
                          </p>
                        </div>

                        <div className={staged ? "opacity-50" : ""}>
                          {renderField(s)}
                          {staged && (
                            <p className="mt-2 text-xs text-indigo-300">
                              Saqlanganda squid.conf'dagi asl holatiga qaytariladi.
                            </p>
                          )}
                          {errors[s.key] && <p className="mt-2 text-xs text-red-400">{errors[s.key]}</p>}
                          {editable && s.source === "panel" && (
                            <button
                              type="button"
                              onClick={() => toggleReset(s.key)}
                              disabled={busy}
                              className="mt-2.5 inline-flex items-center gap-1.5 text-xs font-medium text-neutral-500 transition-colors hover:text-neutral-200"
                            >
                              {staged ? <Undo2 size={12} /> : <RotateCcw size={12} />}
                              {staged ? "Bekor qilish" : "Asl holatiga qaytarish"}
                            </button>
                          )}
                        </div>
                      </div>
                    );
                  })}
              </div>
            </Card>
          ))}
        </div>
      )}

      {editable && dirtyCount > 0 && (
        <div className="fixed inset-x-0 bottom-0 z-40 border-t border-white/[0.08] bg-[#0b0c11]/90 backdrop-blur-xl">
          <div className="mx-auto flex max-w-5xl items-center justify-between gap-3 px-8 py-3.5">
            <span className="text-sm text-neutral-300">{dirtyCount} ta o'zgarish saqlanmagan</span>
            <div className="flex gap-2">
              <Button variant="ghost" onClick={discard} disabled={busy}>
                Bekor qilish
              </Button>
              <Button onClick={review} disabled={busy}>
                {busy ? "Tekshirilmoqda…" : "Ko'rib chiqish va qo'llash"}
              </Button>
            </div>
          </div>
        </div>
      )}

      {preview && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm">
          <div className="w-full max-w-xl rounded-2xl border border-white/[0.08] bg-[#14171f] p-6 shadow-[0_20px_60px_-15px_rgba(0,0,0,0.9)]">
            <h3 className="font-semibold text-white">Quyidagi o'zgarishlar qo'llanadi</h3>
            <p className="mt-1 text-xs text-neutral-500">
              Squid'ning o'z tekshiruvidan o'tdi. Saqlagandan keyin tarixdan qaytarish mumkin.
            </p>

            {preview.changes.length === 0 ? (
              <p className="my-5 text-sm text-neutral-400">Amaldagi qiymatlardan farq yo'q.</p>
            ) : (
              <ul className="my-5 max-h-[45vh] divide-y divide-white/[0.06] overflow-auto rounded-lg border border-white/[0.06]">
                {preview.changes.map((c) => (
                  <li key={c.key} className="grid gap-1.5 px-4 py-3 text-xs">
                    <div className="flex items-center gap-2 text-sm font-medium text-neutral-200">
                      {settingMeta[c.key]?.label ?? c.key}
                      {byKey.get(c.key)?.restart && (
                        <span className="rounded-full bg-amber-500/15 px-2 py-0.5 text-[10px] font-medium text-amber-300">
                          qayta ishga tushirish
                        </span>
                      )}
                    </div>
                    <div className="grid grid-cols-[3rem_1fr] gap-x-2 text-neutral-400">
                      <span className="text-red-400/80">avval</span>
                      <ValueList values={c.from} />
                      <span className="text-emerald-400/80">keyin</span>
                      <ValueList values={c.to} />
                    </div>
                  </li>
                ))}
              </ul>
            )}

            {preview.restart_required && (
              <p className="mb-4 flex items-start gap-2 rounded-lg border border-amber-500/20 bg-amber-500/10 px-3 py-2 text-xs text-amber-200">
                <AlertTriangle size={14} className="mt-0.5 shrink-0" />
                Bu o'zgarish faqat Squid qayta ishga tushirilgandan keyin kuchga kiradi. Saqlagandan so'ng
                sahifada "Hozir qayta ishga tushirish" tugmasi paydo bo'ladi.
              </p>
            )}

            <div className="flex justify-end gap-2">
              <Button variant="ghost" onClick={() => setPreview(null)} disabled={busy}>
                Orqaga
              </Button>
              <Button onClick={apply} disabled={busy || preview.changes.length === 0}>
                {busy ? "Qo'llanmoqda…" : "Saqlash va qo'llash"}
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
