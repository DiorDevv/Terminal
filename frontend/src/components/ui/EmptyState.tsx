import type { ReactNode } from "react";

export function EmptyState({
  icon,
  title,
  description,
}: {
  icon: ReactNode;
  title: string;
  description?: string;
}) {
  return (
    <div className="flex flex-col items-center gap-2 px-6 py-14 text-center">
      <div className="mb-1 flex h-12 w-12 items-center justify-center rounded-full bg-white/[0.04] text-neutral-600">
        {icon}
      </div>
      <p className="text-sm font-medium text-neutral-300">{title}</p>
      {description && (
        <p className="max-w-xs text-xs text-neutral-500">{description}</p>
      )}
    </div>
  );
}
