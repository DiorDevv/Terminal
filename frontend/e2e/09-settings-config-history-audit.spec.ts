import { AUTH_FILE, api, confirmIfAsked, expect, seeToast, test } from "./helpers";

test.use({ storageState: AUTH_FILE });
test.describe.configure({ mode: "serial" });

const HOST = "e2e-proxy.local";
const conf = async (page: import("@playwright/test").Page) => String((await api(page, "GET", "/squid/config")).json.content ?? "");
const has = (text: string, line: string) => text.split("\n").some((l) => l.trim() === line);
const fieldOf = (page: import("@playwright/test").Page, label: string) =>
  page.getByText(label, { exact: true }).first().locator("xpath=ancestor::div[.//input][1]").locator("input").first();

test("a Squid setting is previewed as a diff, can be backed out of, then applied for real", async ({ page }) => {
  await api(page, "PUT", "/squid/settings", { values: {}, reset: ["visible_hostname"] }); // start clean
  await page.goto("/squid-settings");
  await expect(page.getByText("Ko'rinadigan host nomi")).toBeVisible();
  const before = await conf(page);
  expect(has(before, `visible_hostname ${HOST}`)).toBe(false);

  await fieldOf(page, "Ko'rinadigan host nomi").fill(HOST);
  await expect(page.getByText("1 ta o'zgarish saqlanmagan")).toBeVisible();
  await page.getByRole("button", { name: "Ko'rib chiqish va qo'llash" }).click();
  await expect(page.getByText("Quyidagi o'zgarishlar qo'llanadi")).toBeVisible();
  await expect(page.getByText(HOST).first()).toBeVisible();

  // going back changes nothing on the server
  await page.getByRole("button", { name: "Orqaga" }).click();
  expect(has(await conf(page), `visible_hostname ${HOST}`)).toBe(false);

  await page.getByRole("button", { name: "Ko'rib chiqish va qo'llash" }).click();
  await page.locator(".fixed.inset-0").getByRole("button", { name: "Saqlash va qo'llash" }).click();
  await expect(page.getByText("saqlanmagan")).toHaveCount(0);
  expect(has(await conf(page), `visible_hostname ${HOST}`)).toBe(true);

  // squid is still up on the new configuration
  const st = await api(page, "GET", "/squid/status");
  expect(JSON.stringify(st.json)).toMatch(/running|active|true/i);
});

test("a setting goes back to its original value from squid.conf", async ({ page }) => {
  await page.goto("/squid-settings");
  const field = page.getByText("Ko'rinadigan host nomi", { exact: true }).first().locator("xpath=ancestor::div[.//button][1]");
  await field.getByRole("button", { name: "Asl holatiga qaytarish" }).click();
  await expect(page.getByText("Saqlanganda squid.conf'dagi asl holatiga qaytariladi.")).toBeVisible();
  await page.getByRole("button", { name: "Ko'rib chiqish va qo'llash" }).click();
  await page.locator(".fixed.inset-0").getByRole("button", { name: "Saqlash va qo'llash" }).click();
  await expect(page.getByText("saqlanmagan")).toHaveCount(0);
  expect(has(await conf(page), `visible_hostname ${HOST}`)).toBe(false);
});

test("a nonsense value is not written to squid.conf", async ({ page }) => {
  await page.goto("/squid-settings");
  const before = await conf(page);
  await fieldOf(page, "Kesh uchun operativ xotira").fill("abc-not-a-size");
  await page.getByRole("button", { name: "Ko'rib chiqish va qo'llash" }).click();
  // Either the panel refuses at once, or squid's own check does in the preview/apply; never a silent write.
  const overlay = page.locator(".fixed.inset-0");
  if (await overlay.isVisible({ timeout: 4000 }).catch(() => false)) {
    await overlay.getByRole("button", { name: "Saqlash va qo'llash" }).click().catch(() => {});
  }
  await page.waitForTimeout(1500);
  expect(await conf(page)).toBe(before);
  expect(await conf(page)).not.toContain("abc-not-a-size");
  await page.reload();
});

test("the raw configuration opens, saves unchanged, and a broken edit is refused", async ({ page }) => {
  await page.goto("/config");
  const area = page.locator("textarea");
  await expect(area).toHaveValue(/http_port/);

  await page.getByRole("button", { name: "Saqlash va qo'llash" }).click();
  await seeToast(page, "squid.conf saqlandi, tekshirildi va qo'llandi");

  const before = await conf(page);
  // the file is large: append at the end in one step instead of re-filling all of it
  await area.click();
  await page.keyboard.press("Control+End");
  await page.keyboard.insertText("\nthis_is_not_a_squid_directive yes\n");
  await expect(area).toHaveValue(/this_is_not_a_squid_directive/);
  await page.getByRole("button", { name: "Saqlash va qo'llash" }).click();
  await expect(page.getByText(/xato|invalid|parse|noto'g'ri|unknown|Unrecognized/i).first()).toBeVisible();
  const after = await conf(page);
  expect(after).toBe(before); // squid's own check stopped it; the live file is untouched
  expect(after).not.toContain("this_is_not_a_squid_directive");
});

test("history lists versions, shows the diff against the current file and restores a version", async ({ page }) => {
  // two real versions through the API: one with the hostname, a newer one without it
  const put = async (body: object) => (await api(page, "PUT", "/squid/settings", body)).status;
  expect(await put({ values: { visible_hostname: [HOST] }, reset: [] })).toBe(200);
  expect(await put({ values: {}, reset: ["visible_hostname"] })).toBe(200);
  expect(has(await conf(page), `visible_hostname ${HOST}`)).toBe(false);

  await page.goto("/history");
  await expect(page.getByText("joriy").first()).toBeVisible();
  const items = page.locator("ul > li button");
  expect(await items.count()).toBeGreaterThan(3);
  await expect(page.getByText("Versiyani tanlang")).toBeVisible();

  // the newest version equals the current file
  await items.first().click();
  await expect(page.getByText("Farq yo'q")).toBeVisible();

  // the one before it had the hostname: the diff shows exactly that, and it can be restored
  await items.nth(1).click();
  await expect(page.getByText(/Qaytarilsa nima o'zgaradi/)).toBeVisible();
  await expect(page.getByText(HOST).first()).toBeVisible();
  await page.getByRole("button", { name: "Shu versiyaga qaytarish" }).click();
  await confirmIfAsked(page, "Qaytarish");
  await expect.poll(async () => has(await conf(page), `visible_hostname ${HOST}`), { timeout: 20_000 }).toBe(true);

  // and back again the same way: the previous version is now the second in the list
  await expect(page.getByText(/restored version #\d+/).first()).toBeVisible();
  await expect(page.getByText("Versiyani tanlang")).toBeVisible(); // the list reloaded, nothing selected
  await items.nth(1).click();
  await expect(page.getByText(/Qaytarilsa nima o'zgaradi/)).toBeVisible();
  await page.getByRole("button", { name: "Shu versiyaga qaytarish" }).click();
  await confirmIfAsked(page, "Qaytarish");
  await expect.poll(async () => has(await conf(page), `visible_hostname ${HOST}`), { timeout: 20_000 }).toBe(false);
});

test("the audit log records what was done, filters by user and shows nothing for a stranger", async ({ page }) => {
  await page.goto("/audit");
  for (const col of ["Vaqt", "Foydalanuvchi", "Amal", "Natija", "IP"]) {
    await expect(page.locator("th", { hasText: col })).toBeVisible();
  }
  await expect(page.locator("tbody tr").first()).toBeVisible();
  await expect(page.locator("tbody")).toContainText("admin");

  // the history restore and the setting changes above must be in there, and no password anywhere
  const all = JSON.stringify((await api(page, "GET", "/audit?limit=200")).json);
  expect(all).toMatch(/restore|history|config/i);
  expect(all).not.toContain("Own-Chosen-Pass-2026");
  expect(all).not.toContain("Temporary-Pass-2026");

  const filter = page.getByPlaceholder("Foydalanuvchi nomi bo'yicha filtr (bo'sh = hammasi)");
  await filter.fill("e2e_viewer");
  await page.getByRole("button", { name: "Yangilash" }).click();
  await expect(page.locator("tbody")).toContainText("e2e_viewer");
  await expect(page.locator("tbody")).not.toContainText("blacklist");

  await filter.fill("nobody-with-this-name");
  await page.getByRole("button", { name: "Yangilash" }).click();
  await expect(page.getByText("Yozuvlar topilmadi")).toBeVisible();
});
