import { ScrollText } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { PageHeader } from "../components/ui/PageHeader";
import { wsURL } from "../lib/api";

export default function Logs() {
  const [lines, setLines] = useState<string[]>([]);
  const [connected, setConnected] = useState(false);
  const boxRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const socket = new WebSocket(wsURL("/squid/logs/stream"));

    socket.onopen = () => setConnected(true);
    socket.onclose = () => setConnected(false);
    socket.onmessage = (event) => {
      setLines((prev) => [...prev.slice(-499), event.data]);
    };

    return () => socket.close();
  }, []);

  useEffect(() => {
    boxRef.current?.scrollTo({ top: boxRef.current.scrollHeight });
  }, [lines]);

  return (
    <div>
      <PageHeader
        icon={<ScrollText size={19} />}
        title="Jonli loglar"
        description="Squid orqali o'tayotgan trafikni real vaqtda kuzating"
        action={
          <span className="flex items-center gap-1.5 rounded-full border border-white/10 bg-white/[0.03] px-3 py-1.5 text-xs font-medium text-neutral-400">
            <span className="relative flex h-2 w-2">
              {connected && (
                <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75" />
              )}
              <span
                className={`relative inline-flex h-2 w-2 rounded-full ${connected ? "bg-emerald-500" : "bg-neutral-600"}`}
              />
            </span>
            {connected ? "Ulangan" : "Ulanmagan"}
          </span>
        }
      />

      <div
        ref={boxRef}
        className="h-[65vh] overflow-auto rounded-xl border border-white/[0.08] bg-black/40 p-4 font-mono text-[12px] leading-relaxed text-neutral-300 shadow-[0_1px_0_0_rgba(255,255,255,0.04)_inset]"
      >
        {lines.length === 0 && (
          <p className="text-neutral-600">Trafik kutilmoqda...</p>
        )}
        {lines.map((line, i) => (
          <div key={i} className="whitespace-pre-wrap border-b border-white/[0.03] py-0.5">
            {line}
          </div>
        ))}
      </div>
    </div>
  );
}
