import { Download, Gauge, Plus, Trash2 } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { useConfirm } from "../components/ConfirmDialog";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";
import { CheckList } from "../components/ui/CheckList";
import { EmptyState } from "../components/ui/EmptyState";
import { Input, Label, Select } from "../components/ui/Input";
import { Modal } from "../components/ui/Modal";
import { PageHeader } from "../components/ui/PageHeader";
import { ListSkeleton } from "../components/ui/Skeleton";
import { api, errorMessage } from "../lib/api";
import { notifyResult, type ReloadResult } from "../lib/reload";
import { useToast } from "../lib/toast";
import {
  describeWho,
  type IPGroup,
  type LimitKind,
  type LimitRule,
  type LimitSpec,
  scopeLabels,
  type SpeedScope,
  type UserGroup,
} from "../lib/userpolicy";

interface Lookups {
  users: string[];
  userGroups: UserGroup[];
  ipGroups: IPGroup[];
}

const emptySpec = (): LimitSpec => ({ everyone: false, users: [], user_groups: [], ip_groups: [] });

function summary(r: LimitRule): string {
  const s = r.spec;
  if (r.kind === "speed") {
    const rate = `${s.rate_kbps} KB/s`;
    return `${rate} · ${s.scope ? scopeLabels[s.scope].toLowerCase() : ""}`;
  }
  return `eng ko'pi bilan ${s.max_mb} MB`;
}

export default function Limits() {
  const [rules, setRules] = useState<LimitRule[]>([]);
  const [lookups, setLookups] = useState<Lookups>({ users: [], userGroups: [], ipGroups: [] });
  const [loading, setLoading] = useState(true);
  const [editing, setEditing] = useState<LimitRule | "new" | null>(null);
  const toast = useToast();
  const confirm = useConfirm();

  const load = useCallback(async () => {
    const [l, u, g, ip] = await Promise.all([
      api.get<{ limits: LimitRule[] | null }>("/squid/limits"),
      api.get<{ users: { username: string }[] | null }>("/squid/proxy-users"),
      api.get<{ groups: UserGroup[] | null }>("/squid/user-groups"),
      api.get<{ groups: IPGroup[] | null }>("/squid/groups"),
    ]);
    setRules(l.data.limits ?? []);
    setLookups({
      users: (u.data.users ?? []).map((x) => x.username),
      userGroups: g.data.groups ?? [],
      ipGroups: ip.data.groups ?? [],
    });
  }, []);

  useEffect(() => {
    load()
      .catch((err) => toast.error(errorMessage(err, "Yuklab bo'lmadi")))
      .finally(() => setLoading(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [load]);

  async function toggle(r: LimitRule) {
    try {
      const res = await api.put<ReloadResult>(`/squid/limits/${r.id}`, { ...r, enabled: !r.enabled });
      notifyResult(toast, res.data, r.enabled ? `${r.name} o'chirildi` : `${r.name} yoqildi`);
      await load();
    } catch (err) {
      toast.error(errorMessage(err, "O'zgartirib bo'lmadi"));
    }
  }

  async function remove(r: LimitRule) {
    const ok = await confirm({ title: "Cheklovni o'chirish", description: `"${r.name}" o'chiriladi va Squid qayta yuklanadi.` });
    if (!ok) return;
    try {
      const res = await api.delete<ReloadResult>(`/squid/limits/${r.id}`);
      notifyResult(toast, res.data, `${r.name} o'chirildi`);
      await load();
    } catch (err) {
      toast.error(errorMessage(err, "O'chirib bo'lmadi"));
    }
  }

  return (
    <div>
      <PageHeader
        icon={<Gauge size={19} />}
        title="Cheklovlar"
        description="Kimdir uchun tezlikni pasaytirish yoki bitta faylning eng katta hajmini cheklash. Bir nechta qoida mos kelsa, ro'yxatda birinchisi ishlaydi (tezlik uchun ham, hajm uchun ham)."
        action={
          <Button icon={<Plus size={16} />} onClick={() => setEditing("new")}>
            Yangi cheklov
          </Button>
        }
      />

      <Card>
        {loading ? (
          <ListSkeleton />
        ) : rules.length === 0 ? (
          <EmptyState icon={<Gauge size={22} />} title="Hali cheklov yo'q" description="Masalan: xodimlarga har biriga 512 KB/s, yoki hammaga bitta fayl 700 MB dan oshmasin." />
        ) : (
          <ul className="divide-y divide-white/[0.06]">
            {rules.map((r) => (
              <li key={r.id} className={`flex flex-wrap items-center justify-between gap-3 px-5 py-3.5 text-sm ${r.enabled ? "" : "opacity-60"}`}>
                <div className="min-w-0">
                  <div className="flex items-center gap-2 text-neutral-200">
                    {r.kind === "speed" ? <Gauge size={14} className="text-indigo-400" /> : <Download size={14} className="text-amber-400" />}
                    <span className="font-medium">{r.name}</span>
                    {!r.enabled && <span className="rounded-full bg-white/[0.08] px-2 py-0.5 text-[11px] text-neutral-400">o'chiq</span>}
                  </div>
                  <div className="mt-1 text-xs text-neutral-500">
                    {summary(r)} · kimga: {describeWho(r.spec, lookups.userGroups, lookups.ipGroups)}
                  </div>
                </div>
                <div className="flex shrink-0 items-center gap-4 text-xs font-medium">
                  <button type="button" className="text-neutral-400 hover:text-neutral-100" onClick={() => toggle(r)}>
                    {r.enabled ? "O'chirib qo'yish" : "Yoqish"}
                  </button>
                  <button type="button" className="text-indigo-300 hover:text-indigo-200" onClick={() => setEditing(r)}>
                    O'zgartirish
                  </button>
                  <button type="button" aria-label={`${r.name} cheklovini o'chirish`} className="text-red-400/80 hover:text-red-400" onClick={() => remove(r)}>
                    <Trash2 size={14} />
                  </button>
                </div>
              </li>
            ))}
          </ul>
        )}
      </Card>

      {editing && <LimitDialog rule={editing === "new" ? null : editing} lookups={lookups} onClose={() => setEditing(null)} onSaved={() => load().catch(() => {})} />}
    </div>
  );
}

function LimitDialog({ rule, lookups, onClose, onSaved }: { rule: LimitRule | null; lookups: Lookups; onClose: () => void; onSaved: () => void }) {
  const toast = useToast();
  const [name, setName] = useState(rule?.name ?? "");
  const [kind, setKind] = useState<LimitKind>(rule?.kind ?? "speed");
  const [spec, setSpec] = useState<LimitSpec>(rule ? { ...emptySpec(), ...rule.spec, users: rule.spec.users ?? [], user_groups: rule.spec.user_groups ?? [], ip_groups: rule.spec.ip_groups ?? [] } : emptySpec());
  const [scope, setScope] = useState<SpeedScope>(rule?.spec.scope ?? "each_user");
  const [rate, setRate] = useState(String(rule?.spec.rate_kbps ?? ""));
  const [burst, setBurst] = useState(String(rule?.spec.burst_kb ?? ""));
  const [maxMB, setMaxMB] = useState(String(rule?.spec.max_mb ?? ""));
  const [busy, setBusy] = useState(false);
  const [fieldError, setFieldError] = useState<string | null>(null);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setFieldError(null);
    const body: LimitRule = {
      id: rule?.id ?? 0,
      name,
      kind,
      enabled: rule?.enabled ?? true,
      spec:
        kind === "speed"
          ? { ...spec, scope, rate_kbps: Number(rate), burst_kb: burst ? Number(burst) : 0, max_mb: undefined }
          : { ...spec, scope: undefined, rate_kbps: undefined, burst_kb: undefined, max_mb: Number(maxMB) },
    };
    setBusy(true);
    try {
      const res = rule
        ? await api.put<ReloadResult>(`/squid/limits/${rule.id}`, body)
        : await api.post<ReloadResult>("/squid/limits", body);
      notifyResult(toast, res.data, `${name} saqlandi`);
      onSaved();
      onClose();
    } catch (err) {
      setFieldError(errorMessage(err, "Saqlab bo'lmadi"));
    } finally {
      setBusy(false);
    }
  }

  const who = (patch: Partial<LimitSpec>) => setSpec((s) => ({ ...s, ...patch }));

  return (
    <Modal title={rule ? "Cheklovni o'zgartirish" : "Yangi cheklov"} onClose={onClose} wide>
      <form onSubmit={save} className="space-y-4">
        <div className="grid gap-3 sm:grid-cols-2">
          <div>
            <Label>Nomi</Label>
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="masalan: xodimlar tezligi" />
          </div>
          <div>
            <Label>Turi</Label>
            <Select value={kind} onChange={(e) => setKind(e.target.value as LimitKind)} disabled={!!rule}>
              <option value="speed" className="bg-[#14171f]">Tezlik (KB/s)</option>
              <option value="download" className="bg-[#14171f]">Bitta fayl hajmi (MB)</option>
            </Select>
          </div>
        </div>

        {kind === "speed" ? (
          <div className="grid gap-3 sm:grid-cols-3">
            <div className="sm:col-span-3">
              <Label>Tezlik kimga qanday taqsimlanadi</Label>
              <Select value={scope} onChange={(e) => setScope(e.target.value as SpeedScope)}>
                {(Object.keys(scopeLabels) as SpeedScope[]).map((k) => (
                  <option key={k} value={k} className="bg-[#14171f]">
                    {scopeLabels[k]}
                  </option>
                ))}
              </Select>
            </div>
            <div>
              <Label>Tezlik (KB/s)</Label>
              <Input type="number" min={1} value={rate} onChange={(e) => setRate(e.target.value)} placeholder="512" />
            </div>
            <div>
              <Label>Dastlabki “portlash” (KB)</Label>
              <Input type="number" min={0} value={burst} onChange={(e) => setBurst(e.target.value)} placeholder={rate ? String(Number(rate) * 2) : "2 soniyalik"} />
            </div>
            <p className="self-end pb-2 text-xs text-neutral-500">Boshida shuncha tez yuklanadi, keyin yuqoridagi tezlikka tushadi.</p>
          </div>
        ) : (
          <div>
            <Label>Bitta fayl eng ko'pi bilan (MB)</Label>
            <Input type="number" min={1} value={maxMB} onChange={(e) => setMaxMB(e.target.value)} placeholder="700" className="max-w-40" />
            <p className="mt-1 text-xs text-neutral-500">Bundan katta javob yuklab olinmaydi: foydalanuvchi xato sahifasini ko'radi.</p>
          </div>
        )}

        <div className="space-y-3 rounded-xl border border-white/[0.06] p-3">
          <label className="flex items-center gap-2 text-sm text-neutral-200">
            <input type="checkbox" className="accent-indigo-500" checked={spec.everyone} onChange={(e) => who({ everyone: e.target.checked })} />
            Hamma uchun
          </label>
          {!spec.everyone && (
            <>
              <div>
                <Label>Foydalanuvchilar</Label>
                <CheckList options={lookups.users.map((u) => ({ value: u, label: u }))} value={spec.users ?? []} onChange={(v) => who({ users: v })} empty="Proxy foydalanuvchisi yo'q" />
              </div>
              <div>
                <Label>Foydalanuvchi guruhlari</Label>
                <CheckList options={lookups.userGroups.map((g) => ({ value: g.id, label: g.name }))} value={spec.user_groups ?? []} onChange={(v) => who({ user_groups: v })} empty="Guruh yo'q (Foydalanuvchilar sahifasida yarating)" />
              </div>
              <div>
                <Label>IP guruhlari</Label>
                <CheckList options={lookups.ipGroups.map((g) => ({ value: g.id, label: g.name }))} value={spec.ip_groups ?? []} onChange={(v) => who({ ip_groups: v })} empty="IP guruhi yo'q" />
              </div>
            </>
          )}
        </div>

        {fieldError && <p className="rounded-lg border border-red-500/20 bg-red-500/10 px-3 py-2 text-sm text-red-300">{fieldError}</p>}

        <div className="flex justify-end gap-2">
          <Button type="button" variant="ghost" onClick={onClose}>
            Bekor qilish
          </Button>
          <Button type="submit" disabled={busy}>
            Saqlash
          </Button>
        </div>
      </form>
    </Modal>
  );
}
