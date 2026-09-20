import {
  Activity,
  Clock3,
  LayoutDashboard,
  MemoryStick,
  Play,
  Power,
  RefreshCw,
  RotateCw,
  Square,
  Tag,
  Wifi,
  WifiOff,
} from "lucide-react";
import { type ReactNode, useCallback, useEffect, useState } from "react";
import { useConfirm } from "../components/ConfirmDialog";
import { Button } from "../components/ui/Button";
import { PageHeader } from "../components/ui/PageHeader";
import { StatCard } from "../components/ui/StatCard";
import { api } from "../lib/api";
import { useAuth } from "../lib/auth";
import { Link } from "react-router-dom";
import { formatBytes, formatUptime } from "../lib/format";
import { useToast } from "../lib/toast";

interface ServiceInfo {
  manager: "systemd" | "direct";
  running: boolean;
  state: string;
  enabled: boolean | null;
  pid: number;
  memory_bytes: number;
  started_at: number;
  version: string;
  restart_pending: boolean;
}

type Action = "start" | "stop" | "restart" | "reload" | "enable" | "disable";

const successMessages: Record<Action, string> = {
  start: "Squid ishga tushirildi",
  stop: "Squid to'xtatilmoqda (ochiq ulanishlar yopilishini kutmoqda)",
  restart: "Squid qayta ishga tushirilmoqda",
  reload: "Squid konfiguratsiyasi qayta yuklandi",
  enable: "Squid tizim yoqilganda avtomatik ishga tushadi",
  disable: "Avtomatik ishga tushish o'chirildi",
};

const failureMessages: Record<Action, string> = {
  start: "Ishga tushirib bo'lmadi",
  stop: "To'xtatib bo'lmadi",
  restart: "Qayta ishga tushirib bo'lmadi",
  reload: "Reconfigure muvaffaqiyatsiz",
  enable: "Avtomatik ishga tushirishni yoqib bo'lmadi",
  disable: "Avtomatik ishga tushirishni o'chirib bo'lmadi",
};

function isTransitioning(s: ServiceInfo | null): boolean {
  return !!s && /^(activating|deactivating)/.test(s.state);
}

function describeState(s: ServiceInfo | null): {
  label: string;
  tone: "green" | "red" | "amber" | "neutral";
} {
  if (!s) return { label: "...", tone: "neutral" };
  if (s.state.startsWith("activating")) return { label: "Ishga tushmoqda…", tone: "amber" };
  if (s.state.startsWith("deactivating")) return { label: "To'xtamoqda…", tone: "amber" };
  if (s.running) return { label: "Ishlamoqda", tone: "green" };
  if (s.state.startsWith("failed")) return { label: "Xatolik bilan to'xtagan", tone: "red" };
  return { label: "To'xtagan", tone: "red" };
}

function Fact({ icon, label, value }: { icon: ReactNode; label: string; value: string }) {
  return (
    <div className="flex items-center gap-2.5 rounded-lg border border-white/[0.06] bg-black/20 px-3 py-2.5">
      <span className="text-neutral-500">{icon}</span>
      <div className="min-w-0">
        <div className="text-[10px] font-medium uppercase tracking-wider text-neutral-500">
          {label}
        </div>
        <div className="truncate text-sm font-medium text-neutral-200">{value}</div>
      </div>
    </div>
  );
}

function Switch({
  on,
  disabled,
  onClick,
}: {
  on: boolean;
  disabled?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      role="switch"
      aria-checked={on}
      className={`relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition-colors disabled:opacity-50 ${
        on ? "bg-emerald-500" : "bg-white/10"
      }`}
    >
      <span
        className={`inline-block h-4 w-4 transform rounded-full bg-white shadow transition-transform ${
          on ? "translate-x-6" : "translate-x-1"
        }`}
      />
    </button>
  );
}

export default function Dashboard() {
  const [service, setService] = useState<ServiceInfo | null>(null);
  const [lanAllowed, setLanAllowed] = useState<boolean | null>(null);
  const [pending, setPending] = useState<Action | null>(null);
  const [lanBusy, setLanBusy] = useState(false);
  const toast = useToast();
  const confirm = useConfirm();
  const { can } = useAuth();

  const fetchService = useCallback(async () => {
    try {
      const res = await api.get<ServiceInfo>("/squid/service");
      setService(res.data);
    } catch {
      // 401 is handled by the axios interceptor; a transient failure just
      // leaves the last known state until the next poll.
    }
  }, []);

  async function fetchLan() {
    const res = await api.get<{ allowed: boolean }>("/squid/lan-access");
    setLanAllowed(res.data.allowed);
  }

  const transitioning = isTransitioning(service);

  // Poll fast while squid is starting/stopping so the UI follows along.
  useEffect(() => {
    fetchService();
    const id = setInterval(fetchService, transitioning ? 1000 : 5000);
    return () => clearInterval(id);
  }, [fetchService, transitioning]);

  useEffect(() => {
    fetchLan().catch(() => {});
  }, []);

  async function run(action: Action) {
    if (action === "stop" || action === "restart") {
      const ok = await confirm({
        title: action === "stop" ? "Squid'ni to'xtatish" : "Squid'ni qayta ishga tushirish",
        description:
          "Proksi orqali ishlayotgan barcha foydalanuvchilarning ulanishi uziladi. Squid ochiq ulanishlar yopilishini 30 soniyagacha kutishi mumkin.",
        confirmLabel: action === "stop" ? "To'xtatish" : "Qayta ishga tushirish",
      });
      if (!ok) return;
    }

    setPending(action);
    try {
      const res = await api.post<{ service: ServiceInfo }>(`/squid/service/${action}`);
      setService(res.data.service);
      toast.success(successMessages[action]);
    } catch (err: any) {
      if (err.response?.data?.service) setService(err.response.data.service);
      toast.error(err.response?.data?.error ?? failureMessages[action]);
    } finally {
      setPending(null);
    }
  }

  async function toggleLan() {
    if (lanAllowed === null) return;
    setLanBusy(true);
    const next = !lanAllowed;
    try {
      const res = await api.put<{ reloaded: boolean; reload_error?: string }>(
        "/squid/lan-access",
        { allowed: next },
      );
      setLanAllowed(next);
      if (res.data.reloaded === false) {
        toast.error(res.data.reload_error ?? "O'zgarish saqlandi, lekin Squid qayta yuklanmadi");
      } else {
        toast.success(
          next
            ? "LAN qurilmalari uchun proxy ruxsati yoqildi"
            : "LAN qurilmalari uchun proxy ruxsati o'chirildi",
        );
      }
    } catch (err: any) {
      toast.error(err.response?.data?.error ?? "O'zgartirib bo'lmadi");
    } finally {
      setLanBusy(false);
    }
  }

  const state = describeState(service);
  const busy = pending !== null || transitioning;
  const isRunning = !!service?.running;

  return (
    <div>
      <PageHeader
        icon={<LayoutDashboard size={19} />}
        title="Dashboard"
        description="Squid proxy holati va asosiy sozlamalarga umumiy nazar"
      />

      <div className="grid gap-4 sm:grid-cols-2">
        <StatCard
          className="sm:col-span-2"
          icon={<Activity size={17} />}
          tone={state.tone}
          label="Squid holati"
          value={state.label}
          hint={
            service
              ? service.manager === "systemd"
                ? `systemd: ${service.state}`
                : "systemd topilmadi — Squid to'g'ridan-to'g'ri boshqariladi"
              : undefined
          }
        >
          <div className="mt-4 grid grid-cols-2 gap-2.5 lg:grid-cols-4">
            <Fact icon={<Tag size={15} />} label="Versiya" value={service?.version || "—"} />
            <Fact
              icon={<Clock3 size={15} />}
              label="Ishlash vaqti"
              value={isRunning ? formatUptime(service?.started_at ?? 0) : "—"}
            />
            <Fact
              icon={<MemoryStick size={15} />}
              label="Xotira"
              value={isRunning ? formatBytes(service?.memory_bytes ?? 0) : "—"}
            />
            <Fact
              icon={<Activity size={15} />}
              label="PID"
              value={isRunning && service?.pid ? String(service.pid) : "—"}
            />
          </div>

          {service?.restart_pending && (
            <div className="mt-4 flex flex-wrap items-center justify-between gap-2 rounded-lg border border-amber-500/25 bg-amber-500/10 px-3.5 py-2.5 text-sm text-amber-200">
              <span>Saqlangan sozlama Squid qayta ishga tushirilgandan keyin kuchga kiradi.</span>
              <Link to="/squid-settings" className="font-medium text-amber-100 underline underline-offset-2 hover:text-white">
                Sozlamalarga o'tish
              </Link>
            </div>
          )}

          <div className="mt-4 flex flex-wrap items-center gap-2">
            <Button
              onClick={() => run("start")}
              disabled={busy || isRunning || !can("operator")}
              icon={<Play size={14} />}
            >
              Ishga tushirish
            </Button>
            <Button
              onClick={() => run("stop")}
              disabled={busy || !isRunning || !can("admin")}
              variant="secondary"
              icon={<Square size={14} />}
            >
              To'xtatish
            </Button>
            <Button
              onClick={() => run("restart")}
              disabled={busy || !can("admin")}
              variant="secondary"
              icon={<RotateCw size={14} className={pending === "restart" ? "animate-spin" : ""} />}
            >
              Qayta ishga tushirish
            </Button>
            <Button
              onClick={() => run("reload")}
              disabled={busy || !isRunning || !can("operator")}
              variant="ghost"
              icon={<RefreshCw size={14} className={pending === "reload" ? "animate-spin" : ""} />}
            >
              {pending === "reload" ? "Tekshirilmoqda…" : "Reconfigure"}
            </Button>
          </div>

          {service?.manager === "systemd" && (
            <div className="mt-4 flex items-center gap-3 border-t border-white/[0.06] pt-4">
              <Power size={15} className="text-neutral-500" />
              <div className="flex-1 text-sm text-neutral-300">
                Tizim yoqilganda avtomatik ishga tushirish
              </div>
              <Switch
                on={service.enabled === true}
                disabled={busy || service.enabled === null || !can("admin")}
                onClick={() => run(service.enabled ? "disable" : "enable")}
              />
            </div>
          )}
        </StatCard>

        <StatCard
          icon={lanAllowed ? <Wifi size={17} /> : <WifiOff size={17} />}
          tone={lanAllowed ? "green" : "neutral"}
          label="LAN qurilmalari"
          value={
            lanAllowed === null
              ? "..."
              : lanAllowed
                ? "Ruxsat berilgan"
                : "Faqat shu kompyuter"
          }
          hint="Yoqilgan bo'lsa, lokal tarmoqdagi boshqa qurilmalar ham proxy'dan foydalana oladi"
        >
          <div className="mt-4">
            <Switch on={!!lanAllowed} disabled={lanBusy || lanAllowed === null || !can("operator")} onClick={toggleLan} />
          </div>
        </StatCard>
      </div>
    </div>
  );
}
