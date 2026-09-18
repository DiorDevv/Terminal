import { FileCode2, Save } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "../components/ui/Button";
import { PageHeader } from "../components/ui/PageHeader";
import { Skeleton } from "../components/ui/Skeleton";
import { api } from "../lib/api";
import { useToast } from "../lib/toast";

export default function ConfigEditor() {
  const [content, setContent] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const toast = useToast();

  useEffect(() => {
    api
      .get<{ content: string }>("/squid/config")
      .then((res) => setContent(res.data.content))
      .catch((err) => toast.error(err.response?.data?.error ?? "Yuklab bo'lmadi"))
      .finally(() => setLoading(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function save() {
    setSaving(true);
    try {
      await api.put("/squid/config", { content });
      toast.success("squid.conf saqlandi va tekshirildi");
    } catch (err: any) {
      toast.error(err.response?.data?.error ?? "Saqlashda xatolik");
    } finally {
      setSaving(false);
    }
  }

  return (
    <div>
      <PageHeader
        icon={<FileCode2 size={19} />}
        title="squid.conf"
        description="Xom konfiguratsiya fayli. Boshqa sahifalardagi (guruhlar, cheklovlar, bloklama) avtomatik bloklarni qo'lda o'zgartirmang."
      />

      {loading ? (
        <Skeleton className="h-[60vh] w-full" />
      ) : (
        <>
          <textarea
            value={content}
            onChange={(e) => setContent(e.target.value)}
            spellCheck={false}
            className="mono mb-4 h-[60vh] w-full rounded-xl border border-white/10 bg-black/30 p-4 font-mono text-[13px] leading-relaxed text-neutral-300 outline-none transition-colors focus:border-indigo-500/50 focus:ring-2 focus:ring-indigo-500/15"
          />

          <Button onClick={save} disabled={saving} icon={<Save size={15} />}>
            {saving ? "Saqlanmoqda..." : "Saqlash va tekshirish"}
          </Button>
        </>
      )}
    </div>
  );
}
