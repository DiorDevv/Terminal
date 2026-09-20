import { KeyRound } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "./ui/Button";
import { Input, Label } from "./ui/Input";

const MIN_LENGTH = 8;

/**
 * Asks for a temporary password for another account. Replaces window.prompt,
 * which showed the password in clear text on screen and cannot be styled or
 * validated.
 */
export function ResetPasswordDialog({
  username,
  busy,
  onSubmit,
  onCancel,
}: {
  username: string;
  busy: boolean;
  onSubmit: (password: string) => void;
  onCancel: () => void;
}) {
  const [password, setPassword] = useState("");

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onCancel();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onCancel]);

  const valid = password.length >= MIN_LENGTH;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm">
      <form
        role="dialog"
        aria-modal="true"
        aria-labelledby="reset-title"
        className="w-full max-w-sm rounded-2xl border border-white/[0.08] bg-[#14171f] p-6 shadow-[0_20px_60px_-15px_rgba(0,0,0,0.9)]"
        onSubmit={(e) => {
          e.preventDefault();
          if (valid) onSubmit(password);
        }}
      >
        <div className="mb-3 flex items-center gap-2.5">
          <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-indigo-500/15 text-indigo-400 ring-1 ring-indigo-500/20">
            <KeyRound size={18} />
          </div>
          <h3 id="reset-title" className="font-semibold text-white">
            {username} uchun vaqtinchalik parol
          </h3>
        </div>
        <p className="mb-4 text-sm leading-relaxed text-neutral-400">
          Foydalanuvchi keyingi kirishda o'zi yangi parol tanlaydi va hamma qurilmalardan chiqariladi.
        </p>
        <Label>Vaqtinchalik parol (kamida {MIN_LENGTH} belgi)</Label>
        <Input
          autoFocus
          type="password"
          autoComplete="new-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        <div className="mt-5 flex justify-end gap-2">
          <Button type="button" variant="ghost" onClick={onCancel}>
            Bekor qilish
          </Button>
          <Button type="submit" disabled={!valid || busy}>
            O'rnatish
          </Button>
        </div>
      </form>
    </div>
  );
}
