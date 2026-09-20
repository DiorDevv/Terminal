import { ClipboardList, RefreshCw } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";
import { EmptyState } from "../components/ui/EmptyState";
import { Input } from "../components/ui/Input";
import { PageHeader } from "../components/ui/PageHeader";
import { ListSkeleton } from "../components/ui/Skeleton";
import { api, errorMessage } from "../lib/api";
import { formatDateTime, timeAgo } from "../lib/format";
import { useToast } from "../lib/toast";

interface Entry {
  id: number;
  time: string;
  username: string;
  ip: string;
  action: string;
  target: string;
  status: number;
  detail: string;
}

const PAGE = 50;

// Route patterns the backend records, in words the administrator reads.
const labels: Record<string, string> = {
  LOGIN: "Tizimga kirish",
  "POST /auth/logout": "Tizimdan chiqish",
  "PUT /auth/password": "O'z parolini o'zgartirdi",
  "DELETE /auth/sessions/:id": "Sessiyani tugatdi",
  "POST /squid/blacklist": "Domenni blokladi",
  "DELETE /squid/blacklist/:domain": "Domenni blokdan chiqardi",
  "POST /squid/users": "Proxy foydalanuvchisi qo'shdi/yangiladi",
  "DELETE /squid/users/:username": "Proxy foydalanuvchisini o'chirdi",
  "PUT /squid/lan-access": "LAN ruxsatini o'zgartirdi",
  "POST /squid/restrictions": "Vaqt cheklovi yaratdi",
  "DELETE /squid/restrictions/:id": "Vaqt cheklovini o'chirdi",
  "POST /squid/groups": "IP guruhi yaratdi",
  "DELETE /squid/groups/:id": "IP guruhini o'chirdi",
  "PUT /squid/config": "squid.conf'ni tahrirladi",
  "POST /squid/reconfigure": "Squid'ni reconfigure qildi",
  "POST /squid/service/:action": "Squid xizmatini boshqardi",
  "POST /squid/history/:id/restore": "Config versiyasini tikladi",
  "POST /panel/users": "Panel foydalanuvchisi yaratdi",
  "PUT /panel/users/:id": "Panel foydalanuvchisini o'zgartirdi",
  "POST /panel/users/:id/password": "Panel foydalanuvchisi parolini tikladi",
  "DELETE /panel/users/:id": "Panel foydalanuvchisini o'chirdi",
};

function StatusBadge({ status, detail }: { status: number; detail: string }) {
  let tone = "bg-emerald-500/15 text-emerald-300";
  let text = "muvaffaqiyatli";
  if (status === 401) {
    tone = "bg-red-500/15 text-red-300";
    text = "noto'g'ri";
  } else if (status === 403) {
    tone = "bg-amber-500/15 text-amber-300";
    text = "ruxsat yo'q";
  } else if (status === 429) {
    tone = "bg-red-500/15 text-red-300";
    text = "bloklandi";
  } else if (status >= 400) {
    tone = "bg-red-500/15 text-red-300";
    text = "xato";
  }
  return (
    <span
      title={`HTTP ${status}${detail ? ` · ${detail}` : ""}`}
      className={`rounded-full px-2 py-0.5 text-[10px] font-medium ${tone}`}
    >
      {text}
    </span>
  );
}

export default function Audit() {
  const [entries, setEntries] = useState<Entry[] | null>(null);
  const [filter, setFilter] = useState("");
  const [applied, setApplied] = useState("");
  const [more, setMore] = useState(false);
  const [busy, setBusy] = useState(false);
  const toast = useToast();

  const fetchPage = useCallback(async (username: string, before?: number) => {
    const res = await api.get<{ entries: Entry[] }>("/audit", {
      params: { limit: PAGE, ...(username ? { username } : {}), ...(before ? { before } : {}) },
    });
    return res.data.entries;
  }, []);

  const reload = useCallback(
    async (username: string) => {
      setBusy(true);
      try {
        const rows = await fetchPage(username);
        setEntries(rows);
        setMore(rows.length === PAGE);
        setApplied(username);
      } catch (err) {
        toast.error(errorMessage(err, "Yuklab bo'lmadi"));
      } finally {
        setBusy(false);
      }
    },
    [fetchPage, toast],
  );

  useEffect(() => {
    reload("");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function loadMore() {
    if (!entries?.length) return;
    setBusy(true);
    try {
      const rows = await fetchPage(applied, entries[entries.length - 1].id);
      setEntries([...entries, ...rows]);
      setMore(rows.length === PAGE);
    } catch (err) {
      toast.error(errorMessage(err, "Yuklab bo'lmadi"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div>
      <PageHeader
        icon={<ClipboardList size={19} />}
        title="Audit log"
        description="Panelda kim, qachon, nima qilgani. Faqat o'zgartirishlar va kirish urinishlari yoziladi; parollar hech qachon saqlanmaydi."
      />

      <form
        onSubmit={(e) => {
          e.preventDefault();
          reload(filter.trim());
        }}
        className="mb-4 flex gap-2"
      >
        <Input
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder="Foydalanuvchi nomi bo'yicha filtr (bo'sh = hammasi)"
        />
        <Button type="submit" variant="secondary" disabled={busy} icon={<RefreshCw size={14} className={busy ? "animate-spin" : ""} />}>
          Yangilash
        </Button>
      </form>

      <Card className="overflow-hidden">
        {entries === null ? (
          <ListSkeleton rows={6} />
        ) : entries.length === 0 ? (
          <EmptyState icon={<ClipboardList size={22} />} title="Yozuvlar topilmadi" />
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[640px] text-left text-sm">
              <thead>
                <tr className="border-b border-white/[0.06] text-[11px] uppercase tracking-wider text-neutral-500">
                  <th className="px-5 py-3 font-medium">Vaqt</th>
                  <th className="px-3 py-3 font-medium">Foydalanuvchi</th>
                  <th className="px-3 py-3 font-medium">Amal</th>
                  <th className="px-3 py-3 font-medium">Natija</th>
                  <th className="px-5 py-3 font-medium">IP</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-white/[0.05]">
                {entries.map((e) => (
                  <tr key={e.id} className="align-top hover:bg-white/[0.02]">
                    <td className="whitespace-nowrap px-5 py-3 text-xs text-neutral-400" title={formatDateTime(e.time)}>
                      {timeAgo(e.time)}
                      <div className="text-[10px] text-neutral-600">{formatDateTime(e.time)}</div>
                    </td>
                    <td className="px-3 py-3 font-medium text-neutral-200">{e.username || "—"}</td>
                    <td className="px-3 py-3 text-neutral-300">
                      {labels[e.action] ?? e.action}
                      {e.target && <div className="font-mono text-[11px] text-neutral-500">{e.target}</div>}
                    </td>
                    <td className="px-3 py-3">
                      <StatusBadge status={e.status} detail={e.detail} />
                    </td>
                    <td className="whitespace-nowrap px-5 py-3 font-mono text-xs text-neutral-500">{e.ip}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      {more && (
        <div className="mt-4 flex justify-center">
          <Button variant="secondary" onClick={loadMore} disabled={busy}>
            Eskiroq yozuvlar
          </Button>
        </div>
      )}
    </div>
  );
}
