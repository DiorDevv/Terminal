import { Boxes, Pencil, Plus, Trash2, X } from "lucide-react";
import { useEffect, useState } from "react";
import { useConfirm } from "../../components/ConfirmDialog";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { EmptyState } from "../../components/ui/EmptyState";
import { Input, Label, Select } from "../../components/ui/Input";
import { api, errorMessage } from "../../lib/api";
import { type Acl, type AclType, aclTypeMeta } from "../../lib/access";
import { notifyResult, type ReloadResult } from "../../lib/reload";
import { useToast } from "../../lib/toast";

interface Group {
  id: number;
  name: string;
  members: string[];
}

const lines = (s: string) =>
  s
    .split("\n")
    .map((x) => x.trim())
    .filter(Boolean);

export default function AclsTab({
  acls,
  types,
  reload,
}: {
  acls: Acl[];
  types: AclType[];
  reload: () => Promise<void>;
}) {
  const toast = useToast();
  const confirm = useConfirm();
  const [editing, setEditing] = useState<Acl | null>(null);
  const [name, setName] = useState("");
  const [type, setType] = useState("dstdomain");
  const [values, setValues] = useState("");
  const [ci, setCi] = useState(true);
  const [description, setDescription] = useState("");
  const [groups, setGroups] = useState<Group[]>([]);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api
      .get<{ groups: Group[] }>("/squid/groups")
      .then((r) => setGroups(r.data.groups ?? []))
      .catch(() => {});
  }, []);

  const selectable = types.filter((t) => !t.managed);
  const info = types.find((t) => t.type === (editing ? editing.type : type));
  const meta = aclTypeMeta[editing ? editing.type : type];

  function startEdit(a: Acl) {
    setEditing(a);
    setName(a.name);
    setType(a.type);
    setValues(a.values.join("\n"));
    setCi(a.case_insensitive);
    setDescription(a.description);
  }

  function reset() {
    setEditing(null);
    setName("");
    setValues("");
    setDescription("");
    setCi(true);
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      if (editing) {
        const res = await api.put<ReloadResult>(`/squid/access/acls/${editing.id}`, {
          values: editing.type === "blocklist" ? undefined : lines(values),
          case_insensitive: ci,
          description,
        });
        notifyResult(toast, res.data, `"${editing.name}" yangilandi`);
      } else {
        await api.post("/squid/access/acls", { name: name.trim(), type, values: lines(values), case_insensitive: ci, description });
        toast.success(`"${name.trim()}" yaratildi`);
      }
      reset();
      await reload();
    } catch (err) {
      toast.error(errorMessage(err, "Saqlab bo'lmadi"));
    } finally {
      setBusy(false);
    }
  }

  async function remove(a: Acl) {
    const ok = await confirm({ title: "ACL'ni o'chirish", description: `"${a.name}" o'chirilsinmi?` });
    if (!ok) return;
    try {
      await api.delete(`/squid/access/acls/${a.id}`);
      toast.success(`"${a.name}" o'chirildi`);
      if (editing?.id === a.id) reset();
      await reload();
    } catch (err) {
      toast.error(errorMessage(err, "O'chirib bo'lmadi"));
    }
  }

  function importGroup(id: string) {
    const g = groups.find((x) => String(x.id) === id);
    if (!g) return;
    setValues((v) => [...lines(v), ...g.members].join("\n"));
  }

  return (
    <div className="grid gap-6">
      <Card className="p-5">
        <div className="mb-4 flex items-center justify-between">
          <h3 className="text-sm font-semibold text-white">
            {editing ? `"${editing.name}" ni tahrirlash` : "Yangi ACL obyekti"}
          </h3>
          {editing && (
            <button onClick={reset} className="flex items-center gap-1 text-xs text-neutral-500 hover:text-neutral-200">
              <X size={13} /> Bekor qilish
            </button>
          )}
        </div>

        <form onSubmit={submit} className="grid gap-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <div>
              <Label>Nom</Label>
              <Input
                value={name}
                onChange={(e) => setName(e.target.value)}
                disabled={!!editing}
                placeholder="masalan: social"
                className="font-mono"
              />
            </div>
            <div>
              <Label>Tur</Label>
              <Select value={editing ? editing.type : type} onChange={(e) => setType(e.target.value)} disabled={!!editing}>
                {(editing?.type === "blocklist" ? types : selectable).map((t) => (
                  <option key={t.type} value={t.type}>
                    {aclTypeMeta[t.type]?.label ?? t.type}
                  </option>
                ))}
              </Select>
            </div>
          </div>

          {editing?.type === "blocklist" ? (
            <p className="rounded-lg border border-white/[0.08] bg-white/[0.03] px-3 py-2 text-sm text-neutral-400">
              Bu ACL yuklab olinadigan blocklist ro'yxatiga bog'langan. Uni "Blocklist manbalari" sahifasidan boshqaring.
            </p>
          ) : (
            <div>
              <Label>Qiymatlar (har qatorga bittadan)</Label>
              <textarea
                value={values}
                onChange={(e) => setValues(e.target.value)}
                rows={5}
                placeholder={meta?.placeholder}
                className="w-full rounded-lg border border-white/10 bg-white/[0.03] px-3 py-2 font-mono text-sm text-neutral-100 outline-none placeholder:text-neutral-600 focus:border-indigo-500/60"
              />
              <p className="mt-1.5 text-xs text-neutral-500">{meta?.hint}</p>
              {(editing ? editing.type : type) === "src" && groups.length > 0 && (
                <div className="mt-2 flex items-center gap-2 text-xs text-neutral-500">
                  IP guruhidan nusxalash:
                  <Select value="" onChange={(e) => importGroup(e.target.value)} className="!w-48 !py-1 text-xs">
                    <option value="">Guruh tanlang…</option>
                    {groups.map((g) => (
                      <option key={g.id} value={g.id}>
                        {g.name} ({g.members.length})
                      </option>
                    ))}
                  </Select>
                </div>
              )}
            </div>
          )}

          <div className="grid gap-4 sm:grid-cols-[1fr_auto] sm:items-end">
            <div>
              <Label>Tavsif (ixtiyoriy)</Label>
              <Input value={description} onChange={(e) => setDescription(e.target.value)} maxLength={200} />
            </div>
            {info?.regex && (
              <label className="flex items-center gap-2 pb-2 text-xs text-neutral-400">
                <input type="checkbox" checked={ci} onChange={(e) => setCi(e.target.checked)} />
                Katta-kichik harfga e'tibor bermaslik
              </label>
            )}
          </div>

          <Button type="submit" disabled={busy} icon={editing ? <Pencil size={15} /> : <Plus size={15} />} className="w-fit">
            {editing ? "Saqlash" : "Yaratish"}
          </Button>
        </form>
      </Card>

      <Card>
        {acls.length === 0 ? (
          <EmptyState
            icon={<Boxes size={22} />}
            title="Hozircha ACL obyekti yo'q"
            description="ACL: nomlangan domenlar, IP'lar, portlar yoki vaqt oynalari to'plami. Qoidalar ularni birlashtiradi."
          />
        ) : (
          <ul className="divide-y divide-white/[0.06]">
            {acls.map((a) => (
              <li key={a.id} className="flex flex-wrap items-center justify-between gap-3 px-5 py-3.5">
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2 text-sm">
                    <code className="font-mono font-medium text-neutral-100">{a.name}</code>
                    <span className="rounded-full bg-indigo-500/15 px-2 py-0.5 text-[10px] font-medium text-indigo-300">
                      {aclTypeMeta[a.type]?.label ?? a.type}
                    </span>
                    <span className="text-[11px] text-neutral-500">
                      {a.used_by > 0 ? `${a.used_by} ta qoidada` : "ishlatilmaydi"}
                    </span>
                  </div>
                  <div className="mt-1 truncate font-mono text-xs text-neutral-500" title={a.values.join(", ")}>
                    {a.values.slice(0, 6).join("  ·  ")}
                    {a.values.length > 6 && `  … +${a.values.length - 6}`}
                  </div>
                  {a.description && <div className="mt-0.5 text-xs text-neutral-600">{a.description}</div>}
                </div>
                <div className="flex items-center gap-1">
                  <Button variant="ghost" onClick={() => startEdit(a)} icon={<Pencil size={13} />} className="!px-2.5 !py-1.5 text-xs">
                    Tahrirlash
                  </Button>
                  <button
                    onClick={() => remove(a)}
                    disabled={a.used_by > 0 || a.type === "blocklist"}
                    title={a.used_by > 0 ? "Qoidalar ishlatayotgan ACL'ni o'chirib bo'lmaydi" : ""}
                    className="rounded-md p-1.5 text-red-400/80 hover:bg-red-500/10 hover:text-red-400 disabled:cursor-not-allowed disabled:opacity-30"
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
    </div>
  );
}
