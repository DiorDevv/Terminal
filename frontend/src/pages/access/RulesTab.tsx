import { ArrowDown, ArrowUp, ListOrdered, Plus, Trash2 } from "lucide-react";
import { useState } from "react";
import { useConfirm } from "../../components/ConfirmDialog";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { EmptyState } from "../../components/ui/EmptyState";
import { Input, Label, Select } from "../../components/ui/Input";
import { api, errorMessage } from "../../lib/api";
import type { Acl, Rule } from "../../lib/access";
import { notifyResult, type ReloadResult } from "../../lib/reload";
import { useToast } from "../../lib/toast";
import Templates from "./Templates";

function ActionBadge({ action }: { action: "allow" | "deny" }) {
  return action === "allow" ? (
    <span className="rounded-md bg-emerald-500/15 px-2 py-0.5 text-[11px] font-semibold uppercase tracking-wide text-emerald-300">
      Ruxsat
    </span>
  ) : (
    <span className="rounded-md bg-red-500/15 px-2 py-0.5 text-[11px] font-semibold uppercase tracking-wide text-red-300">
      Rad etish
    </span>
  );
}

function Switch({ on, onClick, disabled }: { on: boolean; onClick: () => void; disabled?: boolean }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={on}
      onClick={onClick}
      disabled={disabled}
      className={`relative inline-flex h-5 w-9 shrink-0 items-center rounded-full transition-colors disabled:opacity-50 ${
        on ? "bg-emerald-500" : "bg-white/10"
      }`}
    >
      <span
        className={`inline-block h-3.5 w-3.5 transform rounded-full bg-white shadow transition-transform ${
          on ? "translate-x-[18px]" : "translate-x-[3px]"
        }`}
      />
    </button>
  );
}

interface TermDraft {
  acl_id: string;
  negate: boolean;
}

export default function RulesTab({
  rules,
  acls,
  reload,
}: {
  rules: Rule[];
  acls: Acl[];
  reload: () => Promise<void>;
}) {
  const toast = useToast();
  const confirm = useConfirm();
  const [busy, setBusy] = useState(false);
  const [action, setAction] = useState<"allow" | "deny">("deny");
  const [terms, setTerms] = useState<TermDraft[]>([{ acl_id: "", negate: false }]);
  const [comment, setComment] = useState("");
  const [position, setPosition] = useState("");

  async function run<T extends ReloadResult>(fn: () => Promise<{ data: T }>, ok: string, fail: string) {
    setBusy(true);
    try {
      const res = await fn();
      notifyResult(toast, res.data, ok);
      await reload();
      return true;
    } catch (err) {
      toast.error(errorMessage(err, fail));
      return false;
    } finally {
      setBusy(false);
    }
  }

  const move = (r: Rule, delta: -1 | 1) => {
    const order = rules.map((x) => x.id);
    const i = order.indexOf(r.id);
    const j = i + delta;
    if (j < 0 || j >= order.length) return;
    [order[i], order[j]] = [order[j], order[i]];
    return run(() => api.put("/squid/access/order", { ids: order }), "Tartib o'zgartirildi", "Tartibni o'zgartirib bo'lmadi");
  };

  const toggle = (r: Rule) =>
    run(
      () => api.put(`/squid/access/rules/${r.id}`, { enabled: !r.enabled }),
      r.enabled ? "Qoida o'chirib qo'yildi" : "Qoida yoqildi",
      "O'zgartirib bo'lmadi",
    );

  async function remove(r: Rule) {
    const ok = await confirm({
      title: "Qoidani o'chirish",
      description: `#${r.position} qoida o'chirilsinmi?${r.comment ? ` ("${r.comment}")` : ""}`,
    });
    if (!ok) return;
    await run(() => api.delete(`/squid/access/rules/${r.id}`), "Qoida o'chirildi", "O'chirib bo'lmadi");
  }

  async function create(e: React.FormEvent) {
    e.preventDefault();
    const chosen = terms.filter((t) => t.acl_id);
    if (chosen.length === 0) {
      toast.error("Kamida bitta shart tanlang");
      return;
    }
    const ok = await run(
      () =>
        api.post("/squid/access/rules", {
          action,
          comment: comment.trim(),
          position: position ? Number(position) : 0,
          terms: chosen.map((t) => ({ acl_id: Number(t.acl_id), negate: t.negate })),
        }),
      "Qoida qo'shildi",
      "Qoidani qo'shib bo'lmadi",
    );
    if (ok) {
      setTerms([{ acl_id: "", negate: false }]);
      setComment("");
      setPosition("");
    }
  }

  const usable = acls;

  return (
    <div className="grid gap-6">
      <Templates acls={acls} reload={reload} />

      <Card>
        <div className="flex items-center gap-2.5 border-b border-white/[0.06] px-5 py-4">
          <ListOrdered size={16} className="text-indigo-400" />
          <div>
            <h3 className="text-sm font-semibold text-white">Qoidalar (yuqoridan pastga tekshiriladi)</h3>
            <p className="mt-0.5 text-xs text-neutral-500">
              Birinchi mos kelgan qoida hal qiladi. Ular vaqt cheklovlaridan keyin, Squid'ning asosiy ruxsatlaridan
              (localhost, LAN) oldin ishlaydi.
            </p>
          </div>
        </div>

        {rules.length === 0 ? (
          <EmptyState
            icon={<ListOrdered size={22} />}
            title="Hozircha qoida yo'q"
            description="Yuqoridagi shablonlardan foydalaning yoki pastdan yangi qoida qo'shing"
          />
        ) : (
          <ul className="divide-y divide-white/[0.06]">
            {rules.map((r, i) => (
              <li key={r.id} className={`flex flex-wrap items-center gap-3 px-5 py-3.5 ${r.enabled ? "" : "opacity-50"}`}>
                <span className="w-6 shrink-0 text-center font-mono text-xs text-neutral-500">{r.position}</span>
                <ActionBadge action={r.action} />
                <div className="min-w-0 flex-1 text-sm text-neutral-300">
                  <span className="text-neutral-500">agar </span>
                  {r.terms.map((t, k) => (
                    <span key={t.acl_id}>
                      {k > 0 && <span className="text-neutral-500"> va </span>}
                      {t.negate && <span className="text-amber-300">emas: </span>}
                      <code className="rounded bg-white/[0.06] px-1.5 py-0.5 text-[12px] text-neutral-100">{t.acl_name}</code>
                    </span>
                  ))}
                  {r.comment && <div className="mt-0.5 text-xs text-neutral-500">{r.comment}</div>}
                </div>
                <div className="flex items-center gap-1">
                  <button
                    type="button"
                    disabled={busy || i === 0}
                    onClick={() => move(r, -1)}
                    className="rounded-md p-1.5 text-neutral-500 hover:bg-white/[0.06] hover:text-neutral-100 disabled:opacity-30"
                    aria-label="Yuqoriga"
                  >
                    <ArrowUp size={15} />
                  </button>
                  <button
                    type="button"
                    disabled={busy || i === rules.length - 1}
                    onClick={() => move(r, 1)}
                    className="rounded-md p-1.5 text-neutral-500 hover:bg-white/[0.06] hover:text-neutral-100 disabled:opacity-30"
                    aria-label="Pastga"
                  >
                    <ArrowDown size={15} />
                  </button>
                  <Switch on={r.enabled} disabled={busy} onClick={() => toggle(r)} />
                  <button
                    type="button"
                    disabled={busy}
                    onClick={() => remove(r)}
                    className="ml-1 rounded-md p-1.5 text-red-400/80 hover:bg-red-500/10 hover:text-red-400 disabled:opacity-30"
                    aria-label="O'chirish"
                  >
                    <Trash2 size={15} />
                  </button>
                </div>
              </li>
            ))}
          </ul>
        )}
      </Card>

      <Card className="p-5">
        <h3 className="mb-4 text-sm font-semibold text-white">Yangi qoida</h3>
        {usable.length === 0 ? (
          <p className="text-sm text-neutral-500">Avval "Obyektlar" bo'limida kamida bitta ACL yarating.</p>
        ) : (
          <form onSubmit={create} className="grid gap-4">
            <div className="grid gap-4 sm:grid-cols-[180px_1fr]">
              <div>
                <Label>Amal</Label>
                <Select value={action} onChange={(e) => setAction(e.target.value as "allow" | "deny")}>
                  <option value="deny">Rad etish</option>
                  <option value="allow">Ruxsat berish</option>
                </Select>
              </div>
              <div>
                <Label>Shartlar (hammasi bajarilsa qoida ishlaydi)</Label>
                <div className="grid gap-2">
                  {terms.map((t, i) => (
                    <div key={i} className="flex flex-wrap items-center gap-2">
                      <Select
                        value={t.acl_id}
                        onChange={(e) => setTerms(terms.map((x, j) => (j === i ? { ...x, acl_id: e.target.value } : x)))}
                        className="!w-64"
                      >
                        <option value="">ACL tanlang…</option>
                        {usable.map((a) => (
                          <option key={a.id} value={a.id}>
                            {a.name} ({a.type})
                          </option>
                        ))}
                      </Select>
                      <label className="flex items-center gap-1.5 text-xs text-neutral-400">
                        <input
                          type="checkbox"
                          checked={t.negate}
                          onChange={(e) => setTerms(terms.map((x, j) => (j === i ? { ...x, negate: e.target.checked } : x)))}
                        />
                        emas (istisno)
                      </label>
                      {terms.length > 1 && (
                        <button
                          type="button"
                          onClick={() => setTerms(terms.filter((_, j) => j !== i))}
                          className="text-neutral-500 hover:text-red-400"
                          aria-label="Shartni olib tashlash"
                        >
                          <Trash2 size={14} />
                        </button>
                      )}
                    </div>
                  ))}
                  {terms.length < 8 && (
                    <Button
                      type="button"
                      variant="ghost"
                      onClick={() => setTerms([...terms, { acl_id: "", negate: false }])}
                      icon={<Plus size={13} />}
                      className="w-fit !px-2.5 !py-1.5 text-xs"
                    >
                      Shart qo'shish
                    </Button>
                  )}
                </div>
              </div>
            </div>
            <div className="grid gap-4 sm:grid-cols-[1fr_160px_auto] sm:items-end">
              <div>
                <Label>Izoh (ixtiyoriy)</Label>
                <Input value={comment} onChange={(e) => setComment(e.target.value)} placeholder="Nima uchun bu qoida?" maxLength={200} />
              </div>
              <div>
                <Label>O'rni (bo'sh = oxiriga)</Label>
                <Input type="number" min={1} value={position} onChange={(e) => setPosition(e.target.value)} />
              </div>
              <Button type="submit" disabled={busy} icon={<Plus size={15} />}>
                Qo'shish
              </Button>
            </div>
            <p className="text-xs text-neutral-500">
              Eslatma: "Proksi foydalanuvchilari" sharti avtomatik ravishda oxirgi tekshiriladi, aks holda Squid
              hamma trafikdan login talab qilib qo'yardi.
            </p>
          </form>
        )}
      </Card>
    </div>
  );
}
