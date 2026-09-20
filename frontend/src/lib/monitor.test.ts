import { describe, expect, it } from "vitest";
import { agoFromUnix, formatCount, formatPercent, formatSeconds, niceScale } from "./monitor";

describe("niceScale", () => {
  it("has a sensible axis when there is no data", () => {
    expect(niceScale(0)).toEqual({ top: 4, step: 1 });
  });

  it("rounds the top of the axis up to a clean number", () => {
    expect(niceScale(87)).toEqual({ top: 100, step: 50 });
    expect(niceScale(3)).toEqual({ top: 3, step: 1 });
  });

  it("always covers the maximum and never draws more than a handful of ticks", () => {
    for (const max of [1, 2, 7, 13, 99, 100, 101, 999, 5000, 123456, 9_999_999]) {
      const { top, step } = niceScale(max);
      expect(top).toBeGreaterThanOrEqual(max);
      expect(top / step).toBeLessThanOrEqual(6);
      expect(top % step).toBe(0);
    }
  });
});

describe("formatters", () => {
  it("counts with thousands separators", () => {
    expect(formatCount(0)).toBe("0");
    expect(formatCount(1234567)).toBe("1,234,567");
  });

  it("percentages", () => {
    expect(formatPercent(1, 0)).toBe("—");
    expect(formatPercent(1, 8)).toBe("13%"); // 12.5 rounds up
    expect(formatPercent(1, 100)).toBe("1.0%");
    expect(formatPercent(50, 50)).toBe("100%");
  });

  it("durations in Uzbek", () => {
    expect(formatSeconds(-5)).toBe("0 soniya");
    expect(formatSeconds(59)).toBe("59 soniya");
    expect(formatSeconds(60 * 5 + 3)).toBe("5 daqiqa");
    expect(formatSeconds(3600 * 2 + 60 * 7)).toBe("2 soat 7 daqiqa");
    expect(formatSeconds(86400 * 3 + 3600 * 4)).toBe("3 kun 4 soat");
  });

  it("time since a unix timestamp", () => {
    const now = Date.UTC(2030, 0, 1, 12, 0, 0);
    const at = (secondsAgo: number) => now / 1000 - secondsAgo;
    expect(agoFromUnix(0, now)).toBe("—");
    expect(agoFromUnix(at(10), now)).toBe("hozirgina");
    expect(agoFromUnix(at(300), now)).toBe("5 daqiqa oldin");
    expect(agoFromUnix(at(7200), now)).toBe("2 soat oldin");
    expect(agoFromUnix(at(86400 * 3), now)).toBe("3 kun oldin");
  });
});
