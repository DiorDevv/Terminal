import { useState } from "react";
import { Button } from "../../components/ui/Button";
import { CheckList } from "../../components/ui/CheckList";
import { Input, Label } from "../../components/ui/Input";
import { Modal } from "../../components/ui/Modal";
import { api, errorMessage } from "../../lib/api";
import { notifyResult, type ReloadResult } from "../../lib/reload";
import { useToast } from "../../lib/toast";
import { expiryToInput, inputToExpiry, type ProxyUser, type UserGroup } from "../../lib/userpolicy";

/** Expiry, daily quota, note and group membership of one account. */
export function UserSettingsDialog({
  user,
  groups,
  onClose,
  onSaved,
}: {
  user: ProxyUser;
  groups: UserGroup[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const toast = useToast();
  const [expiry, setExpiry] = useState(expiryToInput(user.expires_at));
  const [quota, setQuota] = useState(String(user.daily_quota_mb || ""));
  const [note, setNote] = useState(user.note);
  const [memberOf, setMemberOf] = useState<number[]>(user.groups.map((g) => g.id));
  const [busy, setBusy] = useState(false);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    const q = quota.trim() === "" ? 0 : Number(quota);
    if (!Number.isInteger(q) || q < 0) {
      toast.error("Kunlik kvota butun son bo'lishi kerak (bo'sh yoki 0 = cheksiz)");
      return;
    }
    setBusy(true);
    try {
      const res = await api.put<ReloadResult>(`/squid/proxy-users/${encodeURIComponent(user.username)}`, {
        expires_at: inputToExpiry(expiry),
        daily_quota_mb: q,
        note,
      });
      notifyResult(toast, res.data, `${user.username} sozlamalari saqlandi`);

      // Group membership: change only the groups whose membership differs.
      const before = new Set(user.groups.map((g) => g.id));
      for (const g of groups) {
        const want = memberOf.includes(g.id);
        if (want === before.has(g.id)) continue;
        const members = want ? [...g.members, user.username] : g.members.filter((m) => m !== user.username);
        await api.put(`/squid/user-groups/${g.id}/members`, { members });
      }
      onSaved();
      onClose();
    } catch (err) {
      toast.error(errorMessage(err, "Saqlab bo'lmadi"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal title={`${user.username} sozlamalari`} onClose={onClose}>
      <form onSubmit={save} className="space-y-4">
        <div>
          <Label>Amal qilish muddati (shu kunning oxirigacha)</Label>
          <div className="flex gap-2">
            <Input type="date" value={expiry} onChange={(e) => setExpiry(e.target.value)} />
            {expiry && (
              <Button type="button" variant="ghost" onClick={() => setExpiry("")}>
                Cheksiz
              </Button>
            )}
          </div>
          <p className="mt-1 text-xs text-neutral-500">Muddat tugagach hisob o'zi bloklanadi (30 soniya ichida).</p>
        </div>
        <div>
          <Label>Kunlik hajm kvotasi (MB)</Label>
          <Input type="number" min={0} value={quota} placeholder="cheksiz" onChange={(e) => setQuota(e.target.value)} />
          <p className="mt-1 text-xs text-neutral-500">
            Kuniga shuncha yuklab olgach, hisob yarim tungacha bloklanadi. Hajm access.log'dan hisoblanadi, shuning uchun bloklash 30–60 soniya kechikishi mumkin.
          </p>
        </div>
        <div>
          <Label>Izoh</Label>
          <Input value={note} maxLength={200} onChange={(e) => setNote(e.target.value)} placeholder="masalan: sinov muddati" />
        </div>
        <div>
          <Label>Guruhlar</Label>
          <CheckList
            options={groups.map((g) => ({ value: g.id, label: g.name }))}
            value={memberOf}
            onChange={setMemberOf}
            empty="Hali guruh yo'q. Pastdagi “Foydalanuvchi guruhlari” bo'limida yarating."
          />
        </div>
        <div className="flex justify-end gap-2 pt-1">
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
