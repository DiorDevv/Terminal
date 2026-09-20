import { TriangleAlert } from "lucide-react";
import { Component, type ErrorInfo, type ReactNode } from "react";
import { Button } from "./ui/Button";

interface State {
  error: Error | null;
}

/**
 * Without this, one exception while rendering any page unmounts the whole app
 * and leaves a blank screen with no way back. Here the user sees what happened
 * and can retry or reload.
 */
export class ErrorBoundary extends Component<{ children: ReactNode }, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("render failed:", error, info.componentStack);
  }

  render() {
    if (!this.state.error) return this.props.children;
    return (
      <div className="flex min-h-screen items-center justify-center p-6">
        <div className="w-full max-w-md rounded-2xl border border-white/[0.08] bg-[#0f1117]/90 p-8 text-center">
          <div className="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-full bg-red-500/15 text-red-400">
            <TriangleAlert size={22} />
          </div>
          <h1 className="text-lg font-semibold text-white">Sahifani ko'rsatib bo'lmadi</h1>
          <p className="mt-2 text-sm text-neutral-400">
            Kutilmagan xato yuz berdi. Ma'lumotlaringiz saqlanib qolgan, sahifani qayta yuklab ko'ring.
          </p>
          <p className="mt-3 break-words rounded-lg bg-black/30 px-3 py-2 font-mono text-xs text-neutral-500">
            {this.state.error.message}
          </p>
          <div className="mt-5 flex justify-center gap-2">
            <Button variant="secondary" onClick={() => this.setState({ error: null })}>
              Qayta urinish
            </Button>
            <Button onClick={() => window.location.assign("/")}>Bosh sahifa</Button>
          </div>
        </div>
      </div>
    );
  }
}
