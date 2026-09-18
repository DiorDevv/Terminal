import type { ReactNode } from "react";
import { IconBadge } from "./IconBadge";

type Tone = "indigo" | "green" | "red" | "amber" | "neutral";

export function StatCard({
  icon,
  tone = "indigo",
  label,
  value,
  hint,
  children,
}: {
  icon: ReactNode;
  tone?: Tone;
  label: string;
  value: ReactNode;
  hint?: string;
  children?: ReactNode;
}) {
  return (
    <div className="rounded-2xl border border-white/[0.08] bg-[#0f1117]/80 p-5 backdrop-blur-sm shadow-[0_1px_0_0_rgba(255,255,255,0.04)_inset,0_8px_24px_-12px_rgba(0,0,0,0.6)]">
      <div className="mb-4 flex items-center justify-between">
        <IconBadge tone={tone}>{icon}</IconBadge>
      </div>
      <div className="text-xs font-medium uppercase tracking-wider text-neutral-500">
        {label}
      </div>
      <div className="mt-1 text-2xl font-semibold tracking-tight text-white">
        {value}
      </div>
      {hint && <div className="mt-1 text-xs text-neutral-500">{hint}</div>}
      {children}
    </div>
  );
}
