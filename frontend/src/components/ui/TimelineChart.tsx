import { useEffect, useState } from "react";
import { type Bucket, formatCount, niceScale, type RangeName } from "../../lib/monitor";

// Stacked columns: allowed traffic with blocked requests on top. The two fills
// are slots 1 and 2 of the dark categorical palette, validated against the card
// surface (#0f1117): adjacent CVD ΔE 26.8, normal-vision ΔE 31.8, both ≥ 3:1.
const ALLOWED = "#3987e5";
const BLOCKED = "#d95926";

const HEIGHT = 220;
const M = { top: 10, right: 8, bottom: 24, left: 46 };
const GAP = 2; // surface-coloured gap between touching marks
const MAX_BAR = 24;

// A callback ref, so the observer follows the element when the chart is hidden
// (table view) and shown again.
function useWidth() {
  const [el, setEl] = useState<HTMLDivElement | null>(null);
  const [w, setW] = useState(0);
  useEffect(() => {
    if (!el) return;
    setW(el.clientWidth);
    const ro = new ResizeObserver(() => setW(el.clientWidth));
    ro.observe(el);
    return () => ro.disconnect();
  }, [el]);
  return [setEl, w] as const;
}

/** A column with rounded top corners and a square base. */
function column(x: number, y: number, w: number, h: number, r: number): string {
  const rr = Math.min(r, h, w / 2);
  return `M${x},${y + h}V${y + rr}Q${x},${y} ${x + rr},${y}H${x + w - rr}Q${x + w},${y} ${x + w},${y + rr}V${y + h}Z`;
}

function label(t: number, range: RangeName): string {
  const d = new Date(t * 1000);
  if (range === "24h") return d.toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit" });
  return d.toLocaleDateString("en-GB", { day: "2-digit", month: "short" });
}

export function TimelineChart({ buckets, range }: { buckets: Bucket[]; range: RangeName }) {
  const [ref, width] = useWidth();
  const [hover, setHover] = useState<number | null>(null);
  const [asTable, setAsTable] = useState(false);

  const plotW = Math.max(0, width - M.left - M.right);
  const plotH = HEIGHT - M.top - M.bottom;
  const n = buckets.length;
  const step = n ? plotW / n : 0;
  const barW = Math.max(2, Math.min(MAX_BAR, step - GAP));
  const max = Math.max(0, ...buckets.map((b) => b.requests));
  const { top, step: tickStep } = niceScale(max);
  const y = (v: number) => M.top + plotH - (v / top) * plotH;

  const ticks: number[] = [];
  for (let v = 0; v <= top; v += tickStep) ticks.push(v);
  // Label about six x positions, however many buckets there are.
  const every = Math.max(1, Math.ceil(n / 6));

  const hb = hover !== null ? buckets[hover] : null;
  const tipLeft = hover !== null ? M.left + step * hover + step / 2 : 0;

  return (
    <div>
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-4 text-xs text-neutral-400">
          <span className="flex items-center gap-1.5">
            <span className="h-2.5 w-2.5 rounded-sm" style={{ background: ALLOWED }} />
            Ruxsat etilgan
          </span>
          <span className="flex items-center gap-1.5">
            <span className="h-2.5 w-2.5 rounded-sm" style={{ background: BLOCKED }} />
            Bloklangan
          </span>
        </div>
        <button
          type="button"
          onClick={() => setAsTable((v) => !v)}
          className="text-xs text-neutral-500 underline-offset-2 hover:text-neutral-300 hover:underline"
        >
          {asTable ? "Grafikni ko'rsatish" : "Jadval ko'rinishi"}
        </button>
      </div>

      {asTable ? (
        <div className="max-h-[220px] overflow-auto">
          <table className="w-full text-left text-xs">
            <thead className="sticky top-0 bg-[#0f1117] text-neutral-500">
              <tr>
                <th className="py-1.5 pr-3 font-medium">Vaqt</th>
                <th className="py-1.5 pr-3 text-right font-medium">So'rovlar</th>
                <th className="py-1.5 pr-3 text-right font-medium">Bloklangan</th>
                <th className="py-1.5 text-right font-medium">Xatolar</th>
              </tr>
            </thead>
            <tbody className="text-neutral-300">
              {buckets.map((b) => (
                <tr key={b.t} className="border-t border-white/[0.05]">
                  <td className="py-1 pr-3">{label(b.t, range)}</td>
                  <td className="py-1 pr-3 text-right tabular-nums">{formatCount(b.requests)}</td>
                  <td className="py-1 pr-3 text-right tabular-nums">{formatCount(b.denied)}</td>
                  <td className="py-1 text-right tabular-nums">{formatCount(b.errors)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <div ref={ref} className="relative" style={{ height: HEIGHT }}>
          {width > 0 && (
            <svg
              width={width}
              height={HEIGHT}
              role="img"
              aria-label="So'rovlar vaqt bo'yicha"
              onMouseLeave={() => setHover(null)}
            >
              {ticks.map((v) => (
                <g key={v}>
                  <line x1={M.left} x2={width - M.right} y1={y(v)} y2={y(v)} stroke="rgba(255,255,255,0.07)" />
                  <text x={M.left - 8} y={y(v) + 4} textAnchor="end" fontSize="11" fill="#737373">
                    {formatCount(v)}
                  </text>
                </g>
              ))}

              {buckets.map((b, i) => {
                const cx = M.left + step * i + step / 2;
                const x = cx - barW / 2;
                const allowed = Math.max(0, b.requests - b.denied);
                const hAllowed = (allowed / top) * plotH;
                const hDenied = (b.denied / top) * plotH;
                const yAllowed = M.top + plotH - hAllowed;
                // A thin blocked segment stays visible; the 2px gap sits between segments.
                const dh = b.denied > 0 ? Math.max(hDenied, 2) : 0;
                const ah = allowed > 0 ? Math.max(hAllowed - (dh > 0 ? GAP : 0), 1) : 0;
                return (
                  <g key={b.t}>
                    {i % every === 0 && (
                      <text x={cx} y={HEIGHT - 6} textAnchor="middle" fontSize="11" fill="#737373">
                        {label(b.t, range)}
                      </text>
                    )}
                    {ah > 0 && (
                      <path d={column(x, M.top + plotH - ah, barW, ah, dh > 0 ? 0 : 4)} fill={ALLOWED} opacity={hover === null || hover === i ? 1 : 0.55} />
                    )}
                    {dh > 0 && (
                      <path d={column(x, yAllowed - dh, barW, dh, 4)} fill={BLOCKED} opacity={hover === null || hover === i ? 1 : 0.55} />
                    )}
                    {/* the hit target is the whole slot, far bigger than the mark */}
                    <rect
                      x={M.left + step * i}
                      y={M.top}
                      width={step}
                      height={plotH}
                      fill="transparent"
                      onMouseEnter={() => setHover(i)}
                    />
                  </g>
                );
              })}
              <line x1={M.left} x2={width - M.right} y1={y(0)} y2={y(0)} stroke="rgba(255,255,255,0.14)" />
            </svg>
          )}

          {hb && (
            <div
              className="pointer-events-none absolute z-10 min-w-36 rounded-lg border border-white/10 bg-[#14171f] px-3 py-2 text-xs shadow-lg"
              style={{
                left: Math.min(Math.max(tipLeft, 80), Math.max(80, width - 80)),
                top: 4,
                transform: "translateX(-50%)",
              }}
            >
              <div className="mb-1 font-medium text-neutral-200">{label(hb.t, range)}</div>
              <Row color={ALLOWED} name="Ruxsat etilgan" value={formatCount(Math.max(0, hb.requests - hb.denied))} />
              <Row color={BLOCKED} name="Bloklangan" value={formatCount(hb.denied)} />
              {hb.errors > 0 && <div className="mt-1 text-neutral-500">Server xatolari: {formatCount(hb.errors)}</div>}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function Row({ color, name, value }: { color: string; name: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-4 text-neutral-300">
      <span className="flex items-center gap-1.5">
        <span className="h-2 w-2 rounded-full" style={{ background: color }} />
        {name}
      </span>
      <span className="tabular-nums text-white">{value}</span>
    </div>
  );
}
