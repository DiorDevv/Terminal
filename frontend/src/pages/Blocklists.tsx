import { CloudDownload, ExternalLink, Plus, RefreshCw, Trash2 } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { useConfirm } from "../components/ConfirmDialog";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";
import { EmptyState } from "../components/ui/EmptyState";
import { Input, Label } from "../components/ui/Input";
import { PageHeader } from "../components/ui/PageHeader";
import { ListSkeleton } from "../components/ui/Skeleton";
import type { BlocklistSource } from "../lib/access";
import { api, errorMessage } from "../lib/api";
import { timeAgo } from "../lib/format";
import { notifyResult, type ReloadResult } from "../lib/reload";
import { useToast } from "../lib/toast";

// Well-known public lists, offered as starting points. They are fetched by the
// server, not by the browser, and only when the administrator adds them.
const suggestions = [
  {
    name: "stevenblack",
    url: "https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts",
    note: "Reklama va zararli domenlar (birlashtirilgan hosts)",
  },
  {
    name: "urlhaus",
    url: "https://urlhaus.abuse.ch/downloads/hostfile/",
    note: "Zararli dasturlar tarqatuvchi domenlar (abuse.ch)",
  },
];

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

export default function Blocklists() {
  const toast = useToast();
  const confirm = useConfirm();
  const [sources, setSources] = useState<BlocklistSource[] | null>(null);
  const [name, setName] = useState("");
  const [url, setUrl] = useState("");
  const [hours, setHours] = useState("24");
  const [addRule, setAddRule] = useState(true);
  const [busy, setBusy] = useState(false);
  const [refreshing, setRefreshing] = useState<number | null>(null);

  const load = useCallback(async () => {
    const res = await api.get<{ sources: BlocklistSource[] }>("/squid/blocklists");
    setSources(res.data.sources);
  }, []);

  useEffect(() => {
    load().catch((err) => toast.error(errorMessage(err, "Yuklab bo'lmadi")));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function create(e: React.FormEvent) {
    e.preventDefault();
    if (!name.trim() || !url.trim()) return;
    setBusy(true);
    try {
      const res = await api.post<ReloadResult & { source: BlocklistSource }>("/squid/blocklists", {
        name: name.trim(),
        url: url.trim(),
        interval_hours: Number(hours) || 24,
        add_rule: addRule,
      });
      if (res.data.source.last_status.startsWith("error")) {
        toast.error(`"${res.data.source.name}" qo'shildi, lekin yuklab bo'lmadi: ${res.data.source.last_status.replace("error: ", "")}`);
      } else {
        notifyResult(toast, res.data, `"${res.data.source.name}" qo'shildi: ${res.data.source.entry_count} ta domen`);
      }
      setName("");
      setUrl("");
      await load();
    } catch (err) {
      toast.error(errorMessage(err, "Qo'shib bo'lmadi"));
    } finally {
      setBusy(false);
    }
  }

  async function refresh(s: BlocklistSource) {
    setRefreshing(s.id);
    try {
      const res = await api.post<{ entries: number; changed: boolean }>(`/squid/blocklists/${s.id}/refresh`);
      toast.success(res.data.changed ? `"${s.name}" yangilandi: ${res.data.entries} ta domen` : `"${s.name}" o'zgarmagan (${res.data.entries} ta domen)`);
    } catch (err) {
      toast.error(errorMessage(err, "Yangilab bo'lmadi"));
    } finally {
      setRefreshing(null);
      await load().catch(() => {});
    }
  }

  async function toggle(s: BlocklistSource) {
    try {
      await api.put(`/squid/blocklists/${s.id}`, { enabled: !s.enabled });
      await load();
    } catch (err) {
      toast.error(errorMessage(err, "O'zgartirib bo'lmadi"));
    }
  }

  async function changeInterval(s: BlocklistSource, value: string) {
    const n = Number(value);
    if (!n || n === s.interval_hours) return;
    try {
      await api.put(`/squid/blocklists/${s.id}`, { interval_hours: n });
      await load();
    } catch (err) {
      toast.error(errorMessage(err, "O'zgartirib bo'lmadi"));
      await load();
    }
  }

  async function remove(s: BlocklistSource) {
    const ok = await confirm({
      title: "Blocklist'ni o'chirish",
      description: `"${s.name}" va unga bog'liq qoidalar o'chiriladi. Ro'yxatdagi saytlar yana ochiladi.`,
    });
    if (!ok) return;
    try {
      const res = await api.delete<ReloadResult>(`/squid/blocklists/${s.id}?remove_rules=1`);
      notifyResult(toast, res.data, `"${s.name}" o'chirildi`);
      await load();
    } catch (err) {
      toast.error(errorMessage(err, "O'chirib bo'lmadi"));
    }
  }

  return (
    <div>
      <PageHeader
        icon={<CloudDownload size={19} />}
        title="Blocklist manbalari"
        description="Tashqi domen ro'yxatlarini avtomatik yuklab, yangilab turadi. Har bir ro'yxat uchun bloklash qoidasi yaratiladi. Ichki manzillarga (localhost, bulut metadata) ulanish taqiqlangan."
      />

      <Card className="mb-6 p-5">
        <form onSubmit={create} className="grid gap-4">
          <div className="grid gap-4 sm:grid-cols-[200px_1fr_120px]">
            <div>
              <Label>Nom</Label>
              <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="masalan: ads" className="font-mono" />
            </div>
            <div>
              <Label>Manzil (URL)</Label>
              <Input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://…/hosts" className="font-mono" />
            </div>
            <div>
              <Label>Yangilash (soat)</Label>
              <Input type="number" min={1} max={720} value={hours} onChange={(e) => setHours(e.target.value)} />
            </div>
          </div>
          <div className="flex flex-wrap items-center justify-between gap-3">
            <label className="flex items-center gap-2 text-xs text-neutral-400">
              <input type="checkbox" checked={addRule} onChange={(e) => setAddRule(e.target.checked)} />
              Bloklash qoidasini avtomatik qo'shish
            </label>
            <Button type="submit" disabled={busy} icon={<Plus size={15} />}>
              {busy ? "Yuklanmoqda…" : "Qo'shish va yuklash"}
            </Button>
          </div>
        </form>

        <div className="mt-4 border-t border-white/[0.06] pt-4">
          <div className="mb-2 text-[11px] font-medium uppercase tracking-wider text-neutral-500">Namuna manbalar</div>
          <div className="flex flex-wrap gap-2">
            {suggestions.map((s) => (
              <button
                key={s.name}
                type="button"
                onClick={() => {
                  setName(s.name);
                  setUrl(s.url);
                }}
                className="flex items-center gap-1.5 rounded-lg border border-white/10 px-3 py-1.5 text-xs text-neutral-300 transition-colors hover:bg-white/[0.06]"
                title={s.url}
              >
                <ExternalLink size={12} className="text-neutral-500" />
                {s.name}
                <span className="text-neutral-600">· {s.note}</span>
              </button>
            ))}
          </div>
        </div>
      </Card>

      <Card>
        {sources === null ? (
          <ListSkeleton />
        ) : sources.length === 0 ? (
          <EmptyState
            icon={<CloudDownload size={22} />}
            title="Hozircha blocklist manbasi yo'q"
            description="Yuqoridan URL kiriting yoki namunalardan birini tanlang"
          />
        ) : (
          <ul className="divide-y divide-white/[0.06]">
            {sources.map((s) => {
              const failed = s.last_status.startsWith("error");
              return (
                <li key={s.id} className={`flex flex-wrap items-center gap-4 px-5 py-4 ${s.enabled ? "" : "opacity-60"}`}>
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="text-sm font-medium text-neutral-100">{s.name}</span>
                      <code className="rounded bg-white/[0.06] px-1.5 py-0.5 text-[10px] text-neutral-500">{s.acl_name}</code>
                      <span
                        className={`rounded-full px-2 py-0.5 text-[10px] font-medium ${
                          failed ? "bg-red-500/15 text-red-300" : s.last_fetched ? "bg-emerald-500/15 text-emerald-300" : "bg-white/[0.06] text-neutral-400"
                        }`}
                      >
                        {failed ? "xato" : s.last_fetched ? `${s.entry_count.toLocaleString()} ta domen` : "hali yuklanmagan"}
                      </span>
                    </div>
                    <div className="mt-1 truncate font-mono text-xs text-neutral-500" title={s.url}>
                      {s.url}
                    </div>
                    <div className={`mt-1 text-xs ${failed ? "text-red-300/90" : "text-neutral-500"}`}>
                      {s.last_fetched ? `Oxirgi yuklash: ${timeAgo(new Date(s.last_fetched * 1000).toISOString())}` : ""}
                      {failed && ` · ${s.last_status.replace("error: ", "")}`}
                    </div>
                  </div>

                  <div className="flex flex-wrap items-center gap-3">
                    <label className="flex items-center gap-1.5 text-xs text-neutral-500">
                      har
                      <input
                        type="number"
                        min={1}
                        max={720}
                        defaultValue={s.interval_hours}
                        onBlur={(e) => changeInterval(s, e.target.value)}
                        className="w-16 rounded-md border border-white/10 bg-white/[0.03] px-2 py-1 text-xs text-neutral-100 outline-none"
                      />
                      soat
                    </label>
                    <Switch on={s.enabled} onClick={() => toggle(s)} />
                    <Button
                      variant="secondary"
                      disabled={refreshing === s.id}
                      onClick={() => refresh(s)}
                      icon={<RefreshCw size={13} className={refreshing === s.id ? "animate-spin" : ""} />}
                      className="!px-3 !py-1.5 text-xs"
                    >
                      Hozir yangilash
                    </Button>
                    <button
                      onClick={() => remove(s)}
                      className="rounded-md p-1.5 text-red-400/80 hover:bg-red-500/10 hover:text-red-400"
                      aria-label="O'chirish"
                    >
                      <Trash2 size={15} />
                    </button>
                  </div>
                </li>
              );
            })}
          </ul>
        )}
      </Card>
    </div>
  );
}
