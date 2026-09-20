import { useState } from "react";
import { api, errorMessage } from "../lib/api";
import { useToast } from "../lib/toast";
import { Button } from "./ui/Button";
import { Input, Label } from "./ui/Input";

export const MIN_PASSWORD_LENGTH = 8;

/** Change-your-own-password form, used in Settings and on the forced-change screen. */
export function PasswordForm({
  oldLabel = "Joriy parol",
  onDone,
}: {
  oldLabel?: string;
  onDone?: () => void;
}) {
  const [oldPassword, setOldPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const toast = useToast();

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (newPassword !== confirmPassword) {
      toast.error("Yangi parollar bir xil emas");
      return;
    }
    if (newPassword.length < MIN_PASSWORD_LENGTH) {
      toast.error(`Yangi parol kamida ${MIN_PASSWORD_LENGTH} belgidan iborat bo'lishi kerak`);
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
      onDone?.();
    } catch (err) {
      toast.error(errorMessage(err, "Parolni o'zgartirib bo'lmadi"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="grid gap-4">
      <div>
        <Label>{oldLabel}</Label>
        <Input
          type="password"
          autoComplete="current-password"
          value={oldPassword}
          onChange={(e) => setOldPassword(e.target.value)}
        />
      </div>
      <div>
        <Label>Yangi parol</Label>
        <Input
          type="password"
          autoComplete="new-password"
          value={newPassword}
          onChange={(e) => setNewPassword(e.target.value)}
        />
        <p className="mt-1.5 text-[11px] text-neutral-500">
          Kamida {MIN_PASSWORD_LENGTH} belgi; "admin123" kabi oddiy parollar qabul qilinmaydi.
        </p>
      </div>
      <div>
        <Label>Yangi parolni tasdiqlash</Label>
        <Input
          type="password"
          autoComplete="new-password"
          value={confirmPassword}
          onChange={(e) => setConfirmPassword(e.target.value)}
        />
      </div>

      <Button type="submit" disabled={busy} className="mt-1 w-fit">
        {busy ? "Saqlanmoqda..." : "Parolni yangilash"}
      </Button>
    </form>
  );
}
