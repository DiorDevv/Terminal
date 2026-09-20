import { Layers } from "lucide-react";
import { useEffect, useState } from "react";
import { Card } from "../../components/ui/Card";
import { ListSkeleton } from "../../components/ui/Skeleton";
import { api, errorMessage } from "../../lib/api";
import { type PolicyAcl, type PolicyRule, sourceLabels } from "../../lib/access";
import { useToast } from "../../lib/toast";

/** Read-only view of every http_access rule squid evaluates, in order. */
export default function PolicyTab({ version }: { version: number }) {
  const toast = useToast();
  const [rules, setRules] = useState<PolicyRule[] | null>(null);
  const [acls, setAcls] = useState<Record<string, PolicyAcl>>({});

  useEffect(() => {
    api
      .get<{ rules: PolicyRule[]; acls: Record<string, PolicyAcl> }>("/squid/access/policy")
      .then((r) => {
        setRules(r.data.rules);
        setAcls(r.data.acls);
      })
      .catch((err) => toast.error(errorMessage(err, "Siyosatni yuklab bo'lmadi")));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [version]);

  const describe = (name: string) => {
    const a = acls[name];
    if (!a) return name;
    const args = a.args ?? [];
    const sample = args.slice(0, 8).join(", ");
    return `${a.type}: ${sample}${args.length > 8 ? ` … (+${args.length - 8})` : ""}`;
  };

  return (
    <Card className="overflow-hidden">
      <div className="flex items-start gap-2.5 border-b border-white/[0.06] px-5 py-4">
        <Layers size={16} className="mt-0.5 text-indigo-400" />
        <div>
          <h3 className="text-sm font-semibold text-white">Samarali siyosat: Squid haqiqatda shu tartibda tekshiradi</h3>
          <p className="mt-0.5 text-xs leading-relaxed text-neutral-500">
            Tartib: Squid xavfsizlik cheklovlari → bloklangan domenlar → vaqt cheklovlari → <b>sizning qoidalaringiz</b> →
            Squid asosiy ruxsatlari (localhost, LAN) → proksi login → hammasini rad etish. Birinchi mos kelgan qoida hal qiladi.
          </p>
        </div>
      </div>

      {rules === null ? (
        <ListSkeleton rows={6} />
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[720px] text-left text-sm">
            <thead>
              <tr className="border-b border-white/[0.06] text-[11px] uppercase tracking-wider text-neutral-500">
                <th className="px-5 py-3 font-medium">#</th>
                <th className="px-3 py-3 font-medium">Manba</th>
                <th className="px-3 py-3 font-medium">Amal</th>
                <th className="px-3 py-3 font-medium">Shartlar</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-white/[0.05]">
              {rules.map((r) => {
                const src = sourceLabels[r.source] ?? sourceLabels.other;
                const mine = r.source === "rule";
                return (
                  <tr key={r.index} className={`align-top ${mine ? "bg-indigo-500/[0.04]" : ""}`}>
                    <td className="px-5 py-2.5 font-mono text-xs text-neutral-500">{r.index}</td>
                    <td className="px-3 py-2.5">
                      <span className={`rounded-full px-2 py-0.5 text-[10px] font-medium ${src.tone}`}>{src.label}</span>
                    </td>
                    <td className="px-3 py-2.5">
                      <span
                        className={`rounded-md px-2 py-0.5 text-[11px] font-semibold uppercase ${
                          r.action === "allow" ? "bg-emerald-500/15 text-emerald-300" : "bg-red-500/15 text-red-300"
                        }`}
                      >
                        {r.action === "allow" ? "Ruxsat" : "Rad"}
                      </span>
                    </td>
                    <td className="px-3 py-2.5 text-neutral-300">
                      <div className="flex flex-wrap gap-1.5">
                        {r.terms.map((t, i) => (
                          <code
                            key={i}
                            title={describe(t.acl)}
                            className="rounded bg-white/[0.06] px-1.5 py-0.5 text-[12px] text-neutral-200"
                          >
                            {t.negate ? "!" : ""}
                            {t.acl}
                          </code>
                        ))}
                      </div>
                      {r.comment && <div className="mt-1 text-xs text-neutral-500">{r.comment}</div>}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}
