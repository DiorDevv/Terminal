import { describe, expect, it } from "vitest";
import { formatBytes, formatUptime, timeAgo } from "./format";

describe("formatBytes", () => {
  it("is a dash for nothing or nonsense", () => {
    expect(formatBytes(0)).toBe("—");
    expect(formatBytes(-1)).toBe("—");
  });

  it("scales through the units", () => {
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1024)).toBe("1.0 KB");
    expect(formatBytes(1536)).toBe("1.5 KB");
    expect(formatBytes(1024 * 1024 * 250)).toBe("250 MB");
    expect(formatBytes(1024 ** 3 * 3)).toBe("3.0 GB");
    expect(formatBytes(1024 ** 5)).toBe("1024 TB"); // the largest unit is TB
  });
});

describe("relative time", () => {
  const now = Date.UTC(2030, 0, 1, 12, 0, 0);

  it("uptime", () => {
    expect(formatUptime(0, now)).toBe("—");
    expect(formatUptime(now / 1000 - 30, now)).toBe("30 soniya");
    expect(formatUptime(now / 1000 - 3 * 86400 - 2 * 3600, now)).toBe("3 kun 2 soat");
  });

  it("time ago", () => {
    expect(timeAgo(new Date(now - 5_000).toISOString(), now)).toBe("hozirgina");
    expect(timeAgo(new Date(now - 90 * 60_000).toISOString(), now)).toBe("1 soat oldin");
    expect(timeAgo("not a date", now)).toBe("");
  });
});
