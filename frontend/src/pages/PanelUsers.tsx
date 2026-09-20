import { KeyRound, Plus, ShieldCheck, Trash2, UserCog, UserX } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { useConfirm } from "../components/ConfirmDialog";
import { ResetPasswordDialog } from "../components/ResetPasswordDialog";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";
import { EmptyState } from "../components/ui/EmptyState";
import { Input, Label, Select } from "../components/ui/Input";
import { PageHeader } from "../components/ui/PageHeader";
import { ListSkeleton } from "../components/ui/Skeleton";
import { api, errorMessage } from "../lib/api";
import { type Role, type User, useAuth } from "../lib/auth";
import { useToast } from "../lib/toast";

const roleLabels: Record<Role, string> = {
  viewer: "Kuzatuvchi",
  operator: "Operator",
  admin: "Administrator",
};

const roleHints: Record<Role, string> = {
  viewer: "Faqat ko'radi: dashboard va ro'yxatlar",
  operator: "Bloklash, cheklovlar, guruhlar, proxy foydalanuvchilari, loglar",
  admin: "Hammasi: config, tarix, panel foydalanuvchilari, audit",
};

const roleTone: Record<Role, string> = {
  viewer: "bg-white/[0.06] text-neutral-300",
  operator: "bg-indigo-500/15 text-indigo-300",
  admin: "bg-amber-500/15 text-amber-300",
};

export default function PanelUsers() {
  const { user: me } = useAuth();
  const [users, setUsers] = useState<User[] | null>(null);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState<Role>("operator");
  const [busy, setBusy] = useState(false);
  const [resetting, setResetting] = useState<User | null>(null);
  const toast = useToast();
  const confirm = useConfirm();

  const load = useCallback(async () => {
    const res = await api.get<{ users: User[] }>("/panel/users");
    setUsers(res.data.users);
  }, []);

  useEffect(() => {
    load().catch((err) => toast.error(errorMessage(err, "Yuklab bo'lmadi")));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function run(fn: () => Promise<unknown>, ok: string, fail: string) {
    setBusy(true);
    try {
      await fn();
      toast.success(ok);
      await load();
    } catch (err) {
      toast.error(errorMessage(err, fail));
    } finally {
      setBusy(false);
    }
  }

  async function create(e: React.FormEvent) {
    e.preventDefault();
    if (!username.trim() || !password) return;
    await run(
      () => api.post("/panel/users", { username: username.trim(), password, role }),
      `${username.trim()} qo'shildi. Birinchi kirishda parolni almashtiradi.`,
      "Foydalanuvchini qo'shib bo'lmadi",
    );
    setUsername("");
    setPassword("");
  }

  const changeRole = (u: User, next: Role) =>
    run(
      () => api.put(`/panel/users/${u.id}`, { role: next }),
      `${u.username} roli "${roleLabels[next]}" ga o'zgartirildi (sessiyalari tugatildi)`,
      "Rolni o'zgartirib bo'lmadi",
    );

  const toggleDisabled = (u: User) =>
    run(
      () => api.put(`/panel/users/${u.id}`, { disabled: !u.disabled }),
      u.disabled ? `${u.username} qayta yoqildi` : `${u.username} o'chirib qo'yildi va tizimdan chiqarildi`,
      "O'zgartirib bo'lmadi",
    );

  async function resetPassword(u: User, temp: string) {
    await run(
      () => api.post(`/panel/users/${u.id}/password`, { password: temp }),
      `${u.username} uchun vaqtinchalik parol o'rnatildi`,
      "Parolni tiklab bo'lmadi",
    );
    setResetting(null);
  }

  async function remove(u: User) {
    const ok = await confirm({
      title: "Foydalanuvchini o'chirish",
      description: `"${u.username}" hisobi butunlay o'chiriladi. Buni qaytarib bo'lmaydi.`,
    });
    if (!ok) return;
    await run(
      () => api.delete(`/panel/users/${u.id}`),
      `${u.username} o'chirildi`,
      "O'chirib bo'lmadi",
    );
  }

  return (
    <div>
      <PageHeader
        icon={<UserCog size={19} />}
        title="Panel foydalanuvchilari"
        description="Shu boshqaruv paneliga kira oladigan hisoblar (Squid proxy foydalanuvchilari bilan adashtirmang). Har biriga rol beriladi."
      />

      <Card className="mb-6 p-5">
        <form onSubmit={create} className="grid gap-4 sm:grid-cols-[1fr_1fr_190px_auto] sm:items-end">
          <div>
            <Label>Foydalanuvchi nomi</Label>
            <Input
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              placeholder="masalan: jamshid"
              autoComplete="off"
            />
          </div>
          <div>
            <Label>Vaqtinchalik parol</Label>
            <Input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder="kamida 8 belgi"
              autoComplete="new-password"
            />
          </div>
          <div>
            <Label>Rol</Label>
            <Select value={role} onChange={(e) => setRole(e.target.value as Role)}>
              {(Object.keys(roleLabels) as Role[]).map((r) => (
                <option key={r} value={r}>
                  {roleLabels[r]}
                </option>
              ))}
            </Select>
          </div>
          <Button type="submit" disabled={busy} icon={<Plus size={16} />}>
            Qo'shish
          </Button>
        </form>
        <p className="mt-3 text-xs text-neutral-500">{roleHints[role]}</p>
      </Card>

      <Card>
        {users === null ? (
          <ListSkeleton />
        ) : users.length === 0 ? (
          <EmptyState icon={<UserCog size={22} />} title="Foydalanuvchilar yo'q" />
        ) : (
          <ul className="divide-y divide-white/[0.06]">
            {users.map((u) => {
              const isMe = u.id === me?.id;
              return (
                <li
                  key={u.id}
                  className={`flex flex-wrap items-center justify-between gap-3 px-5 py-3.5 ${u.disabled ? "opacity-60" : ""}`}
                >
                  <div className="flex min-w-0 items-center gap-3">
                    <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-white/[0.06] text-sm font-semibold uppercase text-neutral-300">
                      {u.username.slice(0, 1)}
                    </div>
                    <div className="min-w-0">
                      <div className="flex flex-wrap items-center gap-2 text-sm font-medium text-neutral-100">
                        {u.username}
                        {isMe && <span className="text-[11px] font-normal text-neutral-500">(siz)</span>}
                        <span className={`rounded-full px-2 py-0.5 text-[10px] font-medium ${roleTone[u.role]}`}>
                          {roleLabels[u.role]}
                        </span>
                        {u.disabled && (
                          <span className="rounded-full bg-red-500/15 px-2 py-0.5 text-[10px] font-medium text-red-300">
                            o'chirilgan
                          </span>
                        )}
                        {u.must_change_password && (
                          <span className="rounded-full bg-amber-500/15 px-2 py-0.5 text-[10px] font-medium text-amber-300">
                            parol almashtirishi kerak
                          </span>
                        )}
                      </div>
                    </div>
                  </div>

                  <div className="flex flex-wrap items-center gap-1.5">
                    <Select
                      value={u.role}
                      disabled={busy}
                      onChange={(e) => changeRole(u, e.target.value as Role)}
                      className="!w-36 !py-1.5 text-xs"
                      aria-label={`${u.username} roli`}
                    >
                      {(Object.keys(roleLabels) as Role[]).map((r) => (
                        <option key={r} value={r}>
                          {roleLabels[r]}
                        </option>
                      ))}
                    </Select>
                    <Button
                      variant="ghost"
                      disabled={busy}
                      onClick={() => setResetting(u)}
                      icon={<KeyRound size={13} />}
                      className="!px-2.5 !py-1.5 text-xs"
                    >
                      Parolni tiklash
                    </Button>
                    {!isMe && (
                      <>
                        <Button
                          variant="ghost"
                          disabled={busy}
                          onClick={() => toggleDisabled(u)}
                          icon={u.disabled ? <ShieldCheck size={13} /> : <UserX size={13} />}
                          className="!px-2.5 !py-1.5 text-xs"
                        >
                          {u.disabled ? "Yoqish" : "O'chirib qo'yish"}
                        </Button>
                        <button
                          onClick={() => remove(u)}
                          disabled={busy}
                          className="flex items-center gap-1.5 px-2 text-xs font-medium text-red-400/80 transition-colors hover:text-red-400 disabled:opacity-50"
                        >
                          <Trash2 size={13} />
                          O'chirish
                        </button>
                      </>
                    )}
                  </div>
                </li>
              );
            })}
          </ul>
        )}
      </Card>

      {resetting && (
        <ResetPasswordDialog
          username={resetting.username}
          busy={busy}
          onSubmit={(temp) => resetPassword(resetting, temp)}
          onCancel={() => setResetting(null)}
        />
      )}
    </div>
  );
}
