import { Plus, Trash2, UsersRound } from "lucide-react";
import { useState } from "react";
import { useConfirm } from "../../components/ConfirmDialog";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { CheckList } from "../../components/ui/CheckList";
import { Input } from "../../components/ui/Input";
import { Modal } from "../../components/ui/Modal";
import { api, errorMessage } from "../../lib/api";
import { notifyResult, type ReloadResult } from "../../lib/reload";
import { useToast } from "../../lib/toast";
import type { UserGroup } from "../../lib/userpolicy";

/** Named sets of proxy users, used to aim speed and download limits. */
export function GroupsCard({
  groups,
  usernames,
  reload,
}: {
  groups: UserGroup[];
  usernames: string[];
  reload: () => void;
}) {
  const toast = useToast();
  const confirm = useConfirm();
  const [name, setName] = useState("");
  const [editing, setEditing] = useState<UserGroup | null>(null);
  const [members, setMembers] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);

  async function create(e: React.FormEvent) {
    e.preventDefault();
    if (!name.trim()) return;
    setBusy(true);
    try {
      await api.post("/squid/user-groups", { name: name.trim() });
      setName("");
      reload();
    } catch (err) {
      toast.error(errorMessage(err, "Guruh yaratib bo'lmadi"));
    } finally {
      setBusy(false);
    }
  }

  async function remove(g: UserGroup) {
    const ok = await confirm({
      title: "Guruhni o'chirish",
      description: `"${g.name}" o'chiriladi. Unga bog'langan cheklovlar endi bu a'zolarga qo'llanmaydi. Foydalanuvchilarning o'zi o'chmaydi.`,
    });
    if (!ok) return;
    try {
      const res = await api.delete<ReloadResult>(`/squid/user-groups/${g.id}`);
      notifyResult(toast, res.data, `${g.name} o'chirildi`);
      reload();
    } catch (err) {
      toast.error(errorMessage(err, "O'chirib bo'lmadi"));
    }
  }

  async function saveMembers() {
    if (!editing) return;
    setBusy(true);
    try {
      const res = await api.put<ReloadResult>(`/squid/user-groups/${editing.id}/members`, { members });
      notifyResult(toast, res.data, `${editing.name} a'zolari saqlandi`);
      setEditing(null);
      reload();
    } catch (err) {
      toast.error(errorMessage(err, "Saqlab bo'lmadi"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card className="mt-6 p-5">
      <h3 className="mb-1 flex items-center gap-2 text-sm font-semibold text-white">
        <UsersRound size={16} className="text-indigo-400" />
        Foydalanuvchi guruhlari
      </h3>
      <p className="mb-4 text-xs text-neutral-500">Guruhni “Cheklovlar” sahifasida tezlik yoki yuklab olish hajmi chegarasiga bog'lash mumkin.</p>

      <form onSubmit={create} className="mb-4 flex gap-2">
        <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="guruh nomi, masalan: xodimlar" className="max-w-xs" />
        <Button type="submit" disabled={busy || !name.trim()} icon={<Plus size={15} />}>
          Yaratish
        </Button>
      </form>

      {groups.length === 0 ? (
        <p className="py-4 text-center text-xs text-neutral-500">Hali guruh yo'q</p>
      ) : (
        <ul className="divide-y divide-white/[0.06]">
          {groups.map((g) => (
            <li key={g.id} className="flex flex-wrap items-center justify-between gap-3 py-3 text-sm">
              <div className="min-w-0">
                <div className="font-medium text-neutral-200">{g.name}</div>
                <div className="mt-0.5 truncate text-xs text-neutral-500">{g.members.length ? g.members.join(", ") : "a'zosi yo'q"}</div>
              </div>
              <div className="flex shrink-0 items-center gap-3">
                <button
                  type="button"
                  className="text-xs font-medium text-indigo-300 hover:text-indigo-200"
                  onClick={() => {
                    setEditing(g);
                    setMembers(g.members);
                  }}
                >
                  A'zolar
                </button>
                <button type="button" aria-label={`${g.name} guruhini o'chirish`} onClick={() => remove(g)} className="text-red-400/80 hover:text-red-400">
                  <Trash2 size={14} />
                </button>
              </div>
            </li>
          ))}
        </ul>
      )}

      {editing && (
        <Modal title={`${editing.name}: a'zolar`} onClose={() => setEditing(null)}>
          <CheckList options={usernames.map((u) => ({ value: u, label: u }))} value={members} onChange={setMembers} empty="Proxy foydalanuvchilari yo'q" />
          <div className="mt-5 flex justify-end gap-2">
            <Button variant="ghost" onClick={() => setEditing(null)}>
              Bekor qilish
            </Button>
            <Button onClick={saveMembers} disabled={busy}>
              Saqlash
            </Button>
          </div>
        </Modal>
      )}
    </Card>
  );
}
