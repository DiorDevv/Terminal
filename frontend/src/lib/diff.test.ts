import { describe, expect, it } from "vitest";
import { diffLines, diffStats, toHunks } from "./diff";

const text = (lines: string[]) => lines.join("\n");

describe("diffLines", () => {
  it("identical texts have no changes", () => {
    const d = diffLines("a\nb\nc", "a\nb\nc");
    expect(d.every((l) => l.kind === "same")).toBe(true);
    expect(diffStats(d)).toEqual({ added: 0, removed: 0 });
  });

  it("finds an added, a removed and a changed line", () => {
    const d = diffLines(text(["a", "b", "c", "d"]), text(["a", "B", "c", "d", "e"]));
    expect(diffStats(d)).toEqual({ added: 2, removed: 1 });
    expect(d.filter((l) => l.kind === "del").map((l) => l.text)).toEqual(["b"]);
    expect(d.filter((l) => l.kind === "add").map((l) => l.text)).toEqual(["B", "e"]);
  });

  it("numbers lines on both sides", () => {
    const d = diffLines("a\nb", "a\nx\nb");
    const added = d.find((l) => l.kind === "add")!;
    expect(added.newNo).toBe(2);
    expect(added.oldNo).toBeUndefined();
    expect(d.find((l) => l.text === "b")).toMatchObject({ oldNo: 2, newNo: 3 });
  });

  it("applying the diff to the old text gives the new text", () => {
    const oldLines = Array.from({ length: 200 }, (_, i) => `line ${i}`);
    const newLines = [...oldLines];
    newLines.splice(50, 3, "changed");
    newLines.splice(120, 0, "inserted one", "inserted two");
    const d = diffLines(text(oldLines), text(newLines));
    expect(d.filter((l) => l.kind !== "del").map((l) => l.text)).toEqual(newLines);
    expect(d.filter((l) => l.kind !== "add").map((l) => l.text)).toEqual(oldLines);
  });

  it("handles a completely different text and empty input", () => {
    expect(diffStats(diffLines("a\nb", "x\ny\nz"))).toEqual({ added: 3, removed: 2 });
    expect(diffStats(diffLines("", ""))).toEqual({ added: 0, removed: 0 });
  });
});

describe("toHunks", () => {
  it("groups changes with context and drops the unchanged stretches between", () => {
    const oldLines = Array.from({ length: 40 }, (_, i) => `l${i}`);
    const newLines = [...oldLines];
    newLines[5] = "CHANGED-A";
    newLines[30] = "CHANGED-B";
    const hunks = toHunks(diffLines(text(oldLines), text(newLines)), 2);
    expect(hunks).toHaveLength(2);
    expect(hunks[0].lines.some((l) => l.text === "CHANGED-A")).toBe(true);
    expect(hunks[1].lines.some((l) => l.text === "CHANGED-B")).toBe(true);
    expect(hunks[0].lines.length).toBeLessThan(10);
  });

  it("has no hunks when nothing changed", () => {
    expect(toHunks(diffLines("a\nb", "a\nb"))).toEqual([]);
  });
});
