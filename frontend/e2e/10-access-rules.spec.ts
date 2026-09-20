import { execSync } from "node:child_process";
import * as os from "node:os";
import { AUTH_FILE, api, confirmDialog, expect, seeToast, test } from "./helpers";

test.use({ storageState: AUTH_FILE });
test.describe.configure({ mode: "serial" });

const ACL = "e2e_games";
const SITE = "e2e-games.example";
const KW_NAME = "e2e_kw";
const KEYWORD = "e2ebadword";

function lanIP(): string {
  for (const list of Object.values(os.networkInterfaces())) {
    for (const i of list ?? []) if (i.family === "IPv4" && !i.internal) return i.address;
  }
  throw new Error("no network address");
}
/** One real request through the running squid; returns the HTTP status. */
function viaSquid(url: string): string {
  try {
    return execSync(`curl -s -m 10 -o /dev/null -x http://127.0.0.1:3128 -w '%{http_code}' ${url}`).toString();
  } catch {
    return "curl-failed";
  }
}
const conf = async (page: import("@playwright/test").Page) => String((await api(page, "GET", "/squid/config")).json.content ?? "");
const tab = (page: import("@playwright/test").Page, name: string) => page.getByRole("button", { name });
const row = (page: import("@playwright/test").Page, text: string) => page.locator("li", { hasText: text }).first();

async function openAccess(page: import("@playwright/test").Page) {
  await page.goto("/");
  await page.locator("nav").getByRole("link", { name: "Kirish qoidalari", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Kirish qoidalari" }).first()).toBeVisible();
}

test("start clean, then an ACL object is created; duplicates and empty values are refused", async ({ page }) => {
  const rules = (await api(page, "GET", "/squid/access/rules")).json.rules ?? [];
  for (const r of rules) if (JSON.stringify(r).includes("e2e_")) await api(page, "DELETE", `/squid/access/rules/${r.id}`);
  for (const a of (await api(page, "GET", "/squid/access/acls")).json.acls ?? []) {
    if (a.name.startsWith("e2e_")) await api(page, "DELETE", `/squid/access/acls/${a.id}`);
  }

  await openAccess(page);
  await tab(page, "Obyektlar (ACL)").click();

  // nothing to save: refused
  await page.getByPlaceholder("masalan: social").fill(ACL);
  await page.getByRole("button", { name: "Yaratish" }).click();
  await expect(row(page, ACL)).toHaveCount(0);

  await page.locator("select").first().selectOption({ label: "Domenlar" });
  await page.locator("textarea").fill(SITE);
  await page.getByRole("button", { name: "Yaratish" }).click();
  await seeToast(page, `"${ACL}" yaratildi`);
  await expect(row(page, ACL)).toContainText(SITE);

  // the same name again is refused
  await page.getByPlaceholder("masalan: social").fill(ACL);
  await page.locator("textarea").fill("other.example");
  await page.getByRole("button", { name: "Yaratish" }).click();
  await expect(page.getByText(/mavjud|band|already|exists/i).first()).toBeVisible();
});

test("an ACL can be edited", async ({ page }) => {
  await openAccess(page);
  await tab(page, "Obyektlar (ACL)").click();
  await row(page, ACL).getByRole("button", { name: "Tahrirlash" }).click();
  await expect(page.getByText(`"${ACL}" ni tahrirlash`)).toBeVisible();
  await page.locator("textarea").fill(`${SITE}\ne2e-second.example`);
  await page.getByRole("button", { name: "Saqlash" }).click();
  await expect(row(page, ACL)).toContainText("e2e-second.example");
});

test("a rule built from the ACL really blocks traffic, and can be switched off and on", async ({ page }) => {
  await openAccess(page);
  await page.getByPlaceholder("Nima uchun bu qoida?").fill("e2e block");
  await page.locator("select", { hasText: "ACL tanlang…" }).selectOption({ label: `${ACL} (dstdomain)` });
  await page.getByRole("button", { name: "Qo'shish", exact: true }).click();
  await seeToast(page, "Qoida qo'shildi");
  await expect(row(page, "e2e block")).toContainText(ACL);

  await expect.poll(() => viaSquid(`http://${SITE}/x`), { timeout: 20_000 }).toBe("403");
  await expect.poll(() => viaSquid(`http://e2e-second.example/x`), { timeout: 20_000 }).toBe("403");

  // an ACL that a rule uses cannot be deleted
  await tab(page, "Obyektlar (ACL)").click();
  await expect(row(page, ACL).getByRole("button", { name: "O'chirish" })).toBeDisabled();
  await tab(page, "Qoidalar").click();

  // switch off: the site is no longer refused
  await row(page, "e2e block").getByRole("switch").or(row(page, "e2e block").locator("button").nth(2)).first().click();
  await expect(row(page, "e2e block")).toHaveClass(/opacity-50/);
  await expect.poll(() => viaSquid(`http://${SITE}/x`), { timeout: 20_000 }).not.toBe("403");

  await row(page, "e2e block").getByRole("switch").or(row(page, "e2e block").locator("button").nth(2)).first().click();
  await expect(row(page, "e2e block")).not.toHaveClass(/opacity-50/);
  await expect.poll(() => viaSquid(`http://${SITE}/x`), { timeout: 20_000 }).toBe("403");
});

test("a rule with no condition is refused", async ({ page }) => {
  await openAccess(page);
  await page.getByRole("button", { name: "Qo'shish", exact: true }).click();
  await seeToast(page, "Kamida bitta shart tanlang");
});

test("the tester explains the verdict, and the effective policy lists the rule", async ({ page }) => {
  await openAccess(page);
  await tab(page, "Nima uchun? (tekshirgich)").click();
  await page.getByPlaceholder("https://www.youtube.com/watch").fill(`http://${SITE}/page`);
  await page.getByRole("button", { name: "Tekshirish" }).click();
  await expect(page.getByText("RAD ETILADI")).toBeVisible();
  await expect(page.getByText(/Hal qilgan qoida/)).toBeVisible();
  await expect(page.getByText(ACL).first()).toBeVisible();

  // an unrelated address is not blocked by that rule
  await page.getByPlaceholder("https://www.youtube.com/watch").fill("http://www.wikipedia.org/");
  await page.getByRole("button", { name: "Tekshirish" }).click();
  await expect(page.getByText("RAD ETILADI")).toHaveCount(0);

  await tab(page, "Samarali siyosat").click();
  await expect(page.getByText("Samarali siyosat: Squid haqiqatda shu tartibda tekshiradi")).toBeVisible();
  await expect(page.getByText(ACL).first()).toBeVisible();
});

test("a rule can be moved up and down", async ({ page }) => {
  await openAccess(page);
  await page.getByPlaceholder("Nima uchun bu qoida?").fill("e2e second");
  await page.locator("select", { hasText: "ACL tanlang…" }).selectOption({ label: `${ACL} (dstdomain)` });
  await page.getByRole("button", { name: "Qo'shish", exact: true }).click();
  await seeToast(page, "Qoida qo'shildi");

  const pos = async (text: string) => Number((await row(page, text).locator("span").first().innerText()).trim());
  const first = await pos("e2e block");
  const second = await pos("e2e second");
  expect(second).toBeGreaterThan(first);

  await row(page, "e2e second").getByRole("button", { name: "Yuqoriga" }).click();
  await expect.poll(async () => (await pos("e2e second")) < (await pos("e2e block"))).toBe(true);
  await row(page, "e2e second").getByRole("button", { name: "Pastga" }).click();
  await expect.poll(async () => (await pos("e2e second")) > (await pos("e2e block"))).toBe(true);
});

test("a keyword template creates its objects and rule, and blocks by keyword", async ({ page }) => {
  await openAccess(page);
  await page.getByRole("button", { name: /Kalit so'z bilan bloklash/ }).click();
  await page.locator("form").filter({ hasText: "Shablonni qo'llash" }).locator("input").first().fill(KW_NAME);
  await page.locator("textarea").last().fill(KEYWORD);
  await page.getByRole("button", { name: "Shablonni qo'llash" }).click();
  await expect(page.getByText(new RegExp(KW_NAME)).first()).toBeVisible();

  const target = `http://${lanIP()}:8080/api/${KEYWORD}`;
  await expect.poll(() => viaSquid(target), { timeout: 20_000 }).toBe("403");
  expect(viaSquid(`http://${lanIP()}:8080/api/health`)).toBe("200");
});

/** Deletes list rows containing `text` one at a time through the UI, re-reading the list after each. */
async function deleteRows(page: import("@playwright/test").Page, text: string) {
  for (let round = 0; round < 12; round++) {
    const li = page.locator("li", { hasText: text }).first();
    if ((await li.count()) === 0) return;
    const btn = li.getByRole("button", { name: "O'chirish" });
    if (await btn.isDisabled().catch(() => true)) {
      await page.waitForTimeout(700); // still busy, or an ACL a rule still uses
      continue;
    }
    await btn.click({ timeout: 5000 }).catch(() => {});
    if (await page.locator(".fixed.inset-0").first().isVisible({ timeout: 1500 }).catch(() => false)) {
      await confirmDialog(page, "O'chirish");
    }
    await page.waitForTimeout(700);
  }
}

test("rules and objects are deleted after a confirmation and squid.conf is clean again", async ({ page }) => {
  await openAccess(page);
  for (const text of ["e2e second", "e2e block", "Kalit so'z bo'yicha bloklash"]) await deleteRows(page, text);
  await tab(page, "Obyektlar (ACL)").click();
  await deleteRows(page, "e2e_");

  const leftover = ((await api(page, "GET", "/squid/access/acls")).json.acls ?? []).filter((a: any) => a.name.startsWith("e2e_"));
  expect(leftover).toEqual([]);
  const rules = (await api(page, "GET", "/squid/access/rules")).json.rules ?? [];
  expect(JSON.stringify(rules)).not.toContain("e2e_");
  expect(await conf(page)).not.toContain("e2e_");
  await expect.poll(() => viaSquid(`http://${SITE}/x`), { timeout: 20_000 }).not.toBe("403");
});
