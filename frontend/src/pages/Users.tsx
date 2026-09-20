import { Ban, CheckCircle2, Download, Plus, Settings2, Trash2, Upload, UserPlus, Users as UsersIcon } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useConfirm } from "../components/ConfirmDialog";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";
import { EmptyState } from "../components/ui/EmptyState";
import { Input } from "../components/ui/Input";
import { PageHeader } from "../components/ui/PageHeader";
import { ListSkeleton } from "../components/ui/Skeleton";
import { api, errorMessage } from "../lib/api";
import { useAuth } from "../lib/auth";
import { formatBytes } from "../lib/format";
import { notifyResult, type ReloadResult } from "../lib/reload";
import { useToast } from "../lib/toast";
import { formatExpiry, type ProxyUser, statusMeta, type UserGroup } from "../lib/userpolicy";
import { GroupsCard } from "./users/GroupsCard";
import { ImportDialog } from "./users/ImportDialog";
import { UserSettingsDialog } from "./users/UserSettingsDialog";

export default function Users() {
  const { can } = useAuth();
  const detailed = can("operator"); // usage and settings are shown to operators and above
  const [users, setUsers] = useState<ProxyUser[]>([]);
  const [groups, setGroups] = useState<UserGroup[]>([]);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [editing, setEditing] = useState<ProxyUser | null>(null);
  const [importFile, setImportFile] = useState<File | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);
  const toast = useToast();
  const confirm = useConfirm();

  const load = useCallback(async () => {
    if (detailed) {
      const [u, g] = await Promise.all([
        api.get<{ users: ProxyUser[] | null }>("/squid/proxy-users"),
        api.get<{ groups: UserGroup[] | null }>("/squid/user-groups"),
      ]);
      setUsers(u.data.users ?? []);
      setGroups(g.data.groups ?? []);
    } else {
      // A viewer only sees the names.
      const res = await api.get<{ users: string[] | null }>("/squid/users");
      setUsers(
        (res.data.users ?? []).map((name) => ({
          username: name, disabled: false, expires_at: 0, daily_quota_mb: 0, note: "", created_at: 0, groups: [], used_today: 0, status: "active" as const,
        })),
      );
    }
  }, [detailed]);

  useEffect(() => {
    load()
      .catch((err) => toast.error(errorMessage(err, "Yuklab bo'lmadi")))
      .finally(() => setLoading(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [load]);

  async function addUser(e: React.FormEvent) {
    e.preventDefault();
    if (!username.trim() || !password) return;
    setBusy(true);
    try {
      const res = await api.post<ReloadResult>("/squid/users", { username: username.trim(), password });
      notifyResult(toast, res.data, `${username.trim()} qo'shildi`);
      setUsername("");
      setPassword("");
      await load();
    } catch (err) {
      toast.error(errorMessage(err, "Foydalanuvchi qo'shib bo'lmadi"));
    } finally {
      setBusy(false);
    }
  }

  async function toggle(u: ProxyUser) {
    setBusy(true);
    try {
      const res = await api.put<ReloadResult>(`/squid/proxy-users/${encodeURIComponent(u.username)}`, { disabled: !u.disabled });
      notifyResult(toast, res.data, u.disabled ? `${u.username} qayta yoqildi` : `${u.username} o'chirib qo'yildi`);
      await load();
    } catch (err) {
      toast.error(errorMessage(err, "O'zgartirib bo'lmadi"));
    } finally {
      setBusy(false);
    }
  }

  async function removeUser(user: string) {
    const ok = await confirm({
      title: "Foydalanuvchini o'chirish",
      description: `"${user}" butunlay o'chiriladi: parol, sozlamalar va guruhlardagi a'zolik ham. Vaqtincha to'xtatish uchun uni "o'chirib qo'ying" (o'chirmasdan).`,
    });
    if (!ok) return;
    setBusy(true);
    try {
      const res = await api.delete<ReloadResult>(`/squid/users/${encodeURIComponent(user)}`);
      notifyResult(toast, res.data, `${user} o'chirildi`);
      await load();
    } catch (err) {
      toast.error(errorMessage(err, "O'chirib bo'lmadi"));
    } finally {
      setBusy(false);
    }
  }

  async function exportCSV() {
    try {
      const res = await api.get<Blob>("/squid/proxy-users-export", { responseType: "blob" });
      const url = URL.createObjectURL(res.data);
      const a = document.createElement("a");
      a.href = url;
      a.download = `proxy-users-${new Date().toISOString().slice(0, 10)}.csv`;
      a.click();
      URL.revokeObjectURL(url);
    } catch (err) {
      toast.error(errorMessage(err, "Yuklab olib bo'lmadi"));
    }
  }

  return (
    <div>
      <PageHeader
        icon={<UsersIcon size={19} />}
        title="Proxy foydalanuvchilari"
        description="Birinchi foydalanuvchi qo'shilganda, Squid barcha (localhost'dan tashqari) trafik uchun login/parol talab qila boshlaydi. Hisobni o'chirmasdan to'xtatish, muddat va kunlik kvota belgilash mumkin."
        action={
          detailed && (
            <div className="flex gap-2">
              <Button variant="secondary" icon={<Download size={15} />} onClick={exportCSV}>
                CSV
              </Button>
              <Button variant="secondary" icon={<Upload size={15} />} onClick={() => fileInput.current?.click()}>
                Import
              </Button>
              <input
                ref={fileInput}
                type="file"
                accept=".csv,text/csv"
                hidden
                onChange={(e) => {
                  setImportFile(e.target.files?.[0] ?? null);
                  e.target.value = ""; // the same file can be chosen again
                }}
              />
            </div>
          )
        }
      />

      <form onSubmit={addUser} className="mb-4 flex flex-wrap gap-2">
        <Input value={username} onChange={(e) => setUsername(e.target.value)} placeholder="foydalanuvchi nomi" className="!w-48" />
        <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} placeholder="parol" className="!w-48" autoComplete="new-password" />
        <Button type="submit" disabled={busy} icon={<Plus size={16} />}>
          Qo'shish
        </Button>
      </form>

      <Card>
        {loading ? (
          <ListSkeleton />
        ) : users.length === 0 ? (
          <EmptyState icon={<UserPlus size={22} />} title="Hozircha foydalanuvchi yo'q" />
        ) : (
          <ul className="divide-y divide-white/[0.06]">
            {users.map((u) => {
              const meta = statusMeta[u.status];
              const quotaBytes = u.daily_quota_mb * 1024 * 1024;
              return (
                <li key={u.username} className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2 px-5 py-3.5 text-sm">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="font-mono text-neutral-200">{u.username}</span>
                      {detailed && <span className={`rounded-full px-2 py-0.5 text-[11px] font-medium ${meta.tone}`}>{meta.label}</span>}
                      {u.groups.map((g) => (
                        <span key={g.id} className="rounded-md bg-indigo-500/10 px-1.5 py-0.5 text-[11px] text-indigo-300">
                          {g.name}
                        </span>
                      ))}
                    </div>
                    {detailed && (
                      <div className="mt-1 flex flex-wrap gap-x-4 text-xs text-neutral-500">
                        <span>Bugun: {u.used_today ? formatBytes(u.used_today) : "0 B"}{u.daily_quota_mb ? ` / ${formatBytes(quotaBytes)}` : ""}</span>
                        <span>Muddat: {formatExpiry(u.expires_at)}</span>
                        {u.note && <span className="truncate">“{u.note}”</span>}
                      </div>
                    )}
                    {detailed && u.daily_quota_mb > 0 && (
                      <div className="mt-1.5 h-1 w-48 overflow-hidden rounded-full bg-white/[0.06]" aria-hidden="true">
                        <div className={`h-full rounded-full ${u.used_today >= quotaBytes ? "bg-red-500" : "bg-indigo-500"}`} style={{ width: `${Math.min(100, (u.used_today / quotaBytes) * 100)}%` }} />
                      </div>
                    )}
                  </div>
                  <div className="flex shrink-0 items-center gap-4">
                    {detailed && (
                      <>
                        <button
                          type="button"
                          onClick={() => toggle(u)}
                          disabled={busy}
                          className="flex items-center gap-1.5 text-xs font-medium text-neutral-400 transition-colors hover:text-neutral-100 disabled:opacity-50"
                        >
                          {u.disabled ? <CheckCircle2 size={13} /> : <Ban size={13} />}
                          {u.disabled ? "Yoqish" : "To'xtatish"}
                        </button>
                        <button
                          type="button"
                          onClick={() => setEditing(u)}
                          className="flex items-center gap-1.5 text-xs font-medium text-indigo-300 transition-colors hover:text-indigo-200"
                        >
                          <Settings2 size={13} />
                          Sozlamalar
                        </button>
                      </>
                    )}
                    <button
                      type="button"
                      onClick={() => removeUser(u.username)}
                      disabled={busy}
                      className="flex items-center gap-1.5 text-xs font-medium text-red-400/80 transition-colors hover:text-red-400 disabled:opacity-50"
                    >
                      <Trash2 size={13} />
                      O'chirish
                    </button>
                  </div>
                </li>
              );
            })}
          </ul>
        )}
      </Card>

      {detailed && <GroupsCard groups={groups} usernames={users.map((u) => u.username)} reload={() => load().catch(() => {})} />}

      {editing && <UserSettingsDialog user={editing} groups={groups} onClose={() => setEditing(null)} onSaved={() => load().catch(() => {})} />}
      {importFile && <ImportDialog file={importFile} onClose={() => setImportFile(null)} onDone={() => load().catch(() => {})} />}
    </div>
  );
}
