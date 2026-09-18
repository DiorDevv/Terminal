import { Clock, Plus, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { useConfirm } from "../components/ConfirmDialog";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";
import { EmptyState } from "../components/ui/EmptyState";
import { Input, Label, Select } from "../components/ui/Input";
import { PageHeader } from "../components/ui/PageHeader";
import { ListSkeleton } from "../components/ui/Skeleton";
import { api } from "../lib/api";
import { useToast } from "../lib/toast";

interface Restriction {
  id: number;
  name: string;
  domains: string[];
  days: string[];
  start_time: string;
  end_time: string;
  exempt_cidrs: string[];
  exempt_group_id?: number;
  exempt_group_name?: string;
}

interface Group {
  id: number;
  name: string;
  members: string[];
}

const DAY_OPTIONS: { code: string; label: string }[] = [
  { code: "M", label: "Dush" },
  { code: "T", label: "Sesh" },
  { code: "W", label: "Chor" },
  { code: "H", label: "Pay" },
  { code: "F", label: "Jum" },
  { code: "A", label: "Shan" },
  { code: "S", label: "Yak" },
];

const dayLabel = (code: string) =>
  DAY_OPTIONS.find((d) => d.code === code)?.label ?? code;

export default function Restrictions() {
  const [items, setItems] = useState<Restriction[]>([]);
  const [groups, setGroups] = useState<Group[]>([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const toast = useToast();
  const confirm = useConfirm();

  const [name, setName] = useState("");
  const [domains, setDomains] = useState("");
  const [days, setDays] = useState<string[]>([]);
  const [startTime, setStartTime] = useState("09:00");
  const [endTime, setEndTime] = useState("18:00");
  const [exemptCidrs, setExemptCidrs] = useState("");
  const [exemptGroupId, setExemptGroupId] = useState("");

  async function load() {
    const [restrictionsRes, groupsRes] = await Promise.all([
      api.get<{ restrictions: Restriction[] | null }>("/squid/restrictions"),
      api.get<{ groups: Group[] | null }>("/squid/groups"),
    ]);
    setItems(restrictionsRes.data.restrictions ?? []);
    setGroups(groupsRes.data.groups ?? []);
  }

  useEffect(() => {
    load()
      .catch((err) => toast.error(err.response?.data?.error ?? "Yuklab bo'lmadi"))
      .finally(() => setLoading(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function toggleDay(code: string) {
    setDays((prev) =>
      prev.includes(code) ? prev.filter((d) => d !== code) : [...prev, code],
    );
  }

  async function create(e: React.FormEvent) {
    e.preventDefault();
    if (!name.trim() || !domains.trim() || days.length === 0) {
      toast.error("Nom, domenlar va kamida bitta kun kerak");
      return;
    }

    setBusy(true);
    try {
      await api.post("/squid/restrictions", {
        name: name.trim(),
        domains: domains.split(",").map((d) => d.trim()).filter(Boolean),
        days,
        start_time: startTime,
        end_time: endTime,
        ...(exemptGroupId
          ? { exempt_group_id: Number(exemptGroupId) }
          : {
              exempt_cidrs: exemptCidrs
                .split(",")
                .map((d) => d.trim())
                .filter(Boolean),
            }),
      });
      toast.success(`"${name.trim()}" qoidasi yaratildi`);
      setName("");
      setDomains("");
      setDays([]);
      setExemptCidrs("");
      setExemptGroupId("");
      await load();
    } catch (err: any) {
      toast.error(err.response?.data?.error ?? "Yaratib bo'lmadi");
    } finally {
      setBusy(false);
    }
  }

  async function remove(item: Restriction) {
    const ok = await confirm({
      title: "Qoidani o'chirish",
      description: `"${item.name}" qoidasi o'chirilsinmi?`,
    });
    if (!ok) return;

    setBusy(true);
    try {
      await api.delete(`/squid/restrictions/${item.id}`);
      toast.success(`"${item.name}" o'chirildi`);
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
        icon={<Clock size={19} />}
        title="Vaqt asosidagi cheklovlar"
        description={`Masalan: "ijtimoiy tarmoqlarni dushanbadan jumagacha 09:00-18:00 oralig'ida bloklash, IT bo'limidan tashqari". squid.conf'ni qo'lda tahrirlamasdan shu yerdan boshqaring.`}
      />

      <Card className="mb-6 p-5">
        <form onSubmit={create} className="grid gap-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <div>
              <Label>Qoida nomi</Label>
              <Input
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="ijtimoiy_tarmoq_ish_vaqti"
              />
            </div>
            <div>
              <Label>Domenlar (vergul bilan)</Label>
              <Input
                value={domains}
                onChange={(e) => setDomains(e.target.value)}
                placeholder="youtube.com, facebook.com"
              />
            </div>
          </div>

          <div>
            <Label>Kunlar</Label>
            <div className="flex flex-wrap gap-1.5">
              {DAY_OPTIONS.map((d) => (
                <button
                  key={d.code}
                  type="button"
                  onClick={() => toggleDay(d.code)}
                  className={`rounded-lg border px-3 py-1.5 text-xs font-medium transition-all ${
                    days.includes(d.code)
                      ? "border-indigo-500/50 bg-indigo-500/20 text-indigo-300 ring-1 ring-indigo-500/30"
                      : "border-white/10 text-neutral-400 hover:bg-white/[0.05] hover:text-neutral-200"
                  }`}
                >
                  {d.label}
                </button>
              ))}
            </div>
          </div>

          <div className="grid gap-4 sm:grid-cols-3">
            <div>
              <Label>Boshlanish vaqti</Label>
              <Input
                type="time"
                value={startTime}
                onChange={(e) => setStartTime(e.target.value)}
              />
            </div>
            <div>
              <Label>Tugash vaqti</Label>
              <Input
                type="time"
                value={endTime}
                onChange={(e) => setEndTime(e.target.value)}
              />
            </div>
            <div>
              <Label>Istisno guruhi (ixtiyoriy)</Label>
              <Select
                value={exemptGroupId}
                onChange={(e) => setExemptGroupId(e.target.value)}
              >
                <option value="">Yo'q</option>
                {groups.map((g) => (
                  <option key={g.id} value={g.id}>
                    {g.name}
                  </option>
                ))}
              </Select>
            </div>
          </div>

          {!exemptGroupId && (
            <div>
              <Label>Yoki qo'lda istisno IP'lar (vergul bilan, ixtiyoriy)</Label>
              <Input
                value={exemptCidrs}
                onChange={(e) => setExemptCidrs(e.target.value)}
                placeholder="127.0.0.1, 10.0.0.0/24"
              />
            </div>
          )}

          <Button type="submit" disabled={busy} icon={<Plus size={16} />} className="w-fit">
            Qoida yaratish
          </Button>
        </form>
      </Card>

      <Card>
        {loading ? (
          <ListSkeleton />
        ) : items.length === 0 ? (
          <EmptyState
            icon={<Clock size={22} />}
            title="Hozircha vaqt asosidagi qoida yo'q"
          />
        ) : (
          <ul className="divide-y divide-white/[0.06]">
            {items.map((item) => (
              <li key={item.id} className="flex items-start justify-between gap-4 px-5 py-4">
                <div className="text-sm">
                  <div className="font-medium text-neutral-100">{item.name}</div>
                  <div className="mt-1 font-mono text-xs text-neutral-500">
                    {item.domains.join(", ")}
                  </div>
                  <div className="mt-1.5 flex flex-wrap items-center gap-1.5 text-xs text-neutral-500">
                    <span className="rounded bg-white/[0.05] px-1.5 py-0.5">
                      {item.days.map(dayLabel).join(" ")}
                    </span>
                    <span>
                      {item.start_time}-{item.end_time}
                    </span>
                    {item.exempt_group_name && (
                      <span className="rounded bg-indigo-500/10 px-1.5 py-0.5 text-indigo-300">
                        guruh: {item.exempt_group_name}
                      </span>
                    )}
                    {!item.exempt_group_name && item.exempt_cidrs.length > 0 && (
                      <span>istisno: {item.exempt_cidrs.join(", ")}</span>
                    )}
                  </div>
                </div>
                <button
                  onClick={() => remove(item)}
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
