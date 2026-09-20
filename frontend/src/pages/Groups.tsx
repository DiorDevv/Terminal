import { Network, Plus, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { useConfirm } from "../components/ConfirmDialog";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";
import { EmptyState } from "../components/ui/EmptyState";
import { Input } from "../components/ui/Input";
import { PageHeader } from "../components/ui/PageHeader";
import { ListSkeleton } from "../components/ui/Skeleton";
import { api } from "../lib/api";
import { notifyResult, type ReloadResult } from "../lib/reload";
import { useToast } from "../lib/toast";

interface Group {
  id: number;
  name: string;
  members: string[];
}

export default function Groups() {
  const [groups, setGroups] = useState<Group[]>([]);
  const [name, setName] = useState("");
  const [members, setMembers] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const toast = useToast();
  const confirm = useConfirm();

  async function load() {
    const res = await api.get<{ groups: Group[] | null }>("/squid/groups");
    setGroups(res.data.groups ?? []);
  }

  useEffect(() => {
    load()
      .catch((err) => toast.error(err.response?.data?.error ?? "Yuklab bo'lmadi"))
      .finally(() => setLoading(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function create(e: React.FormEvent) {
    e.preventDefault();
    if (!name.trim() || !members.trim()) return;
    setBusy(true);
    try {
      await api.post("/squid/groups", {
        name: name.trim(),
        members: members.split(",").map((m) => m.trim()).filter(Boolean),
      });
      toast.success(`"${name.trim()}" guruhi yaratildi`);
      setName("");
      setMembers("");
      await load();
    } catch (err: any) {
      toast.error(err.response?.data?.error ?? "Yaratib bo'lmadi");
    } finally {
      setBusy(false);
    }
  }

  async function remove(group: Group) {
    const ok = await confirm({
      title: "Guruhni o'chirish",
      description: `"${group.name}" guruhi o'chirilsinmi? Bu guruhni ishlatgan vaqt cheklovlari endi istisnosiz qoladi.`,
    });
    if (!ok) return;

    setBusy(true);
    try {
      const res = await api.delete<ReloadResult>(`/squid/groups/${group.id}`);
      notifyResult(toast, res.data, `"${group.name}" o'chirildi`);
      await load();
    } catch (err: any) {
      toast.error(err.response?.data?.error ?? "O'chirib bo'lmadi");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div>
      <PageHeader
        icon={<Network size={19} />}
        title="IP guruhlari"
        description={`Masalan "IT bo'limi" kabi qayta ishlatiladigan IP/tarmoq guruhlari. Bu guruhni Vaqt cheklovlari sahifasida istisno sifatida tanlashingiz mumkin — bir joyda o'zgartirsangiz, uni ishlatgan barcha qoidalarga avtomatik qo'llanadi.`}
      />

      <form
        onSubmit={create}
        className="mb-4 flex flex-wrap gap-2"
      >
        <Input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="guruh nomi (masalan it_bolimi)"
          className="w-56"
        />
        <Input
          value={members}
          onChange={(e) => setMembers(e.target.value)}
          placeholder="IP/CIDR, vergul bilan: 127.0.0.1, 10.0.0.0/24"
          className="min-w-64 flex-1"
        />
        <Button type="submit" disabled={busy} icon={<Plus size={16} />}>
          Yaratish
        </Button>
      </form>

      <Card>
        {loading ? (
          <ListSkeleton />
        ) : groups.length === 0 ? (
          <EmptyState icon={<Network size={22} />} title="Hozircha guruh yo'q" />
        ) : (
          <ul className="divide-y divide-white/[0.06]">
            {groups.map((group) => (
              <li key={group.id} className="flex items-start justify-between gap-4 px-5 py-3.5">
                <div className="text-sm">
                  <div className="font-medium text-neutral-100">{group.name}</div>
                  <div className="mt-1 font-mono text-xs text-neutral-500">
                    {group.members.join(", ")}
                  </div>
                </div>
                <button
                  onClick={() => remove(group)}
                  disabled={busy}
                  className="flex shrink-0 items-center gap-1.5 text-xs font-medium text-red-400/80 transition-colors hover:text-red-400 disabled:opacity-50"
                >
                  <Trash2 size={13} />
                  O'chirish
                </button>
              </li>
            ))}
          </ul>
        )}
      </Card>
    </div>
  );
}
