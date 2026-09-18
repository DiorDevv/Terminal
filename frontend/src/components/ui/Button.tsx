import type { ButtonHTMLAttributes, ReactNode } from "react";

type Variant = "primary" | "secondary" | "danger" | "ghost";

const variants: Record<Variant, string> = {
  primary:
    "bg-gradient-to-b from-indigo-500 to-indigo-600 text-white shadow-[0_1px_0_0_rgba(255,255,255,0.15)_inset,0_4px_16px_-4px_rgba(99,102,241,0.5)] hover:from-indigo-400 hover:to-indigo-500 hover:shadow-[0_1px_0_0_rgba(255,255,255,0.2)_inset,0_6px_20px_-4px_rgba(99,102,241,0.65)]",
  secondary:
    "bg-white/[0.04] text-neutral-200 border border-white/10 hover:bg-white/[0.08] hover:border-white/20",
  danger:
    "bg-gradient-to-b from-red-500 to-red-600 text-white shadow-[0_1px_0_0_rgba(255,255,255,0.15)_inset,0_4px_16px_-4px_rgba(239,68,68,0.5)] hover:from-red-400 hover:to-red-500",
  ghost: "text-neutral-400 hover:bg-white/[0.06] hover:text-neutral-100",
};

interface Props extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
  icon?: ReactNode;
  children: ReactNode;
}

export function Button({
  variant = "primary",
  icon,
  children,
  className = "",
  ...rest
}: Props) {
  return (
    <button
      className={`inline-flex items-center gap-1.5 rounded-lg px-4 py-2 text-sm font-medium transition-all duration-150 disabled:cursor-not-allowed disabled:opacity-50 ${variants[variant]} ${className}`}
      {...rest}
    >
      {icon}
      {children}
    </button>
  );
}
