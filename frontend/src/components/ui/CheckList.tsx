/** A list of checkboxes for picking several items (users, groups, ...). */
export function CheckList<T extends string | number>({
  options,
  value,
  onChange,
  empty = "Tanlash uchun hech narsa yo'q",
}: {
  options: { value: T; label: string }[];
  value: T[];
  onChange: (next: T[]) => void;
  empty?: string;
}) {
  if (options.length === 0) return <p className="text-xs text-neutral-500">{empty}</p>;
  return (
    <div className="flex max-h-40 flex-wrap gap-2 overflow-auto rounded-lg border border-white/10 bg-white/[0.02] p-2">
      {options.map((o) => {
        const on = value.includes(o.value);
        return (
          <label
            key={String(o.value)}
            className={`flex cursor-pointer items-center gap-1.5 rounded-md border px-2 py-1 text-xs transition-colors ${
              on ? "border-indigo-500/50 bg-indigo-500/15 text-white" : "border-white/10 text-neutral-400 hover:text-neutral-200"
            }`}
          >
            <input
              type="checkbox"
              className="h-3 w-3 accent-indigo-500"
              checked={on}
              onChange={() => onChange(on ? value.filter((v) => v !== o.value) : [...value, o.value])}
            />
            {o.label}
          </label>
        );
      })}
    </div>
  );
}
