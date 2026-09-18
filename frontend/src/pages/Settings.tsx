import { KeyRound, Settings as SettingsIcon } from "lucide-react";
import { useState } from "react";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";
import { IconBadge } from "../components/ui/IconBadge";
import { Input, Label } from "../components/ui/Input";
import { PageHeader } from "../components/ui/PageHeader";
import { api } from "../lib/api";
import { useToast } from "../lib/toast";

export default function Settings() {
  const [oldPassword, setOldPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const toast = useToast();

  async function changePassword(e: React.FormEvent) {
    e.preventDefault();
    if (newPassword !== confirmPassword) {
      toast.error("Yangi parollar bir xil emas");
      return;
    }
    if (newPassword.length < 6) {
      toast.error("Yangi parol kamida 6 belgidan iborat bo'lishi kerak");
      return;
    }

    setBusy(true);
    try {
      await api.put("/auth/password", {
        old_password: oldPassword,
        new_password: newPassword,
      });
      toast.success("Parol muvaffaqiyatli o'zgartirildi");
      setOldPassword("");
      setNewPassword("");
      setConfirmPassword("");
    } catch (err: any) {
      toast.error(err.response?.data?.error ?? "Parolni o'zgartirib bo'lmadi");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div>
      <PageHeader icon={<SettingsIcon size={19} />} title="Sozlamalar" />

      <Card className="max-w-md p-6">
        <div className="mb-5 flex items-center gap-2.5">
          <IconBadge size={32}>
            <KeyRound size={15} />
          </IconBadge>
          <span className="text-sm font-medium text-neutral-200">
            Admin parolini o'zgartirish
          </span>
        </div>

        <form onSubmit={changePassword} className="grid gap-4">
          <div>
            <Label>Joriy parol</Label>
            <Input
              type="password"
              value={oldPassword}
              onChange={(e) => setOldPassword(e.target.value)}
            />
          </div>
          <div>
            <Label>Yangi parol</Label>
            <Input
              type="password"
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
            />
          </div>
          <div>
            <Label>Yangi parolni tasdiqlash</Label>
            <Input
              type="password"
              value={confirmPassword}
              onChange={(e) => setConfirmPassword(e.target.value)}
            />
          </div>

          <Button type="submit" disabled={busy} className="mt-1 w-fit">
            {busy ? "Saqlanmoqda..." : "Parolni yangilash"}
          </Button>
        </form>
      </Card>
    </div>
  );
}
