import { CheckCircle2, FileUp, TriangleAlert } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "../../components/ui/Button";
import { Modal } from "../../components/ui/Modal";
import { api, errorMessage } from "../../lib/api";
import { notifyResult, type ReloadResult } from "../../lib/reload";
import { useToast } from "../../lib/toast";
import type { ImportReport } from "../../lib/userpolicy";

const actionLabel = { create: "yaratiladi", update: "yangilanadi", error: "xato" } as const;

/**
 * Checks a CSV file first (a dry run that changes nothing), shows what would
 * happen row by row, and only applies it when the admin confirms. One bad row
 * and the server applies nothing at all.
 */
export function ImportDialog({ file, onClose, onDone }: { file: File; onClose: () => void; onDone: () => void }) {
  const toast = useToast();
  const [text, setText] = useState<string | null>(null);
  const [report, setReport] = useState<ImportReport | null>(null);
  const [problem, setProblem] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function post(dry: boolean, body: string) {
    return api.post<{ report: ImportReport } & ReloadResult>(`/squid/proxy-users-import${dry ? "?dry_run=1" : ""}`, body, {
      headers: { "Content-Type": "text/csv" },
    });
  }

  // Read the file and run the dry run when the dialog opens.
  useEffect(() => {
    let cancelled = false;
    file
      .text()
      .then(async (t) => {
        if (cancelled) return;
        setText(t);
        try {
          const res = await post(true, t);
          if (!cancelled) setReport(res.data.report);
        } catch (err) {
          if (cancelled) return;
          const data = (err as { response?: { data?: { report?: ImportReport } } }).response?.data;
          if (data?.report) setReport(data.report);
          else setProblem(errorMessage(err, "Faylni tekshirib bo'lmadi"));
        }
      })
      .catch(() => !cancelled && setProblem("Faylni o'qib bo'lmadi"));
    return () => {
      cancelled = true;
    };
  }, [file]);

  async function apply() {
    if (text === null) return;
    setBusy(true);
    try {
      const res = await post(false, text);
      notifyResult(toast, res.data, `Import qilindi: ${res.data.report.created} ta yangi, ${res.data.report.updated} ta yangilandi`);
      onDone();
      onClose();
    } catch (err) {
      toast.error(errorMessage(err, "Import qilib bo'lmadi"));
    } finally {
      setBusy(false);
    }
  }

  const ok = report && report.errors === 0;

  return (
    <Modal title={`CSV import: ${file.name}`} onClose={onClose} wide>
      {!report && !problem && <p className="text-sm text-neutral-400">Fayl tekshirilmoqda…</p>}
      {problem && <p className="rounded-lg border border-red-500/20 bg-red-500/10 px-3 py-2 text-sm text-red-300">{problem}</p>}

      {report && (
        <>
          <div
            className={`mb-4 flex items-start gap-2 rounded-lg border px-3 py-2 text-sm ${
              ok ? "border-emerald-500/20 bg-emerald-500/10 text-emerald-200" : "border-red-500/20 bg-red-500/10 text-red-200"
            }`}
          >
            {ok ? <CheckCircle2 size={16} className="mt-0.5 shrink-0" /> : <TriangleAlert size={16} className="mt-0.5 shrink-0" />}
            <span>
              {ok
                ? `Fayl to'g'ri: ${report.created} ta yangi hisob, ${report.updated} ta yangilanadi. Hali hech narsa o'zgartirilmadi.`
                : `${report.errors} ta qatorda xato bor. Xatolar tuzatilmaguncha hech narsa import qilinmaydi.`}
            </span>
          </div>

          <div className="max-h-72 overflow-auto rounded-lg border border-white/[0.06]">
            <table className="w-full text-left text-xs">
              <thead className="sticky top-0 bg-[#14171f] text-neutral-500">
                <tr>
                  <th className="px-3 py-2 font-medium">Qator</th>
                  <th className="px-3 py-2 font-medium">Foydalanuvchi</th>
                  <th className="px-3 py-2 font-medium">Natija</th>
                </tr>
              </thead>
              <tbody className="text-neutral-300">
                {report.rows.map((r) => (
                  <tr key={`${r.line}-${r.username}`} className="border-t border-white/[0.05]">
                    <td className="px-3 py-1.5 tabular-nums text-neutral-500">{r.line}</td>
                    <td className="px-3 py-1.5 font-mono">{r.username || "—"}</td>
                    <td className={`px-3 py-1.5 ${r.action === "error" ? "text-red-300" : ""}`}>{r.action === "error" ? r.error : actionLabel[r.action]}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}

      <p className="mt-3 text-xs text-neutral-500">
        Ustunlar: <code>username</code> (majburiy), <code>password</code>, <code>disabled</code>, <code>expires</code> (YYYY-MM-DD), <code>daily_quota_mb</code>, <code>groups</code> (; bilan), <code>note</code>. Bo'sh katak mavjud
        sozlamani o'zgartirmaydi; mavjud hisobga parol yozilsa, parol almashtiriladi.
      </p>

      <div className="mt-5 flex justify-end gap-2">
        <Button variant="ghost" onClick={onClose}>
          Yopish
        </Button>
        <Button onClick={apply} disabled={!ok || busy} icon={<FileUp size={15} />}>
          Import qilish
        </Button>
      </div>
    </Modal>
  );
}
