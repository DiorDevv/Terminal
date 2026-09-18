import {
  Activity,
  LayoutDashboard,
  RefreshCw,
  Wifi,
  WifiOff,
} from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "../components/ui/Button";
import { PageHeader } from "../components/ui/PageHeader";
import { StatCard } from "../components/ui/StatCard";
import { api } from "../lib/api";
import { useToast } from "../lib/toast";

interface Status {
  running: boolean;
  detail: string;
}

export default function Dashboard() {
  const [status, setStatus] = useState<Status | null>(null);
  const [lanAllowed, setLanAllowed] = useState<boolean | null>(null);
  const [busy, setBusy] = useState(false);
  const [lanBusy, setLanBusy] = useState(false);
  const toast = useToast();

  async function fetchStatus() {
    const res = await api.get<Status>("/squid/status");
    setStatus(res.data);
  }

  async function fetchLan() {
    const res = await api.get<{ allowed: boolean }>("/squid/lan-access");
    setLanAllowed(res.data.allowed);
  }

  useEffect(() => {
    fetchStatus();
    fetchLan();
    const id = setInterval(fetchStatus, 5000);
    return () => clearInterval(id);
  }, []);

  async function reconfigure() {
    setBusy(true);
    try {
      await api.post("/squid/reconfigure");
      await fetchStatus();
      toast.success("Squid konfiguratsiyasi qayta yuklandi");
    } catch (err: any) {
      toast.error(err.response?.data?.error ?? "Reconfigure muvaffaqiyatsiz");
    } finally {
      setBusy(false);
    }
  }

  async function toggleLan() {
    if (lanAllowed === null) return;
    setLanBusy(true);
    const next = !lanAllowed;
    try {
      await api.put("/squid/lan-access", { allowed: next });
      setLanAllowed(next);
      toast.success(
        next
          ? "LAN qurilmalari uchun proxy ruxsati yoqildi"
          : "LAN qurilmalari uchun proxy ruxsati o'chirildi",
      );
    } catch (err: any) {
      toast.error(err.response?.data?.error ?? "O'zgartirib bo'lmadi");
    } finally {
      setLanBusy(false);
    }
  }

  return (
    <div>
      <PageHeader
        icon={<LayoutDashboard size={19} />}
        title="Dashboard"
        description="Squid proxy holati va asosiy sozlamalarga umumiy nazar"
      />

      <div className="grid gap-4 sm:grid-cols-2">
        <StatCard
          icon={<Activity size={17} />}
          tone={status?.running ? "green" : "red"}
          label="Squid holati"
          value={
            status ? (status.running ? "Ishlamoqda" : "Ishlamayapti") : "..."
          }
        >
          {status?.detail && (
            <pre className="mt-3 max-h-28 overflow-auto rounded-lg border border-white/[0.06] bg-black/30 p-3 font-mono text-[11px] leading-relaxed text-neutral-400">
              {status.detail}
            </pre>
          )}
          <Button
            onClick={reconfigure}
            disabled={busy}
            variant="secondary"
            icon={<RefreshCw size={14} className={busy ? "animate-spin" : ""} />}
            className="mt-4"
          >
            Reconfigure
          </Button>
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
          <button
            onClick={toggleLan}
            disabled={lanBusy || lanAllowed === null}
            className={`relative mt-4 inline-flex h-6 w-11 items-center rounded-full transition-colors disabled:opacity-50 ${
              lanAllowed ? "bg-emerald-500" : "bg-white/10"
            }`}
          >
            <span
              className={`inline-block h-4 w-4 transform rounded-full bg-white shadow transition-transform ${
                lanAllowed ? "translate-x-6" : "translate-x-1"
              }`}
            />
          </button>
        </StatCard>
      </div>
    </div>
  );
}
