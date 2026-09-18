import type { ReactNode } from "react";
import { IconBadge } from "./IconBadge";

export function PageHeader({
  icon,
  title,
  description,
  action,
}: {
  icon: ReactNode;
  title: string;
  description?: string;
  action?: ReactNode;
}) {
  return (
    <div className="mb-7 flex items-start justify-between gap-4">
      <div className="flex items-start gap-3.5">
        <IconBadge size={40}>{icon}</IconBadge>
        <div>
          <h2 className="text-xl font-semibold tracking-tight text-white">
            {title}
          </h2>
          {description && (
            <p className="mt-1 max-w-xl text-sm leading-relaxed text-neutral-400">
              {description}
            </p>
          )}
        </div>
      </div>
      {action}
    </div>
  );
}
