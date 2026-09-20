import { describe, expect, it } from "vitest";
import { describeWho, expiryToInput, formatExpiry, inputToExpiry, type LimitSpec } from "./userpolicy";

describe("expiry dates", () => {
  it("a date means valid through the whole of that day", () => {
    const at = inputToExpiry("2030-06-30");
    // the stored instant is local midnight of the NEXT day
    expect(at).toBe(Math.floor(new Date(2030, 5, 30 + 1, 0, 0, 0).getTime() / 1000));
    expect(expiryToInput(at)).toBe("2030-06-30");
  });

  it("round-trips every day of a year, including month and year ends", () => {
    for (let d = 0; d < 366; d++) {
      const day = new Date(2031, 0, 1 + d);
      const text = `${day.getFullYear()}-${String(day.getMonth() + 1).padStart(2, "0")}-${String(day.getDate()).padStart(2, "0")}`;
      expect(expiryToInput(inputToExpiry(text))).toBe(text);
    }
  });

  it("empty means never", () => {
    expect(inputToExpiry("")).toBe(0);
    expect(expiryToInput(0)).toBe("");
    expect(formatExpiry(0)).toBe("cheksiz");
    expect(formatExpiry(inputToExpiry("2032-02-29"))).toBe("2032-02-29");
  });
});

describe("describeWho", () => {
  const userGroups = [{ id: 1, name: "xodimlar", members: [] }];
  const ipGroups = [{ id: 7, name: "ofis", members: [] }];
  const base: LimitSpec = { everyone: false, users: null, user_groups: null, ip_groups: null };

  it("names everyone", () => {
    expect(describeWho({ ...base, everyone: true }, userGroups, ipGroups)).toBe("Hamma");
  });

  it("lists users, groups and IP groups", () => {
    const text = describeWho({ ...base, users: ["alice", "bob"], user_groups: [1], ip_groups: [7] }, userGroups, ipGroups);
    expect(text).toBe("alice, bob · guruh: xodimlar · IP: ofis");
  });

  it("does not crash on a group that no longer exists", () => {
    expect(describeWho({ ...base, user_groups: [99] }, userGroups, ipGroups)).toBe("guruh: #99");
    expect(describeWho(base, userGroups, ipGroups)).toBe("—");
  });
});
