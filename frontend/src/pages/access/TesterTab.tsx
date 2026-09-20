import { CheckCircle2, CircleDashed, HelpCircle, KeyRound, Search, XCircle } from "lucide-react";
import { useState } from "react";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { Input, Label, Select } from "../../components/ui/Input";
import { api, errorMessage } from "../../lib/api";
import { decisionMeta, sourceLabels, type RuleTrace, type Verdict } from "../../lib/access";
import { useToast } from "../../lib/toast";

const methods = ["GET", "POST", "PUT", "DELETE", "HEAD", "CONNECT", "OPTIONS", "PATCH"];

function resultIcon(r: RuleTrace["result"]) {
  switch (r) {
    case "match":
      return <CheckCircle2 size={15} className="text-emerald-400" />;
    case "no-match":
      return <XCircle size={15} className="text-neutral-600" />;
    case "auth-required":
      return <KeyRound size={15} className="text-sky-400" />;
    case "unknown":
      return <HelpCircle size={15} className="text-amber-400" />;
    default:
      return <CircleDashed size={15} className="text-neutral-700" />;
  }
}

const termTone: Record<string, string> = {
  match: "text-emerald-300",
  "no-match": "text-neutral-500",
  unknown: "text-amber-300",
  skipped: "text-neutral-700",
};

/** "Why was this request allowed or blocked?" — walks the real rules in order. */
export default function TesterTab() {
  const toast = useToast();
  const [srcIp, setSrcIp] = useState("127.0.0.1");
  const [url, setUrl] = useState("");
  const [method, setMethod] = useState("GET");
  const [user, setUser] = useState("");
  const [agent, setAgent] = useState("");
  const [time, setTime] = useState("");
  const [busy, setBusy] = useState(false);
  const [verdict, setVerdict] = useState<Verdict | null>(null);
  const [showAll, setShowAll] = useState(false);

  async function test(e: React.FormEvent) {
    e.preventDefault();
    if (!url.trim()) return toast.error("URL kiriting");
    setBusy(true);
    try {
      const res = await api.post<Verdict>("/squid/access/test", {
        src_ip: srcIp.trim(),
        url: url.trim(),
        method,
        user: user.trim(),
        user_agent: agent.trim(),
        time,
      });
      setVerdict(res.data);
    } catch (err) {
      setVerdict(null);
      toast.error(errorMessage(err, "Tekshirib bo'lmadi"));
    } finally {
      setBusy(false);
    }
  }

  const decided = verdict?.rule_index ? verdict.trace[verdict.rule_index - 1] : null;
  const shown = verdict ? verdict.trace.filter((t) => showAll || (t.result !== "not-reached" && (t.result !== "no-match" || t.terms.some((x) => x.result === "match")))) : [];
  const meta = verdict ? decisionMeta[verdict.decision] : null;

  return (
    <div className="grid gap-6">
      <Card className="p-5">
        <div className="mb-4 flex items-center gap-2 text-sm font-semibold text-white">
          <Search size={15} className="text-indigo-400" />
          So'rovni sinab ko'rish
        </div>
        <form onSubmit={test} className="grid gap-4">
          <div className="grid gap-4 sm:grid-cols-[1fr_140px]">
            <div>
              <Label>Manzil (URL)</Label>
              <Input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://www.youtube.com/watch" className="font-mono" autoFocus />
            </div>
            <div>
              <Label>Usul</Label>
              <Select value={method} onChange={(e) => setMethod(e.target.value)}>
                {methods.map((m) => (
                  <option key={m}>{m}</option>
                ))}
              </Select>
            </div>
          </div>
          <div className="grid gap-4 sm:grid-cols-3">
            <div>
              <Label>Manba IP (kim yubordi)</Label>
              <Input value={srcIp} onChange={(e) => setSrcIp(e.target.value)} className="font-mono" />
              <div className="mt-1.5 flex gap-2 text-[11px]">
                <button type="button" onClick={() => setSrcIp("127.0.0.1")} className="text-neutral-500 hover:text-neutral-200">
                  localhost
                </button>
                <button type="button" onClick={() => setSrcIp("192.168.1.50")} className="text-neutral-500 hover:text-neutral-200">
                  LAN namuna
                </button>
              </div>
            </div>
            <div>
              <Label>Proksi foydalanuvchisi (ixtiyoriy)</Label>
              <Input value={user} onChange={(e) => setUser(e.target.value)} placeholder="bo'sh = login qilmagan" />
            </div>
            <div>
              <Label>Vaqt (bo'sh = hozir)</Label>
              <Input type="datetime-local" value={time} onChange={(e) => setTime(e.target.value)} />
            </div>
          </div>
          <div>
            <Label>Brauzer / User-Agent (ixtiyoriy)</Label>
            <Input value={agent} onChange={(e) => setAgent(e.target.value)} placeholder="Mozilla/5.0 …" />
          </div>
          <Button type="submit" disabled={busy} icon={<Search size={15} />} className="w-fit">
            {busy ? "Tekshirilmoqda…" : "Tekshirish"}
          </Button>
        </form>
      </Card>

      {verdict && meta && (
        <Card className="overflow-hidden">
          <div className={`border-b px-5 py-4 ${meta.tone}`}>
            <div className="text-lg font-bold tracking-wide">{meta.label}</div>
            <p className="mt-0.5 text-sm opacity-90">{meta.text}</p>
            {decided && (
              <p className="mt-2 text-xs opacity-90">
                Hal qilgan qoida: <b>#{decided.rule.index}</b> ·{" "}
                {(sourceLabels[decided.rule.source] ?? sourceLabels.other).label} ·{" "}
                <code className="font-mono">{decided.rule.raw}</code>
                {decided.rule.comment && <> ({decided.rule.comment})</>}
              </p>
            )}
          </div>

          <div className="flex items-center justify-between px-5 py-3 text-xs text-neutral-500">
            <span>Tekshiruv izi (Squid qoidalarni shu tartibda ko'radi)</span>
            <label className="flex items-center gap-1.5">
              <input type="checkbox" checked={showAll} onChange={(e) => setShowAll(e.target.checked)} />
              Hammasini ko'rsatish
            </label>
          </div>

          <ul className="divide-y divide-white/[0.05] border-t border-white/[0.05]">
            {shown.map((t) => (
              <li key={t.rule.index} className={`flex gap-3 px-5 py-3 ${t.result === "match" ? "bg-emerald-500/[0.05]" : ""}`}>
                <span className="mt-0.5 shrink-0">{resultIcon(t.result)}</span>
                <div className="min-w-0 flex-1 text-sm">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-mono text-xs text-neutral-500">#{t.rule.index}</span>
                    <span className={`rounded-full px-2 py-0.5 text-[10px] font-medium ${(sourceLabels[t.rule.source] ?? sourceLabels.other).tone}`}>
                      {(sourceLabels[t.rule.source] ?? sourceLabels.other).label}
                    </span>
                    <code className="truncate font-mono text-xs text-neutral-300">{t.rule.raw}</code>
                  </div>
                  {t.terms.length > 0 && (
                    <ul className="mt-1.5 grid gap-0.5 text-xs">
                      {t.terms.map((x, i) => (
                        <li key={i} className={termTone[x.result]}>
                          <code className="font-mono">
                            {x.negate ? "!" : ""}
                            {x.acl}
                          </code>{" "}
                          — {x.detail}
                        </li>
                      ))}
                    </ul>
                  )}
                </div>
              </li>
            ))}
          </ul>

          {verdict.notes.length > 0 && (
            <ul className="border-t border-white/[0.06] px-5 py-3 text-xs leading-relaxed text-neutral-500">
              {verdict.notes.map((n, i) => (
                <li key={i}>• {n}</li>
              ))}
            </ul>
          )}
        </Card>
      )}
    </div>
  );
}
