import { FileDiff, History as HistoryIcon, RotateCcw } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useConfirm } from "../components/ConfirmDialog";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";
import { EmptyState } from "../components/ui/EmptyState";
import { PageHeader } from "../components/ui/PageHeader";
import { ListSkeleton, Skeleton } from "../components/ui/Skeleton";
import { api } from "../lib/api";
import { diffLines, diffStats, toHunks } from "../lib/diff";
import { formatDateTime, timeAgo } from "../lib/format";
import { useToast } from "../lib/toast";

interface Version {
  id: number;
  created_at: string;
  comment: string;
  size: number;
  content?: string;
}

const lineStyle = {
  same: "text-neutral-500",
  add: "bg-emerald-500/10 text-emerald-300",
  del: "bg-red-500/10 text-red-300",
} as const;

const marker = { same: " ", add: "+", del: "-" } as const;

export default function History() {
  const [versions, setVersions] = useState<Version[]>([]);
  const [current, setCurrent] = useState<string | null>(null);
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [selected, setSelected] = useState<Version | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadingDiff, setLoadingDiff] = useState(false);
  const [restoring, setRestoring] = useState(false);
  const toast = useToast();
  const confirm = useConfirm();

  const load = useCallback(async () => {
    const [list, conf] = await Promise.all([
      api.get<{ versions: Version[] }>("/squid/history"),
      api.get<{ content: string }>("/squid/config"),
    ]);
    setVersions(list.data.versions);
    setCurrent(conf.data.content);
  }, []);

  useEffect(() => {
    load()
      .catch((err) => toast.error(err.response?.data?.error ?? "Yuklab bo'lmadi"))
      .finally(() => setLoading(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (selectedId === null) return;
    let cancelled = false;
    setLoadingDiff(true);
    api
      .get<{ version: Version }>(`/squid/history/${selectedId}`)
      .then((res) => {
        if (!cancelled) setSelected(res.data.version);
      })
      .catch((err) => toast.error(err.response?.data?.error ?? "Versiyani yuklab bo'lmadi"))
      .finally(() => {
        if (!cancelled) setLoadingDiff(false);
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedId]);

  // What restoring would change: the current config -> the selected version.
  const diff = useMemo(() => {
    if (current === null || selected?.content === undefined) return null;
    const lines = diffLines(current, selected.content);
    return { stats: diffStats(lines), hunks: toHunks(lines) };
  }, [current, selected]);

  async function restore() {
    if (!selected) return;
    const ok = await confirm({
      title: `#${selected.id} versiyaga qaytarish`,
      description:
        "Joriy squid.conf shu versiya bilan almashtiriladi va Squid qayta yuklanadi. Joriy holat tarixda saqlanib qoladi, keyin unga ham qaytish mumkin. Eslatma: cheklovlar keyingi o'zgarishda ma'lumotlar bazasidan qayta yaratiladi.",
      confirmLabel: "Qaytarish",
    });
    if (!ok) return;

    setRestoring(true);
    try {
      const res = await api.post<{ reloaded: boolean; reload_error?: string }>(
        `/squid/history/${selected.id}/restore`,
      );
      if (res.data.reloaded === false) {
        toast.error(res.data.reload_error ?? "Saqlandi, lekin Squid qayta yuklanmadi");
      } else {
        toast.success(`#${selected.id} versiya tiklandi va Squid qayta yuklandi`);
      }
      setSelectedId(null);
      await load();
    } catch (err: any) {
      toast.error(err.response?.data?.error ?? "Qaytarib bo'lmadi");
    } finally {
      setRestoring(false);
    }
  }

  const latestId = versions[0]?.id;

  return (
    <div>
      <PageHeader
        icon={<HistoryIcon size={19} />}
        title="Konfiguratsiya tarixi"
        description="squid.conf'ga kiritilgan har bir o'zgarish saqlanadi. Istalgan versiyani ko'rib, farqini tekshirib, bir tugma bilan qaytarishingiz mumkin."
      />

      <div className="grid gap-4 lg:grid-cols-[300px_1fr]">
        <Card className="overflow-hidden">
          {loading ? (
            <ListSkeleton rows={5} />
          ) : versions.length === 0 ? (
            <EmptyState
              icon={<HistoryIcon size={22} />}
              title="Hozircha tarix yo'q"
              description="Birinchi o'zgarishdan keyin shu yerda paydo bo'ladi"
            />
          ) : (
            <ul className="max-h-[70vh] divide-y divide-white/[0.06] overflow-y-auto">
              {versions.map((v) => (
                <li key={v.id}>
                  <button
                    onClick={() => setSelectedId(v.id)}
                    className={`w-full px-4 py-3 text-left transition-colors ${
                      selectedId === v.id
                        ? "bg-indigo-500/10 ring-1 ring-inset ring-indigo-500/25"
                        : "hover:bg-white/[0.03]"
                    }`}
                  >
                    <div className="flex items-center justify-between gap-2">
                      <span className="font-mono text-xs text-indigo-300">#{v.id}</span>
                      {v.id === latestId && (
                        <span className="rounded-full bg-emerald-500/15 px-2 py-0.5 text-[10px] font-medium text-emerald-400">
                          joriy
                        </span>
                      )}
                    </div>
                    <div className="mt-1 text-[13px] leading-snug text-neutral-200">{v.comment}</div>
                    <div className="mt-1 text-[11px] text-neutral-500" title={formatDateTime(v.created_at)}>
                      {timeAgo(v.created_at)} · {(v.size / 1024).toFixed(1)} KB
                    </div>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </Card>

        <Card className="min-w-0">
          {selectedId === null ? (
            <EmptyState
              icon={<FileDiff size={22} />}
              title="Versiyani tanlang"
              description="Chapdagi ro'yxatdan versiyani bosing: joriy config bilan farqi shu yerda ko'rinadi"
            />
          ) : loadingDiff || !selected || selected.id !== selectedId || !diff ? (
            <div className="p-5">
              <Skeleton className="h-64 w-full" />
            </div>
          ) : (
            <div>
              <div className="flex flex-wrap items-center justify-between gap-3 border-b border-white/[0.06] px-5 py-4">
                <div className="min-w-0">
                  <div className="text-sm font-medium text-white">
                    #{selected.id} · {selected.comment}
                  </div>
                  <div className="mt-0.5 text-xs text-neutral-500">
                    {formatDateTime(selected.created_at)}
                  </div>
                </div>
                <Button
                  onClick={restore}
                  disabled={restoring || diff.hunks.length === 0}
                  icon={<RotateCcw size={14} className={restoring ? "animate-spin" : ""} />}
                >
                  {restoring ? "Qaytarilmoqda…" : "Shu versiyaga qaytarish"}
                </Button>
              </div>

              <div className="border-b border-white/[0.06] px-5 py-2.5 text-xs text-neutral-400">
                Qaytarilsa nima o'zgaradi (joriy → #{selected.id}):{" "}
                <span className="font-medium text-emerald-400">+{diff.stats.added}</span>{" "}
                <span className="font-medium text-red-400">−{diff.stats.removed}</span> qator
              </div>

              {diff.hunks.length === 0 ? (
                <EmptyState
                  icon={<FileDiff size={22} />}
                  title="Farq yo'q"
                  description="Bu versiya joriy konfiguratsiya bilan bir xil"
                />
              ) : (
                <div className="max-h-[62vh] overflow-auto py-2 font-mono text-[12px] leading-relaxed">
                  {diff.hunks.map((hunk, hi) => (
                    <div key={hi} className={hi > 0 ? "mt-2 border-t border-dashed border-white/10 pt-2" : ""}>
                      {hunk.lines.map((l, li) => (
                        <div key={li} className={`flex whitespace-pre px-3 ${lineStyle[l.kind]}`}>
                          <span className="w-10 shrink-0 select-none pr-2 text-right text-neutral-600">
                            {l.oldNo ?? ""}
                          </span>
                          <span className="w-10 shrink-0 select-none pr-2 text-right text-neutral-600">
                            {l.newNo ?? ""}
                          </span>
                          <span className="w-4 shrink-0 select-none">{marker[l.kind]}</span>
                          <span>{l.text}</span>
                        </div>
                      ))}
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}
        </Card>
      </div>
    </div>
  );
}
