import { KeyRound, ShieldOff, Sparkles, UserCheck } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { Input, Label } from "../../components/ui/Input";
import { api, errorMessage } from "../../lib/api";
import { type Acl, keywordToPattern } from "../../lib/access";
import { notifyResult, type ReloadResult } from "../../lib/reload";
import { useToast } from "../../lib/toast";

const lines = (s: string) =>
  s
    .split(/[\n,]/)
    .map((x) => x.trim())
    .filter(Boolean);

const BASE = /^[a-z0-9_-]{3,30}$/;

type TemplateId = "whitelist" | "keywords" | "users";

/**
 * Templates are just shortcuts: each one creates ordinary ACLs and one rule
 * through the same API as the manual form, so the result can be edited later.
 * If any step fails, the ACLs created so far are removed again.
 */
export default function Templates({ acls, reload }: { acls: Acl[]; reload: () => Promise<void> }) {
  const toast = useToast();
  const [open, setOpen] = useState<TemplateId | null>(null);
  const [busy, setBusy] = useState(false);

  const [name, setName] = useState("");
  const [domains, setDomains] = useState("");
  const [exempt, setExempt] = useState("");
  const [words, setWords] = useState("");
  const [users, setUsers] = useState<string[]>([]);
  const [chosen, setChosen] = useState<string[]>([]);

  useEffect(() => {
    api
      .get<{ users: string[] }>("/squid/users")
      .then((r) => setUsers(r.data.users ?? []))
      .catch(() => {});
  }, []);

  function openTemplate(id: TemplateId) {
    setOpen(open === id ? null : id);
    setName({ whitelist: "whitelist", keywords: "keywords", users: "restricted" }[id]);
  }

  async function build(steps: (add: (body: object) => Promise<number>) => Promise<{ data: ReloadResult }>, ok: string) {
    setBusy(true);
    const created: number[] = [];
    const add = async (body: object) => {
      const res = await api.post<{ acl: { id: number } }>("/squid/access/acls", body);
      created.push(res.data.acl.id);
      return res.data.acl.id;
    };
    try {
      const res = await steps(add);
      notifyResult(toast, res.data, ok);
      setOpen(null);
      setDomains("");
      setExempt("");
      setWords("");
      setChosen([]);
      await reload();
    } catch (err) {
      for (const id of created.reverse()) await api.delete(`/squid/access/acls/${id}`).catch(() => {});
      toast.error(errorMessage(err, "Shablonni qo'llab bo'lmadi"));
      await reload();
    } finally {
      setBusy(false);
    }
  }

  function checkName() {
    if (!BASE.test(name)) {
      toast.error("Nom: kichik harflar, raqamlar, '_' va '-', 3-30 belgi");
      return false;
    }
    if (acls.some((a) => a.name === name || a.name.startsWith(name + "_"))) {
      toast.error(`"${name}" nomi band, boshqa nom tanlang`);
      return false;
    }
    return true;
  }

  const whitelist = (e: React.FormEvent) => {
    e.preventDefault();
    const doms = lines(domains);
    if (!checkName()) return;
    if (doms.length === 0) return toast.error("Kamida bitta domen kiriting");
    const ex = lines(exempt);
    return build(async (add) => {
      const sites = await add({ name, type: "dstdomain", values: doms, description: "Oq ro'yxat: ruxsat etilgan saytlar" });
      const terms = [{ acl_id: sites, negate: true }];
      if (ex.length > 0) {
        const ips = await add({ name: `${name}_exempt`, type: "src", values: ex, description: "Oq ro'yxatdan istisno qurilmalar" });
        terms.push({ acl_id: ips, negate: true });
      }
      return api.post("/squid/access/rules", { action: "deny", terms, comment: "Oq ro'yxat rejimi" });
    }, "Oq ro'yxat rejimi yoqildi: ro'yxatdagi saytlardan boshqasi bloklanadi");
  };

  const keywords = (e: React.FormEvent) => {
    e.preventDefault();
    const ws = lines(words);
    if (!checkName()) return;
    if (ws.length === 0) return toast.error("Kamida bitta so'z kiriting");
    if (ws.some((w) => !/^[A-Za-z0-9._-]+$/.test(w))) {
      return toast.error("So'zlar faqat harf, raqam, '.', '_' va '-' dan iborat bo'lishi kerak (bo'shliqsiz)");
    }
    return build(async (add) => {
      const kw = await add({
        name, type: "url_regex", case_insensitive: true,
        values: ws.map(keywordToPattern), description: "Kalit so'zlar",
      });
      return api.post("/squid/access/rules", { action: "deny", terms: [{ acl_id: kw, negate: false }], comment: "Kalit so'z bo'yicha bloklash" });
    }, "Kalit so'z bo'yicha bloklash yoqildi");
  };

  const restricted = (e: React.FormEvent) => {
    e.preventDefault();
    const doms = lines(domains);
    if (!checkName()) return;
    if (doms.length === 0) return toast.error("Kamida bitta domen kiriting");
    if (chosen.length === 0) return toast.error("Kamida bitta foydalanuvchini tanlang");
    return build(async (add) => {
      const sites = await add({ name: `${name}_sites`, type: "dstdomain", values: doms, description: "Cheklangan saytlar" });
      const who = await add({ name: `${name}_users`, type: "proxy_auth", values: chosen, description: "Ruxsat berilgan foydalanuvchilar" });
      return api.post("/squid/access/rules", {
        action: "deny",
        terms: [{ acl_id: sites, negate: false }, { acl_id: who, negate: true }],
        comment: "Saytlarga faqat tanlangan foydalanuvchilar",
      });
    }, "Cheklov yoqildi: saytlarga faqat tanlangan foydalanuvchilar kira oladi");
  };

  const cards: { id: TemplateId; icon: React.ReactNode; title: string; text: string }[] = [
    { id: "whitelist", icon: <ShieldOff size={16} />, title: "Oq ro'yxat rejimi", text: "Faqat ro'yxatdagi saytlarga ruxsat, qolgani bloklanadi" },
    { id: "keywords", icon: <KeyRound size={16} />, title: "Kalit so'z bilan bloklash", text: "URL'da shu so'zlar bo'lsa, rad etish" },
    { id: "users", icon: <UserCheck size={16} />, title: "Saytni foydalanuvchilarga bog'lash", text: "Tanlangan saytlarga faqat ayrim foydalanuvchilar kiradi" },
  ];

  return (
    <Card className="p-5">
      <div className="mb-4 flex items-center gap-2 text-sm font-semibold text-white">
        <Sparkles size={15} className="text-indigo-400" />
        Tayyor shablonlar
      </div>
      <div className="grid gap-3 md:grid-cols-3">
        {cards.map((c) => (
          <button
            key={c.id}
            type="button"
            onClick={() => openTemplate(c.id)}
            className={`rounded-xl border p-4 text-left transition-colors ${
              open === c.id ? "border-indigo-500/40 bg-indigo-500/10" : "border-white/[0.08] bg-white/[0.02] hover:bg-white/[0.05]"
            }`}
          >
            <div className="mb-1.5 flex items-center gap-2 text-sm font-medium text-neutral-100">
              <span className="text-indigo-400">{c.icon}</span>
              {c.title}
            </div>
            <p className="text-xs leading-relaxed text-neutral-500">{c.text}</p>
          </button>
        ))}
      </div>

      {open && (
        <form
          onSubmit={open === "whitelist" ? whitelist : open === "keywords" ? keywords : restricted}
          className="mt-5 grid gap-4 border-t border-white/[0.06] pt-5"
        >
          <div className="max-w-xs">
            <Label>Nom (obyektlar shu nom bilan yaratiladi)</Label>
            <Input value={name} onChange={(e) => setName(e.target.value)} className="font-mono" />
          </div>

          {open === "keywords" ? (
            <div>
              <Label>Kalit so'zlar (har qatorga bittadan)</Label>
              <textarea
                value={words}
                onChange={(e) => setWords(e.target.value)}
                rows={4}
                placeholder={"casino\nbet365"}
                className="w-full rounded-lg border border-white/10 bg-white/[0.03] px-3 py-2 font-mono text-sm text-neutral-100 outline-none focus:border-indigo-500/60"
              />
              <p className="mt-1.5 text-xs text-neutral-500">Katta-kichik harfga e'tibor berilmaydi; so'z URL'ning istalgan joyida bo'lsa ishlaydi.</p>
            </div>
          ) : (
            <div>
              <Label>{open === "whitelist" ? "Ruxsat etilgan domenlar" : "Cheklanadigan domenlar"} (har qatorga bittadan)</Label>
              <textarea
                value={domains}
                onChange={(e) => setDomains(e.target.value)}
                rows={4}
                placeholder={"example.com\nwikipedia.org"}
                className="w-full rounded-lg border border-white/10 bg-white/[0.03] px-3 py-2 font-mono text-sm text-neutral-100 outline-none focus:border-indigo-500/60"
              />
            </div>
          )}

          {open === "whitelist" && (
            <div>
              <Label>Istisno qurilmalar (ixtiyoriy): ularga cheklov qo'llanmaydi</Label>
              <textarea
                value={exempt}
                onChange={(e) => setExempt(e.target.value)}
                rows={2}
                placeholder="10.0.0.5"
                className="w-full rounded-lg border border-white/10 bg-white/[0.03] px-3 py-2 font-mono text-sm text-neutral-100 outline-none focus:border-indigo-500/60"
              />
              <p className="mt-1.5 rounded-lg border border-amber-500/20 bg-amber-500/10 px-3 py-2 text-xs text-amber-200">
                Diqqat: ro'yxatda bo'lmagan barcha saytlar bloklanadi (Squid'ga o'z sahifalari ham). Istisno qurilmani
                kiriting yoki keyin qoidani "Qoidalar" bo'limidan o'chirib qo'yishingiz mumkin.
              </p>
            </div>
          )}

          {open === "users" && (
            <div>
              <Label>Ruxsat berilgan proksi foydalanuvchilari</Label>
              {users.length === 0 ? (
                <p className="text-xs text-neutral-500">Hali proksi foydalanuvchisi yo'q. Avval Foydalanuvchilar sahifasida qo'shing.</p>
              ) : (
                <div className="flex flex-wrap gap-2">
                  {users.map((u) => (
                    <label key={u} className="flex items-center gap-1.5 rounded-lg border border-white/10 px-2.5 py-1.5 text-xs text-neutral-300">
                      <input
                        type="checkbox"
                        checked={chosen.includes(u)}
                        onChange={(e) => setChosen(e.target.checked ? [...chosen, u] : chosen.filter((x) => x !== u))}
                      />
                      {u}
                    </label>
                  ))}
                </div>
              )}
            </div>
          )}

          <Button type="submit" disabled={busy} className="w-fit">
            {busy ? "Qo'llanmoqda…" : "Shablonni qo'llash"}
          </Button>
        </form>
      )}
    </Card>
  );
}
