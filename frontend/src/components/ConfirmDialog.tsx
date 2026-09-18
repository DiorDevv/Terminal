import { AlertTriangle } from "lucide-react";
import { createContext, type ReactNode, useCallback, useContext, useState } from "react";
import { Button } from "./ui/Button";

interface ConfirmOptions {
  title: string;
  description?: string;
  confirmLabel?: string;
}

interface ConfirmContextValue {
  confirm: (options: ConfirmOptions) => Promise<boolean>;
}

const ConfirmContext = createContext<ConfirmContextValue | null>(null);

export function ConfirmProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<{
    options: ConfirmOptions;
    resolve: (value: boolean) => void;
  } | null>(null);

  const confirm = useCallback((options: ConfirmOptions) => {
    return new Promise<boolean>((resolve) => {
      setState({ options, resolve });
    });
  }, []);

  function close(result: boolean) {
    state?.resolve(result);
    setState(null);
  }

  return (
    <ConfirmContext.Provider value={{ confirm }}>
      {children}
      {state && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm">
          <div className="w-full max-w-sm rounded-2xl border border-white/[0.08] bg-[#14171f] p-6 shadow-[0_20px_60px_-15px_rgba(0,0,0,0.9)]">
            <div className="mb-3 flex items-center gap-2.5">
              <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-amber-500/15 text-amber-400 ring-1 ring-amber-500/20">
                <AlertTriangle size={18} />
              </div>
              <h3 className="font-semibold text-white">{state.options.title}</h3>
            </div>
            {state.options.description && (
              <p className="mb-5 text-sm leading-relaxed text-neutral-400">
                {state.options.description}
              </p>
            )}
            <div className="flex justify-end gap-2">
              <Button variant="ghost" onClick={() => close(false)}>
                Bekor qilish
              </Button>
              <Button variant="danger" onClick={() => close(true)}>
                {state.options.confirmLabel ?? "O'chirish"}
              </Button>
            </div>
          </div>
        </div>
      )}
    </ConfirmContext.Provider>
  );
}

export function useConfirm() {
  const ctx = useContext(ConfirmContext);
  if (!ctx) throw new Error("useConfirm must be used within ConfirmProvider");
  return ctx.confirm;
}
