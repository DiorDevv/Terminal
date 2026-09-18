import { useEffect, useState } from "react";
import { api } from "./api";

export interface SquidStatus {
  running: boolean;
  detail: string;
}

export function useSquidStatus(intervalMs = 8000) {
  const [status, setStatus] = useState<SquidStatus | null>(null);

  useEffect(() => {
    let cancelled = false;

    async function poll() {
      try {
        const res = await api.get<SquidStatus>("/squid/status");
        if (!cancelled) setStatus(res.data);
      } catch {
        // handled by the axios 401 interceptor / individual pages
      }
    }

    poll();
    const id = setInterval(poll, intervalMs);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [intervalMs]);

  return status;
}
