import { Eye } from "lucide-react";
import type { ReactNode } from "react";
import { useAuth } from "../lib/auth";

/**
 * Makes a page read-only for users below the operator role: every input and
 * button inside is disabled (a disabled <fieldset> does that for all of its
 * descendants) and a notice explains why. The API refuses their writes
 * regardless; this only keeps the UI from offering actions that would fail.
 */
export function WriteGuard({ children }: { children: ReactNode }) {
  const { can, user } = useAuth();
  if (can("operator")) return <>{children}</>;

  return (
    <>
      <div className="mb-5 flex items-center gap-2.5 rounded-lg border border-amber-500/20 bg-amber-500/10 px-4 py-2.5 text-sm text-amber-300">
        <Eye size={15} className="shrink-0" />
        Faqat o'qish rejimi: "{user?.role}" roli o'zgartirish kiritishga ruxsat bermaydi.
      </div>
      <fieldset disabled className="contents min-w-0 border-0 p-0 m-0">
        {children}
      </fieldset>
    </>
  );
}
