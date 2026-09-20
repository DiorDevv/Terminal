import { X } from "lucide-react";
import { type ReactNode, useEffect } from "react";

/** A centred dialog. Closes on Escape and on a click outside it. */
export function Modal({
  title,
  onClose,
  children,
  wide = false,
}: {
  title: string;
  onClose: () => void;
  children: ReactNode;
  wide?: boolean;
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm"
      onMouseDown={(e) => e.target === e.currentTarget && onClose()}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className={`max-h-[90vh] w-full overflow-auto rounded-2xl border border-white/[0.08] bg-[#14171f] p-6 shadow-[0_20px_60px_-15px_rgba(0,0,0,0.9)] ${
          wide ? "max-w-2xl" : "max-w-md"
        }`}
      >
        <div className="mb-4 flex items-start justify-between gap-3">
          <h3 className="font-semibold text-white">{title}</h3>
          <button type="button" aria-label="Yopish" onClick={onClose} className="rounded-md p-1 text-neutral-500 hover:bg-white/[0.06] hover:text-neutral-200">
            <X size={16} />
          </button>
        </div>
        {children}
      </div>
    </div>
  );
}
