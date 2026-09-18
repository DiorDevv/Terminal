import {
  Ban,
  Clock,
  FileCode2,
  LayoutDashboard,
  LogOut,
  Network,
  ScrollText,
  Settings as SettingsIcon,
  ShieldCheck,
  Users as UsersIcon,
} from "lucide-react";
import type { ReactNode } from "react";
import { NavLink } from "react-router-dom";
import { useAuth } from "../lib/auth";
import { useSquidStatus } from "../lib/useSquidStatus";

const navItems = [
  { to: "/", label: "Dashboard", icon: LayoutDashboard, end: true },
  { to: "/blacklist", label: "Bloklangan domenlar", icon: Ban },
  { to: "/restrictions", label: "Vaqt cheklovlari", icon: Clock },
  { to: "/groups", label: "IP guruhlari", icon: Network },
  { to: "/users", label: "Foydalanuvchilar", icon: UsersIcon },
  { to: "/config", label: "Konfiguratsiya", icon: FileCode2 },
  { to: "/logs", label: "Loglar", icon: ScrollText },
  { to: "/settings", label: "Sozlamalar", icon: SettingsIcon },
];

export default function Layout({ children }: { children: ReactNode }) {
  const { logout } = useAuth();
  const status = useSquidStatus();

  return (
    <div className="flex min-h-screen">
      <aside className="flex w-64 shrink-0 flex-col border-r border-white/[0.08] bg-[#0b0c11]/60 p-4 backdrop-blur-xl">
        <div className="mb-7 flex items-center gap-2.5 px-2 pt-1">
          <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-gradient-to-br from-indigo-500 to-violet-600 shadow-[0_4px_16px_-4px_rgba(99,102,241,0.6)]">
            <ShieldCheck size={19} className="text-white" strokeWidth={2.25} />
          </div>
          <div>
            <h1 className="text-sm font-semibold leading-tight text-white">
              Squid Admin
            </h1>
            <p className="text-[11px] leading-tight text-neutral-500">
              Proxy boshqaruv paneli
            </p>
          </div>
        </div>

        <nav className="flex flex-col gap-0.5">
          {navItems.map((item) => {
            const Icon = item.icon;
            return (
              <NavLink
                key={item.to}
                to={item.to}
                end={item.end}
                className={({ isActive }) =>
                  `group flex items-center gap-2.5 rounded-lg px-3 py-2 text-[13px] font-medium transition-all duration-150 ${
                    isActive
                      ? "bg-gradient-to-r from-indigo-500/15 to-violet-500/10 text-white ring-1 ring-inset ring-indigo-500/25"
                      : "text-neutral-400 hover:bg-white/[0.05] hover:text-neutral-100"
                  }`
                }
              >
                {({ isActive }) => (
                  <>
                    <Icon
                      size={16}
                      className={isActive ? "text-indigo-400" : "text-neutral-500 group-hover:text-neutral-300"}
                    />
                    {item.label}
                  </>
                )}
              </NavLink>
            );
          })}
        </nav>

        <div className="mt-auto flex flex-col gap-2 pt-4">
          <div className="flex items-center gap-2.5 rounded-lg border border-white/[0.08] bg-white/[0.02] px-3 py-2.5">
            <span className="relative flex h-2 w-2">
              {status?.running && (
                <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75" />
              )}
              <span
                className={`relative inline-flex h-2 w-2 rounded-full ${
                  status?.running
                    ? "bg-emerald-500"
                    : status
                      ? "bg-red-500"
                      : "bg-neutral-600"
                }`}
              />
            </span>
            <span className="text-xs font-medium text-neutral-400">
              {status?.running
                ? "Squid ishlamoqda"
                : status
                  ? "Squid ishlamayapti"
                  : "Tekshirilmoqda..."}
            </span>
          </div>

          <button
            onClick={logout}
            className="flex items-center gap-2.5 rounded-lg px-3 py-2 text-left text-[13px] font-medium text-neutral-500 transition-colors hover:bg-white/[0.05] hover:text-neutral-200"
          >
            <LogOut size={16} />
            Chiqish
          </button>
        </div>
      </aside>
      <main className="flex-1 overflow-y-auto p-8">
        <div className="mx-auto max-w-5xl">{children}</div>
      </main>
    </div>
  );
}
