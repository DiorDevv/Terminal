import {
  Ban,
  BarChart3,
  Bell,
  ClipboardList,
  CloudDownload,
  Clock,
  FileCode2,
  Gauge,
  History,
  LayoutDashboard,
  LogOut,
  Menu,
  Network,
  ScrollText,
  Settings as SettingsIcon,
  ShieldCheck,
  ShieldHalf,
  SlidersHorizontal,
  UserCog,
  Users as UsersIcon,
} from "lucide-react";
import { type ReactNode, useState } from "react";
import { NavLink } from "react-router-dom";
import { type Role, useAuth } from "../lib/auth";
import { useSquidStatus } from "../lib/useSquidStatus";

const navItems: {
  to: string;
  label: string;
  icon: typeof LayoutDashboard;
  end?: boolean;
  /** Lowest role that sees this entry (the API enforces it as well). */
  min: Role;
}[] = [
  { to: "/", label: "Dashboard", icon: LayoutDashboard, end: true, min: "viewer" },
  { to: "/blacklist", label: "Bloklangan domenlar", icon: Ban, min: "viewer" },
  { to: "/access", label: "Kirish qoidalari", icon: ShieldHalf, min: "viewer" },
  { to: "/blocklists", label: "Blocklist manbalari", icon: CloudDownload, min: "viewer" },
  { to: "/restrictions", label: "Vaqt cheklovlari", icon: Clock, min: "viewer" },
  { to: "/groups", label: "IP guruhlari", icon: Network, min: "viewer" },
  { to: "/users", label: "Proxy foydalanuvchilari", icon: UsersIcon, min: "viewer" },
  { to: "/limits", label: "Cheklovlar", icon: Gauge, min: "operator" },
  { to: "/stats", label: "Statistika", icon: BarChart3, min: "operator" },
  { to: "/logs", label: "Loglar", icon: ScrollText, min: "operator" },
  { to: "/squid-settings", label: "Squid sozlamalari", icon: SlidersHorizontal, min: "operator" },
  { to: "/config", label: "Konfiguratsiya", icon: FileCode2, min: "admin" },
  { to: "/history", label: "Config tarixi", icon: History, min: "admin" },
  { to: "/panel-users", label: "Panel foydalanuvchilari", icon: UserCog, min: "admin" },
  { to: "/audit", label: "Audit log", icon: ClipboardList, min: "admin" },
  { to: "/alerts", label: "Ogohlantirishlar", icon: Bell, min: "admin" },
  { to: "/settings", label: "Sozlamalar", icon: SettingsIcon, min: "viewer" },
];

const roleLabels: Record<Role, string> = {
  viewer: "Kuzatuvchi",
  operator: "Operator",
  admin: "Administrator",
};

export default function Layout({ children }: { children: ReactNode }) {
  const { logout, user, can } = useAuth();
  const status = useSquidStatus();
  // Below the lg breakpoint the sidebar is a drawer opened from the top bar.
  const [menuOpen, setMenuOpen] = useState(false);

  return (
    <div className="flex min-h-screen">
      {menuOpen && (
        <div className="fixed inset-0 z-30 bg-black/60 lg:hidden" onClick={() => setMenuOpen(false)} aria-hidden="true" />
      )}
      <aside
        className={`fixed inset-y-0 left-0 z-40 flex w-64 shrink-0 flex-col overflow-y-auto border-r border-white/[0.08] bg-[#0b0c11] p-4 transition-transform duration-200 lg:sticky lg:top-0 lg:h-screen lg:translate-x-0 lg:bg-[#0b0c11]/60 lg:backdrop-blur-xl ${
          menuOpen ? "translate-x-0" : "-translate-x-full"
        }`}
      >
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
          {navItems.filter((item) => can(item.min)).map((item) => {
            const Icon = item.icon;
            return (
              <NavLink
                key={item.to}
                to={item.to}
                onClick={() => setMenuOpen(false)}
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

          {user && (
            <div className="flex items-center gap-2.5 px-3 py-1.5">
              <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-white/[0.06] text-xs font-semibold uppercase text-neutral-300">
                {user.username.slice(0, 1)}
              </div>
              <div className="min-w-0">
                <div className="truncate text-xs font-medium text-neutral-200">{user.username}</div>
                <div className="text-[10px] text-neutral-500">{roleLabels[user.role]}</div>
              </div>
            </div>
          )}

          <button
            onClick={logout}
            className="flex items-center gap-2.5 rounded-lg px-3 py-2 text-left text-[13px] font-medium text-neutral-500 transition-colors hover:bg-white/[0.05] hover:text-neutral-200"
          >
            <LogOut size={16} />
            Chiqish
          </button>
        </div>
      </aside>
      <main className="min-w-0 flex-1 p-4 sm:p-6 lg:p-8">
        <div className="mb-5 flex items-center gap-3 lg:hidden">
          <button
            type="button"
            aria-label="Menyuni ochish"
            onClick={() => setMenuOpen(true)}
            className="rounded-lg border border-white/10 bg-white/[0.04] p-2 text-neutral-300 hover:bg-white/[0.08]"
          >
            <Menu size={18} />
          </button>
          <span className="text-sm font-semibold text-white">Squid Admin</span>
        </div>
        <div className="mx-auto max-w-5xl">{children}</div>
      </main>
    </div>
  );
}
