import {
  Activity,
  BarChart3,
  Database,
  Gauge,
  HardDrive,
  RefreshCw,
  RotateCw,
  ScrollText,
  ShieldBan,
  TriangleAlert,
  Users as UsersIcon,
  Zap,
} from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { useConfirm } from "../components/ConfirmDialog";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";
import { EmptyState } from "../components/ui/EmptyState";
import { PageHeader } from "../components/ui/PageHeader";
import { Skeleton } from "../components/ui/Skeleton";
import { StatCard } from "../components/ui/StatCard";
import { TimelineChart } from "../components/ui/TimelineChart";
import { api, errorMessage } from "../lib/api";
import { useAuth } from "../lib/auth";
import { formatBytes } from "../lib/format";
import {
  agoFromUnix,
  type DeniedRow,
  formatCount,
  formatPercent,
  formatSeconds,
  formatUnix,
  type Live,
  type LogFile,
  type RangeName,
  ranges,
  type Summary,
  type TopRow,
} from "../lib/monitor";
import { useToast } from "../lib/toast";

const LIVE_EVERY_MS = 10_000;
const STATS_EVERY_MS = 30_000;

export default function Stats() {
  const [range, setRange] = useState<RangeName>("24h");
  const [summary, setSummary] = useState<Summary | null>(null);
  const [failed, setFailed] = useState<string | null>(null);
  const [tick, setTick] = useState(0);

  useEffect(() => {
    let cancelled = false;
    const load = () =>
      api
        .get<Summary>("/monitor/summary", { params: { range } })
        .then((r) => {
          if (!cancelled) {
            setSummary(r.data);
            setFailed(null);
          }
        })
        .catch((e) => {
          if (!cancelled) setFailed(errorMessage(e, "Statistikani yuklab bo'lmadi"));
        });
    load();
    const id = setInterval(load, STATS_EVERY_MS);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [range, tick]);

  return (
    <div>
      <PageHeader
        icon={<BarChart3 size={20} />}
        title="Statistika"
        description="Squid access.log dan hisoblangan trafik, bloklashlar va keshdan foydalanish. Ma'lumotlar har 30 soniyada yangilanadi."
        action={
          <div className="flex items-center gap-2">
            <div className="flex rounded-lg border border-white/10 bg-white/[0.03] p-0.5">
              {ranges.map((r) => (
                <button
                  key={r.value}
                  type="button"
                  onClick={() => setRange(r.value)}
                  className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
                    range === r.value ? "bg-indigo-500/20 text-white" : "text-neutral-400 hover:text-neutral-100"
                  }`}
                >
                  {r.label}
                </button>
              ))}
            </div>
            <Button variant="secondary" icon={<RefreshCw size={14} />} onClick={() => setTick((t) => t + 1)}>
              Yangilash
            </Button>
          </div>
        }
      />

      {failed && (
        <div className="mb-5 rounded-lg border border-red-500/20 bg-red-500/10 px-4 py-3 text-sm text-red-300">{failed}</div>
      )}

      <Tiles summary={summary} />

      <Card className="mb-6 p-5">
        <h3 className="mb-4 text-sm font-semibold text-white">So'rovlar</h3>
        {summary ? (
          summary.requests === 0 ? (
            <EmptyState
              icon={<Activity size={20} />}
              title="Bu davrda so'rov yo'q"
              description="Squid orqali trafik o'tganda bu yerda ko'rinadi."
            />
          ) : (
            <TimelineChart buckets={summary.timeline} range={range} />
          )
        ) : (
          <Skeleton className="h-56 w-full" />
        )}
      </Card>

      <div className="mb-6 grid gap-6 lg:grid-cols-3">
        <TopCard title="Eng ko'p so'ralgan saytlar" by="domain" range={range} tick={tick} />
        <TopCard title="Eng faol qurilmalar" by="client" range={range} tick={tick} />
        <TopCard title="Eng faol foydalanuvchilar" by="user" range={range} tick={tick} />
      </div>

      <DeniedCard tick={tick} />
      <LiveCard />
      <LogsCard />
    </div>
  );
}

// -------------------------------------------------------------------- tiles

function Tiles({ summary }: { summary: Summary | null }) {
  const s = summary;
  const cell = (v: string | null | undefined) => (s ? v : <Skeleton className="h-7 w-20" />);
  return (
    <div className="mb-6 grid grid-cols-2 gap-4 lg:grid-cols-3 xl:grid-cols-6">
      <StatCard icon={<Activity size={18} />} label="So'rovlar" value={cell(s && formatCount(s.requests))} />
      <StatCard icon={<Database size={18} />} tone="neutral" label="Trafik" value={cell(s && (s.bytes ? formatBytes(s.bytes) : "0 B"))} />
      <StatCard
        icon={<Zap size={18} />}
        tone="green"
        label="Keshdan"
        value={cell(s && formatPercent(s.hits, s.requests))}
        hint={s && s.hit_bytes ? `${formatBytes(s.hit_bytes)} tejaldi` : undefined}
      />
      <StatCard
        icon={<ShieldBan size={18} />}
        tone="amber"
        label="Bloklangan"
        value={cell(s && formatCount(s.denied))}
        hint={s ? formatPercent(s.denied, s.requests) + " so'rovlar" : undefined}
      />
      <StatCard
        icon={<TriangleAlert size={18} />}
        tone={s && s.errors > 0 ? "red" : "neutral"}
        label="Xatolar (5xx)"
        value={cell(s && formatCount(s.errors))}
      />
      <StatCard
        icon={<UsersIcon size={18} />}
        tone="neutral"
        label="Qurilmalar"
        value={cell(s && formatCount(s.clients))}
        hint={s ? `${formatCount(s.domains)} sayt` : undefined}
      />
    </div>
  );
}

// ---------------------------------------------------------------- top lists

type SortKey = "requests" | "bytes" | "denied";
const sortLabels: Record<SortKey, string> = { requests: "So'rovlar", bytes: "Trafik", denied: "Bloklangan" };

function TopCard({ title, by, range, tick }: { title: string; by: "domain" | "client" | "user"; range: RangeName; tick: number }) {
  const [sort, setSort] = useState<SortKey>("requests");
  const [rows, setRows] = useState<TopRow[] | null>(null);

  useEffect(() => {
    let cancelled = false;
    api
      .get<{ rows: TopRow[] }>("/monitor/top", { params: { range, by, sort, limit: 8 } })
      .then((r) => !cancelled && setRows(r.data.rows))
      .catch(() => !cancelled && setRows([]));
    return () => {
      cancelled = true;
    };
  }, [range, by, sort, tick]);

  const value = (r: TopRow) => (sort === "bytes" ? r.bytes : sort === "denied" ? r.denied : r.requests);
  const max = Math.max(1, ...(rows ?? []).map(value));

  return (
    <Card className="p-5">
      <div className="mb-4 flex items-center justify-between gap-2">
        <h3 className="text-sm font-semibold text-white">{title}</h3>
        <select
          value={sort}
          onChange={(e) => setSort(e.target.value as SortKey)}
          aria-label="Saralash"
          className="rounded-md border border-white/10 bg-white/[0.03] px-2 py-1 text-xs text-neutral-300 outline-none"
        >
          {(Object.keys(sortLabels) as SortKey[]).map((k) => (
            <option key={k} value={k} className="bg-[#14171f]">
              {sortLabels[k]}
            </option>
          ))}
        </select>
      </div>
      {rows === null ? (
        <Skeleton className="h-40 w-full" />
      ) : rows.length === 0 ? (
        <p className="py-8 text-center text-xs text-neutral-500">Ma'lumot yo'q</p>
      ) : (
        <ul className="space-y-2.5">
          {rows.map((r) => (
            <li key={r.key}>
              <div className="flex items-baseline justify-between gap-3 text-xs">
                <span className="truncate text-neutral-200" title={r.key}>
                  {r.key === "-" ? "(nomaʼlum)" : r.key}
                </span>
                <span className="shrink-0 tabular-nums text-neutral-400">
                  {sort === "bytes" ? formatBytes(r.bytes) : formatCount(value(r))}
                </span>
              </div>
              <div className="mt-1 h-1.5 overflow-hidden rounded-full bg-white/[0.05]">
                <div className="h-full rounded-full bg-[#3987e5]" style={{ width: `${Math.max(2, (value(r) / max) * 100)}%` }} />
              </div>
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}

// ------------------------------------------------------------ blocked list

function DeniedCard({ tick }: { tick: number }) {
  const [rows, setRows] = useState<DeniedRow[] | null>(null);
  useEffect(() => {
    let cancelled = false;
    api
      .get<{ rows: DeniedRow[] }>("/monitor/denied", { params: { limit: 15 } })
      .then((r) => !cancelled && setRows(r.data.rows))
      .catch(() => !cancelled && setRows([]));
    return () => {
      cancelled = true;
    };
  }, [tick]);

  return (
    <Card className="mb-6 p-5">
      <h3 className="mb-1 text-sm font-semibold text-white">So'nggi bloklangan so'rovlar</h3>
      <p className="mb-4 text-xs text-neutral-500">Qaysi qoida bloklaganini “Kirish qoidalari” sahifasidagi tekshiruvchi orqali bilib olish mumkin.</p>
      {rows === null ? (
        <Skeleton className="h-24 w-full" />
      ) : rows.length === 0 ? (
        <EmptyState icon={<ShieldBan size={20} />} title="Bloklangan so'rov yo'q" />
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-left text-xs">
            <thead className="text-neutral-500">
              <tr>
                <th className="py-1.5 pr-4 font-medium">Vaqt</th>
                <th className="py-1.5 pr-4 font-medium">Qurilma</th>
                <th className="py-1.5 pr-4 font-medium">Foydalanuvchi</th>
                <th className="py-1.5 pr-4 font-medium">Usul</th>
                <th className="py-1.5 font-medium">Manzil</th>
              </tr>
            </thead>
            <tbody className="text-neutral-300">
              {rows.map((r, i) => (
                <tr key={`${r.time}-${i}`} className="border-t border-white/[0.05]">
                  <td className="whitespace-nowrap py-1.5 pr-4 text-neutral-400">{formatUnix(r.time)}</td>
                  <td className="py-1.5 pr-4 font-mono">{r.client}</td>
                  <td className="py-1.5 pr-4">{r.user && r.user !== "-" ? r.user : "—"}</td>
                  <td className="py-1.5 pr-4">{r.method}</td>
                  <td className="max-w-[28rem] truncate py-1.5 font-mono" title={r.url}>
                    {r.url}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}

// -------------------------------------------------------------------- live

function Meter({ percent, warn = 85 }: { percent: number; warn?: number }) {
  const p = Math.min(100, Math.max(0, percent));
  return (
    <div className="h-1.5 overflow-hidden rounded-full bg-white/[0.06]" role="progressbar" aria-valuenow={Math.round(p)} aria-valuemin={0} aria-valuemax={100}>
      <div className={`h-full rounded-full ${p >= warn ? "bg-amber-500" : "bg-indigo-500"}`} style={{ width: `${p}%` }} />
    </div>
  );
}

function LiveCard() {
  const [live, setLive] = useState<Live | null>(null);
  useEffect(() => {
    let cancelled = false;
    const load = () =>
      api
        .get<Live>("/monitor/live")
        .then((r) => !cancelled && setLive(r.data))
        .catch(() => {});
    load();
    const id = setInterval(load, LIVE_EVERY_MS);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, []);

  const info = live?.info;
  const fact = (label: string, value: string) => (
    <div>
      <dt className="text-xs text-neutral-500">{label}</dt>
      <dd className="mt-0.5 text-sm tabular-nums text-neutral-100">{value}</dd>
    </div>
  );

  return (
    <Card className="mb-6 p-5">
      <div className="mb-4 flex items-center justify-between gap-3">
        <h3 className="flex items-center gap-2 text-sm font-semibold text-white">
          <Gauge size={16} className="text-indigo-400" />
          Squid hozir
        </h3>
        {live && (
          <span className={`rounded-full px-2.5 py-0.5 text-xs font-medium ${live.squid.running ? "bg-emerald-500/15 text-emerald-300" : "bg-red-500/15 text-red-300"}`}>
            {live.squid.running ? "ishlayapti" : "to'xtagan"}
          </span>
        )}
      </div>

      {!live ? (
        <Skeleton className="h-28 w-full" />
      ) : (
        <>
          {info?.available ? (
            <dl className="grid grid-cols-2 gap-x-6 gap-y-4 sm:grid-cols-3 lg:grid-cols-4">
              {fact("Versiya", info.version || "—")}
              {fact("Ishlash vaqti", formatSeconds(info.uptime_seconds))}
              {fact("Faol mijozlar", formatCount(info.clients))}
              {fact("So'rov / daqiqa", info.avg_per_minute.toFixed(1))}
              {fact("Keshdan (5 / 60 daqiqa)", `${info.hit_ratio_5m.toFixed(1)}% / ${info.hit_ratio_60m.toFixed(1)}%`)}
              {fact("Kesh trafigi (5 / 60)", `${info.byte_hit_ratio_5m.toFixed(1)}% / ${info.byte_hit_ratio_60m.toFixed(1)}%`)}
              {fact("Xotira (RSS)", formatBytes(info.rss_kb * 1024))}
              {fact("CPU", `${info.cpu_pct.toFixed(2)}%`)}
              {fact("Fayl deskriptorlar", info.fd_max ? `${info.fd_used} / ${info.fd_max}` : "—")}
              {fact("Keshdagi obyektlar (xotira)", formatBytes(info.mem_kb * 1024))}
              {fact("Diskli kesh", info.swap_kb ? formatBytes(info.swap_kb * 1024) : "yo'q")}
            </dl>
          ) : (
            <p className="rounded-lg border border-amber-500/20 bg-amber-500/10 px-3 py-2 text-xs text-amber-200">
              Squid'ning jonli ma'lumotlarini olib bo'lmadi{info?.error ? `: ${info.error}` : ""}. Statistika jadvallari access.log dan olinadi va bunga bog'liq emas.
            </p>
          )}

          <div className="mt-5 grid gap-5 border-t border-white/[0.06] pt-4 md:grid-cols-2">
            <div>
              <h4 className="mb-2 flex items-center gap-1.5 text-xs font-medium text-neutral-400">
                <HardDrive size={13} /> Disk
              </h4>
              {live.disks.length === 0 ? (
                <p className="text-xs text-neutral-500">Disk ma'lumoti mavjud emas</p>
              ) : (
                <ul className="space-y-3">
                  {live.disks.map((d) => (
                    <li key={d.path}>
                      <div className="mb-1 flex justify-between gap-3 text-xs">
                        <span className="truncate font-mono text-neutral-300" title={d.path}>
                          {d.path}
                        </span>
                        <span className="shrink-0 tabular-nums text-neutral-400">
                          {d.used_percent.toFixed(0)}% · {formatBytes(d.free_bytes)} bo'sh
                        </span>
                      </div>
                      <Meter percent={d.used_percent} warn={90} />
                    </li>
                  ))}
                </ul>
              )}
            </div>
            <div>
              <h4 className="mb-2 flex items-center gap-1.5 text-xs font-medium text-neutral-400">
                <ScrollText size={13} /> Log o'quvchi
              </h4>
              {live.reader.readable && !live.reader.last_error ? (
                <p className="text-xs leading-relaxed text-neutral-400">
                  access.log o'qilmoqda · so'nggi so'rov {agoFromUnix(live.reader.last_entry)} · panel ishga tushgandan beri {formatCount(live.reader.lines)} ta yozuv hisoblandi.
                </p>
              ) : (
                <p className="rounded-lg border border-red-500/20 bg-red-500/10 px-3 py-2 text-xs text-red-300">
                  access.log ni o'qib bo'lmayapti{live.reader.last_error ? `: ${live.reader.last_error}` : ""}. Statistika yangilanmaydi.
                </p>
              )}
            </div>
          </div>
        </>
      )}
    </Card>
  );
}

// -------------------------------------------------------------------- logs

function LogsCard() {
  const { can } = useAuth();
  const toast = useToast();
  const confirm = useConfirm();
  const [files, setFiles] = useState<LogFile[] | null>(null);
  const [dir, setDir] = useState("");
  const [open, setOpen] = useState<string | null>(null);
  const [lines, setLines] = useState<string[] | null>(null);
  const [rotating, setRotating] = useState(false);

  const loadFiles = useCallback(() => {
    api
      .get<{ dir: string; files: LogFile[] }>("/monitor/logs")
      .then((r) => {
        setFiles(r.data.files);
        setDir(r.data.dir);
      })
      .catch((e) => {
        setFiles([]);
        toast.error(errorMessage(e, "Log fayllarini yuklab bo'lmadi"));
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(loadFiles, [loadFiles]);

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    setLines(null);
    api
      .get<{ lines: string[] }>(`/monitor/logs/${encodeURIComponent(open)}`, { params: { limit: 200 } })
      .then((r) => !cancelled && setLines(r.data.lines))
      .catch((e) => {
        if (!cancelled) {
          setLines([]);
          toast.error(errorMessage(e, "Faylni o'qib bo'lmadi"));
        }
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  async function rotate() {
    const ok = await confirm({
      title: "Loglarni aylantirish",
      description:
        "Squid log fayllarini yopib qayta ochadi. logfile_rotate noldan katta bo'lsa eski loglar access.log.0, .1 ... nomi bilan saqlanadi; nol bo'lsa (Debian'da odatiy) faqat qayta ochiladi, nomini tizim logrotate o'zgartiradi. Statistika bundan buzilmaydi.",
      confirmLabel: "Aylantirish",
    });
    if (!ok) return;
    setRotating(true);
    try {
      await api.post("/monitor/logs/rotate");
      toast.success("Loglar aylantirildi");
      loadFiles();
    } catch (e) {
      toast.error(errorMessage(e, "Aylantirib bo'lmadi"));
    } finally {
      setRotating(false);
    }
  }

  return (
    <Card className="p-5">
      <div className="mb-4 flex items-start justify-between gap-3">
        <div>
          <h3 className="flex items-center gap-2 text-sm font-semibold text-white">
            <ScrollText size={16} className="text-indigo-400" />
            Log fayllar
          </h3>
          {dir && <p className="mt-1 font-mono text-xs text-neutral-500">{dir}</p>}
        </div>
        {can("admin") && (
          <Button variant="secondary" icon={<RotateCw size={14} />} onClick={rotate} disabled={rotating}>
            Aylantirish
          </Button>
        )}
      </div>

      {files === null ? (
        <Skeleton className="h-16 w-full" />
      ) : files.length === 0 ? (
        <p className="py-4 text-center text-xs text-neutral-500">Log fayllar topilmadi</p>
      ) : (
        <ul className="divide-y divide-white/[0.05]">
          {files.map((f) => (
            <li key={f.name}>
              <button
                type="button"
                onClick={() => setOpen(open === f.name ? null : f.name)}
                className={`flex w-full items-center justify-between gap-3 px-2 py-2 text-left text-xs transition-colors hover:bg-white/[0.04] ${open === f.name ? "bg-white/[0.04]" : ""}`}
              >
                <span className="font-mono text-neutral-200">{f.name}</span>
                <span className="text-neutral-500">
                  {formatBytes(f.size)} · {agoFromUnix(f.modified)}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}

      {open && (
        <div className="mt-4">
          <div className="mb-2 flex items-center justify-between text-xs text-neutral-500">
            <span>{open} — oxirgi 200 qator</span>
            <button type="button" className="hover:text-neutral-300" onClick={() => setOpen(null)}>
              Yopish
            </button>
          </div>
          <pre className="max-h-80 overflow-auto rounded-lg border border-white/[0.06] bg-black/30 p-3 font-mono text-[11px] leading-relaxed text-neutral-300">
            {lines === null ? "Yuklanmoqda…" : lines.length === 0 ? "(bo'sh)" : lines.join("\n")}
          </pre>
        </div>
      )}
    </Card>
  );
}
