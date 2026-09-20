import { KeyRound, LogOut } from "lucide-react";
import { useNavigate } from "react-router-dom";
import { PasswordForm } from "../components/PasswordForm";
import { useAuth } from "../lib/auth";

/** Shown instead of the app while the account still has a temporary password. */
export default function ChangePassword() {
  const { user, refresh, logout } = useAuth();
  const navigate = useNavigate();

  async function done() {
    await refresh();
    navigate("/", { replace: true });
  }

  return (
    <div className="flex min-h-screen items-center justify-center px-4">
      <div className="w-full max-w-sm">
        <div className="mb-8 flex flex-col items-center text-center">
          <div className="mb-4 flex h-14 w-14 items-center justify-center rounded-2xl bg-gradient-to-br from-amber-500 to-orange-600 shadow-[0_8px_24px_-6px_rgba(245,158,11,0.5)]">
            <KeyRound size={26} className="text-white" strokeWidth={2.25} />
          </div>
          <h1 className="text-lg font-semibold text-white">Yangi parol o'rnating</h1>
          <p className="mt-1 text-sm leading-relaxed text-neutral-500">
            <span className="text-neutral-300">{user?.username}</span> hisobi vaqtinchalik parol
            bilan ochilgan. Davom etish uchun o'zingizning parolingizni tanlang.
          </p>
        </div>

        <div className="rounded-2xl border border-white/[0.08] bg-[#0f1117]/80 p-7 backdrop-blur-sm shadow-[0_1px_0_0_rgba(255,255,255,0.04)_inset,0_20px_50px_-20px_rgba(0,0,0,0.8)]">
          <PasswordForm oldLabel="Vaqtinchalik parol" onDone={done} />
        </div>

        <button
          onClick={logout}
          className="mx-auto mt-5 flex items-center gap-2 text-sm text-neutral-500 transition-colors hover:text-neutral-300"
        >
          <LogOut size={14} />
          Chiqish
        </button>
      </div>
    </div>
  );
}
