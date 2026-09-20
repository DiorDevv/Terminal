import { KeyRound, Laptop, LogOut, Settings as SettingsIcon } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { PasswordForm } from "../components/PasswordForm";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";
import { IconBadge } from "../components/ui/IconBadge";
import { PageHeader } from "../components/ui/PageHeader";
import { ListSkeleton } from "../components/ui/Skeleton";
import { api, errorMessage } from "../lib/api";
import { useAuth } from "../lib/auth";
import { formatDateTime, timeAgo } from "../lib/format";
import { useToast } from "../lib/toast";

interface Session {
  id: number;
  created_at: string;
  last_seen: string;
  expires_at: string;
  ip: string;
  user_agent: string;
  current: boolean;
}

/** "Chrome on Linux"-style label from a raw User-Agent string. */
function describeAgent(ua: string): string {
  const browser = /Edg\//.test(ua)
    ? "Edge"
    : /Firefox\//.test(ua)
      ? "Firefox"
      : /Chrome\//.test(ua)
        ? "Chrome"
        : /Safari\//.test(ua)
          ? "Safari"
          : /curl|python|node|axios/i.test(ua)
            ? "Skript"
            : "Brauzer";
  const os = /Windows/.test(ua)
    ? "Windows"
    : /Android/.test(ua)
      ? "Android"
      : /iPhone|iPad/.test(ua)
        ? "iOS"
        : /Mac OS/.test(ua)
          ? "macOS"
          : /Linux/.test(ua)
            ? "Linux"
            : "";
  return os ? `${browser} · ${os}` : browser;
}

export default function Settings() {
  const { user } = useAuth();
  const [sessions, setSessions] = useState<Session[] | null>(null);
  const toast = useToast();

  const load = useCallback(async () => {
    const res = await api.get<{ sessions: Session[] }>("/auth/sessions");
    setSessions(res.data.sessions);
  }, []);

  useEffect(() => {
    load().catch((err) => toast.error(errorMessage(err, "Sessiyalarni yuklab bo'lmadi")));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function revoke(s: Session) {
    try {
      await api.delete(`/auth/sessions/${s.id}`);
      toast.success("Sessiya tugatildi");
      await load();
    } catch (err) {
      toast.error(errorMessage(err, "Sessiyani tugatib bo'lmadi"));
    }
  }

  return (
    <div>
      <PageHeader
        icon={<SettingsIcon size={19} />}
        title="Sozlamalar"
        description={user ? `${user.username} · ${user.role}` : undefined}
      />

      <div className="grid gap-5 lg:grid-cols-2">
        <Card className="p-6">
          <div className="mb-5 flex items-center gap-2.5">
            <IconBadge size={32}>
              <KeyRound size={15} />
            </IconBadge>
            <span className="text-sm font-medium text-neutral-200">Parolni o'zgartirish</span>
          </div>
          <PasswordForm onDone={() => load().catch(() => {})} />
          <p className="mt-4 text-xs leading-relaxed text-neutral-500">
            Parol o'zgartirilganda boshqa qurilmalardagi sessiyalaringiz tugatiladi.
          </p>
        </Card>

        <Card className="overflow-hidden">
          <div className="flex items-center gap-2.5 border-b border-white/[0.06] p-6 pb-5">
            <IconBadge size={32}>
              <Laptop size={15} />
            </IconBadge>
            <span className="text-sm font-medium text-neutral-200">Faol sessiyalar</span>
          </div>

          {sessions === null ? (
            <ListSkeleton rows={2} />
          ) : (
            <ul className="divide-y divide-white/[0.06]">
              {sessions.map((s) => (
                <li key={s.id} className="flex items-center justify-between gap-3 px-6 py-3.5">
                  <div className="min-w-0">
                    <div className="flex items-center gap-2 text-sm text-neutral-200">
                      {describeAgent(s.user_agent)}
                      {s.current && (
                        <span className="rounded-full bg-emerald-500/15 px-2 py-0.5 text-[10px] font-medium text-emerald-400">
                          shu qurilma
                        </span>
                      )}
                    </div>
                    <div
                      className="mt-0.5 truncate text-xs text-neutral-500"
                      title={`Kirgan: ${formatDateTime(s.created_at)}`}
                    >
                      {s.ip || "—"} · oxirgi faollik {timeAgo(s.last_seen)}
                    </div>
                  </div>
                  {!s.current && (
                    <Button
                      variant="ghost"
                      onClick={() => revoke(s)}
                      icon={<LogOut size={13} />}
                      className="shrink-0 !px-2.5 !py-1.5 text-xs"
                    >
                      Tugatish
                    </Button>
                  )}
                </li>
              ))}
            </ul>
          )}
        </Card>
      </div>
    </div>
  );
}
