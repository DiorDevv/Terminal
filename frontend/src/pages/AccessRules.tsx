import { Boxes, Layers, ListOrdered, Search, ShieldHalf } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { Skeleton } from "../components/ui/Skeleton";
import { PageHeader } from "../components/ui/PageHeader";
import { api, errorMessage } from "../lib/api";
import type { Acl, AclType, Rule } from "../lib/access";
import { useToast } from "../lib/toast";
import AclsTab from "./access/AclsTab";
import PolicyTab from "./access/PolicyTab";
import RulesTab from "./access/RulesTab";
import TesterTab from "./access/TesterTab";

type Tab = "rules" | "acls" | "policy" | "test";

const tabs: { id: Tab; label: string; icon: React.ReactNode }[] = [
  { id: "rules", label: "Qoidalar", icon: <ListOrdered size={15} /> },
  { id: "acls", label: "Obyektlar (ACL)", icon: <Boxes size={15} /> },
  { id: "policy", label: "Samarali siyosat", icon: <Layers size={15} /> },
  { id: "test", label: "Nima uchun? (tekshirgich)", icon: <Search size={15} /> },
];

export default function AccessRules() {
  const toast = useToast();
  const [tab, setTab] = useState<Tab>("rules");
  const [rules, setRules] = useState<Rule[] | null>(null);
  const [acls, setAcls] = useState<Acl[]>([]);
  const [types, setTypes] = useState<AclType[]>([]);
  // Bumped after every change so the read-only policy view reloads.
  const [version, setVersion] = useState(0);

  const load = useCallback(async () => {
    const [r, a, t] = await Promise.all([
      api.get<{ rules: Rule[] }>("/squid/access/rules"),
      api.get<{ acls: Acl[] }>("/squid/access/acls"),
      api.get<{ types: AclType[] }>("/squid/access/types"),
    ]);
    setRules(r.data.rules);
    setAcls(a.data.acls);
    setTypes(t.data.types);
    setVersion((v) => v + 1);
  }, []);

  useEffect(() => {
    load().catch((err) => toast.error(errorMessage(err, "Yuklab bo'lmadi")));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div>
      <PageHeader
        icon={<ShieldHalf size={19} />}
        title="Kirish qoidalari"
        description="Kim, qaysi saytga, qachon kira olishini belgilang. Qoidalar yuqoridan pastga tekshiriladi; birinchi mos kelgani hal qiladi."
      />

      <div className="mb-6 flex flex-wrap gap-1.5 border-b border-white/[0.08] pb-px">
        {tabs.map((t) => (
          <button
            key={t.id}
            onClick={() => setTab(t.id)}
            className={`-mb-px flex items-center gap-2 rounded-t-lg border-b-2 px-4 py-2.5 text-[13px] font-medium transition-colors ${
              tab === t.id
                ? "border-indigo-500 text-white"
                : "border-transparent text-neutral-500 hover:text-neutral-200"
            }`}
          >
            {t.icon}
            {t.label}
          </button>
        ))}
      </div>

      {rules === null ? (
        <Skeleton className="h-64 w-full" />
      ) : (
        <>
          {tab === "rules" && <RulesTab rules={rules} acls={acls} reload={load} />}
          {tab === "acls" && <AclsTab acls={acls} types={types} reload={load} />}
          {tab === "policy" && <PolicyTab version={version} />}
          {tab === "test" && <TesterTab />}
        </>
      )}
    </div>
  );
}
