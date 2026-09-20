export type DiffKind = "same" | "add" | "del";

export interface DiffLine {
  kind: DiffKind;
  text: string;
  /** 1-based line number in the old text (absent for added lines). */
  oldNo?: number;
  /** 1-based line number in the new text (absent for removed lines). */
  newNo?: number;
}

export interface DiffHunk {
  lines: DiffLine[];
}

// Above this many DP cells the exact diff is skipped and the changed middle is
// shown as one removed block + one added block instead of exhausting memory.
const MAX_CELLS = 4_000_000;

/**
 * Line diff from `oldText` to `newText`. Common leading/trailing lines are
 * trimmed first, so the quadratic LCS only runs on the (usually tiny) region
 * that actually changed — squid.conf is ~2000 lines but edits touch a few.
 */
export function diffLines(oldText: string, newText: string): DiffLine[] {
  const a = oldText.split("\n");
  const b = newText.split("\n");

  let start = 0;
  while (start < a.length && start < b.length && a[start] === b[start]) start++;

  let endA = a.length;
  let endB = b.length;
  while (endA > start && endB > start && a[endA - 1] === b[endB - 1]) {
    endA--;
    endB--;
  }

  const out: DiffLine[] = [];
  for (let i = 0; i < start; i++) {
    out.push({ kind: "same", text: a[i], oldNo: i + 1, newNo: i + 1 });
  }

  const midA = a.slice(start, endA);
  const midB = b.slice(start, endB);
  out.push(...diffMiddle(midA, midB, start));

  for (let i = 0; i < a.length - endA; i++) {
    out.push({
      kind: "same",
      text: a[endA + i],
      oldNo: endA + i + 1,
      newNo: endB + i + 1,
    });
  }
  return out;
}

function diffMiddle(a: string[], b: string[], offset: number): DiffLine[] {
  const n = a.length;
  const m = b.length;
  const res: DiffLine[] = [];

  const del = (i: number) =>
    res.push({ kind: "del", text: a[i], oldNo: offset + i + 1 });
  const add = (j: number) =>
    res.push({ kind: "add", text: b[j], newNo: offset + j + 1 });

  if (n === 0) {
    for (let j = 0; j < m; j++) add(j);
    return res;
  }
  if (m === 0) {
    for (let i = 0; i < n; i++) del(i);
    return res;
  }
  if ((n + 1) * (m + 1) > MAX_CELLS) {
    for (let i = 0; i < n; i++) del(i);
    for (let j = 0; j < m; j++) add(j);
    return res;
  }

  // lcs[i][j] = length of the LCS of a[i:] and b[j:]
  const width = m + 1;
  const lcs = new Uint32Array((n + 1) * width);
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      lcs[i * width + j] =
        a[i] === b[j]
          ? lcs[(i + 1) * width + j + 1] + 1
          : Math.max(lcs[(i + 1) * width + j], lcs[i * width + j + 1]);
    }
  }

  let i = 0;
  let j = 0;
  while (i < n && j < m) {
    if (a[i] === b[j]) {
      res.push({
        kind: "same",
        text: a[i],
        oldNo: offset + i + 1,
        newNo: offset + j + 1,
      });
      i++;
      j++;
    } else if (lcs[(i + 1) * width + j] >= lcs[i * width + j + 1]) {
      del(i++);
    } else {
      add(j++);
    }
  }
  while (i < n) del(i++);
  while (j < m) add(j++);
  return res;
}

export function diffStats(lines: DiffLine[]): { added: number; removed: number } {
  let added = 0;
  let removed = 0;
  for (const l of lines) {
    if (l.kind === "add") added++;
    else if (l.kind === "del") removed++;
  }
  return { added, removed };
}

/**
 * Groups changed lines with `context` unchanged lines around them, dropping
 * the long unchanged stretches in between. Returns no hunks when nothing
 * changed.
 */
export function toHunks(lines: DiffLine[], context = 3): DiffHunk[] {
  const keep = new Array<boolean>(lines.length).fill(false);
  lines.forEach((l, idx) => {
    if (l.kind === "same") return;
    const from = Math.max(0, idx - context);
    const to = Math.min(lines.length - 1, idx + context);
    for (let k = from; k <= to; k++) keep[k] = true;
  });

  const hunks: DiffHunk[] = [];
  let current: DiffLine[] | null = null;
  lines.forEach((l, idx) => {
    if (!keep[idx]) {
      current = null;
      return;
    }
    if (!current) {
      current = [];
      hunks.push({ lines: current });
    }
    current.push(l);
  });
  return hunks;
}
