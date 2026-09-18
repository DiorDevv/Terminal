import { Plus, Trash2, UserPlus, Users as UsersIcon } from "lucide-react";
import { useEffect, useState } from "react";
import { useConfirm } from "../components/ConfirmDialog";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";
import { EmptyState } from "../components/ui/EmptyState";
import { Input } from "../components/ui/Input";
import { PageHeader } from "../components/ui/PageHeader";
import { ListSkeleton } from "../components/ui/Skeleton";
import { api } from "../lib/api";
import { useToast } from "../lib/toast";

export default function Users() {
  const [users, setUsers] = useState<string[]>([]);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const toast = useToast();
  const confirm = useConfirm();

  async function load() {
    const res = await api.get<{ users: string[] | null }>("/squid/users");
    setUsers(res.data.users ?? []);
  }

  useEffect(() => {
    load()
      .catch((err) => toast.error(err.response?.data?.error ?? "Yuklab bo'lmadi"))
      .finally(() => setLoading(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function addUser(e: React.FormEvent) {
    e.preventDefault();
    if (!username.trim() || !password) return;
    setBusy(true);
    try {
      const res = await api.post("/squid/users", {
        username: username.trim(),
        password,
      });
      if (res.data.warning) {
        toast.error(res.data.warning);
      } else {
        toast.success(`${username.trim()} qo'shildi`);
      }
      setUsername("");
      setPassword("");
      await load();
    } catch (err: any) {
      toast.error(err.response?.data?.error ?? "Foydalanuvchi qo'shib bo'lmadi");
    } finally {
      setBusy(false);
    }
  }

  async function removeUser(user: string) {
    const ok = await confirm({
      title: "Foydalanuvchini o'chirish",
      description: `"${user}" proxy'dan kira olmay qoladi. Davom etilsinmi?`,
    });
    if (!ok) return;

    setBusy(true);
    try {
      await api.delete(`/squid/users/${encodeURIComponent(user)}`);
      toast.success(`${user} o'chirildi`);
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
        icon={<UsersIcon size={19} />}
        title="Proxy foydalanuvchilari"
        description="Birinchi foydalanuvchi qo'shilganda, Squid barcha (localhost'dan tashqari) trafik uchun login/parol talab qila boshlaydi"
      />

      <form onSubmit={addUser} className="mb-4 flex flex-wrap gap-2">
        <Input
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          placeholder="foydalanuvchi nomi"
          className="w-48"
        />
        <Input
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          placeholder="parol"
          className="w-48"
        />
        <Button type="submit" disabled={busy} icon={<Plus size={16} />}>
          Qo'shish
        </Button>
      </form>

      <Card>
        {loading ? (
          <ListSkeleton />
        ) : users.length === 0 ? (
          <EmptyState
            icon={<UserPlus size={22} />}
            title="Hozircha foydalanuvchi yo'q"
          />
        ) : (
          <ul className="divide-y divide-white/[0.06]">
            {users.map((user) => (
              <li
                key={user}
                className="flex items-center justify-between px-5 py-3.5 text-sm"
              >
                <span className="font-mono text-neutral-300">{user}</span>
                <button
                  onClick={() => removeUser(user)}
                  disabled={busy}
                  className="flex items-center gap-1.5 text-xs font-medium text-red-400/80 transition-colors hover:text-red-400 disabled:opacity-50"
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
