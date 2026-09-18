import type { ReactNode } from "react";

type Tone = "indigo" | "green" | "red" | "amber" | "neutral";

const tones: Record<Tone, string> = {
  indigo: "bg-indigo-500/15 text-indigo-400 ring-1 ring-indigo-500/20",
  green: "bg-emerald-500/15 text-emerald-400 ring-1 ring-emerald-500/20",
  red: "bg-red-500/15 text-red-400 ring-1 ring-red-500/20",
  amber: "bg-amber-500/15 text-amber-400 ring-1 ring-amber-500/20",
  neutral: "bg-white/[0.06] text-neutral-300 ring-1 ring-white/10",
};

export function IconBadge({
  children,
  tone = "indigo",
  size = 36,
}: {
  children: ReactNode;
  tone?: Tone;
  size?: number;
}) {
  return (
    <div
      className={`flex shrink-0 items-center justify-center rounded-xl ${tones[tone]}`}
      style={{ width: size, height: size }}
    >
      {children}
    </div>
  );
}
