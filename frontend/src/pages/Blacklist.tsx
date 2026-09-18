import { Ban, Globe, Plus, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { useConfirm } from "../components/ConfirmDialog";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";
import { EmptyState } from "../components/ui/EmptyState";
import { Input } from "../components/ui/Input";
import { ListSkeleton } from "../components/ui/Skeleton";
import { PageHeader } from "../components/ui/PageHeader";
import { api } from "../lib/api";
import { useToast } from "../lib/toast";

export default function Blacklist() {
  const [domains, setDomains] = useState<string[]>([]);
  const [input, setInput] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const toast = useToast();
  const confirm = useConfirm();

  async function load() {
    const res = await api.get<{ domains: string[] | null }>("/squid/blacklist");
    setDomains(res.data.domains ?? []);
  }

  useEffect(() => {
    load()
      .catch((err) => toast.error(err.response?.data?.error ?? "Yuklab bo'lmadi"))
      .finally(() => setLoading(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function addDomain(e: React.FormEvent) {
    e.preventDefault();
    if (!input.trim()) return;
    setBusy(true);
    try {
      await api.post("/squid/blacklist", { domain: input.trim() });
      toast.success(`${input.trim()} bloklandi`);
      setInput("");
      await load();
    } catch (err: any) {
      toast.error(err.response?.data?.error ?? "Qo'shib bo'lmadi");
    } finally {
      setBusy(false);
    }
  }

  async function removeDomain(domain: string) {
    const ok = await confirm({
      title: "Domenni o'chirish",
      description: `"${domain}" bloklamadan olib tashlansinmi? Bu domenga kirish qayta ochiladi.`,
    });
    if (!ok) return;

    setBusy(true);
    try {
      await api.delete(`/squid/blacklist/${encodeURIComponent(domain.replace(/^\./, ""))}`);
      toast.success(`${domain} bloklamadan olib tashlandi`);
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
        icon={<Ban size={19} />}
        title="Bloklangan domenlar"
        description="Bu domenlarga hech kim (localhost ham) kira olmaydi"
      />

      <form onSubmit={addDomain} className="mb-4 flex gap-2">
        <Input
          value={input}
          onChange={(e) => setInput(e.target.value)}
          placeholder="masalan: example.com"
        />
        <Button type="submit" disabled={busy} icon={<Plus size={16} />}>
          Qo'shish
        </Button>
      </form>

      <Card>
        {loading ? (
          <ListSkeleton />
        ) : domains.length === 0 ? (
          <EmptyState
            icon={<Globe size={22} />}
            title="Hozircha bloklangan domen yo'q"
            description="Yuqoridagi maydonga domen kiritib qo'shing"
          />
        ) : (
          <ul className="divide-y divide-white/[0.06]">
            {domains.map((domain) => (
              <li
                key={domain}
                className="flex items-center justify-between px-5 py-3.5 text-sm"
              >
                <span className="font-mono text-neutral-300">{domain}</span>
                <button
                  onClick={() => removeDomain(domain)}
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
