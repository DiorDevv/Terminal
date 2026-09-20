import { AUTH_FILE, api, expect, seeToast, test } from "./helpers";

test.use({ storageState: AUTH_FILE });
test.describe.configure({ mode: "serial" });

const row = (page: import("@playwright/test").Page, text: string) => page.locator("li", { hasText: text }).first();
const sources = async (page: import("@playwright/test").Page) => (await api(page, "GET", "/squid/blocklists")).json.sources ?? [];

test("the page lists sources and offers ready-made ones", async ({ page }) => {
  await page.goto("/blocklists");
  await expect(page.getByRole("heading", { name: "Blocklist manbalari" }).first()).toBeVisible();
  await expect(page.getByText("Namuna manbalar")).toBeVisible();
  for (const s of ["stevenblack", "urlhaus"]) await expect(page.getByRole("button", { name: new RegExp(s) })).toBeVisible();

  // a sample fills the form in
  await page.getByRole("button", { name: /urlhaus/ }).click();
  await expect(page.getByPlaceholder("masalan: ads")).toHaveValue("urlhaus");
  await expect(page.getByPlaceholder("https://…/hosts")).toHaveValue(/urlhaus\.abuse\.ch/);
});

test("a wrong address scheme is refused; an address inside the network is never downloaded", async ({ page }) => {
  for (const x of await sources(page)) {
    if (!x.name.startsWith("e2e_")) continue;
    const r = await api(page, "DELETE", `/squid/blocklists/${x.id}?remove_rules=1`);
    expect(r.status, JSON.stringify(r.json)).toBe(200);
  }
  await page.goto("/blocklists");
  const before = (await sources(page)).length;

  // not http(s): refused outright, nothing stored
  for (const bad of ["ftp://example.com/hosts", "file:///etc/passwd", "example.com/hosts"]) {
    await page.getByPlaceholder("masalan: ads").fill("e2e_badurl");
    await page.getByPlaceholder("https://…/hosts").fill(bad);
    await page.getByRole("button", { name: "Qo'shish" }).click();
    await page.waitForTimeout(600);
    expect((await sources(page)).length).toBe(before);
  }

  // this machine / cloud metadata (a LAN address is allowed on purpose, so it is really tried): the source is recorded but the download is refused by the guard
  for (const [name, url] of [["e2e_local", "http://127.0.0.1:8080/api/health"], ["e2e_meta", "http://169.254.169.254/latest/meta-data"]]) {
    await page.getByPlaceholder("masalan: ads").fill(name);
    await page.getByPlaceholder("https://…/hosts").fill(url);
    await page.getByRole("button", { name: "Qo'shish" }).click();
    await expect(page.getByText(new RegExp(`"${name}" qo'shildi, lekin yuklab bo'lmadi`)).first()).toBeVisible();
    await expect(row(page, name)).toContainText("xato");
    const src = (await sources(page)).find((x: any) => x.name === name);
    expect(src.entry_count).toBe(0);
    expect(String(src.last_status)).toMatch(/^error/);
  }

  // and they are removed again, after a confirmation
  for (const name of ["e2e_local", "e2e_meta"]) {
    await row(page, name).getByRole("button", { name: "O'chirish" }).click();
    await page.locator(".fixed.inset-0").getByRole("button", { name: "O'chirish" }).last().click();
    await expect(row(page, name)).toHaveCount(0);
  }
  expect((await sources(page)).length).toBe(before);
  const acls = (await api(page, "GET", "/squid/access/acls")).json.acls ?? [];
  expect(acls.filter((a: any) => a.name.startsWith("bl_e2e_"))).toEqual([]);
});

test("a source can be refreshed for real, its interval changed, and switched off and on", async ({ page }) => {
  const list = await sources(page);
  test.skip(list.length === 0, "no source configured on this server");
  const s = list[0];
  await page.goto("/blocklists");
  const item = row(page, s.name);
  await expect(item).toContainText(/ta domen|xato|hali yuklanmagan/);

  await item.getByRole("button", { name: "Hozir yangilash" }).click();
  await expect(page.getByText(new RegExp(`"${s.name}" (yangilandi|o'zgarmagan)`))).toBeVisible({ timeout: 90_000 });
  await expect(item).toContainText("ta domen");

  // interval is saved when the field loses focus
  const hours = item.locator('input[type="number"]');
  await hours.fill("12");
  await hours.blur();
  await expect.poll(async () => (await sources(page)).find((x: any) => x.id === s.id)?.interval_hours).toBe(12);
  await hours.fill(String(s.interval_hours));
  await hours.blur();
  await expect.poll(async () => (await sources(page)).find((x: any) => x.id === s.id)?.interval_hours).toBe(s.interval_hours);

  // off: the source stops being enforced, on: it does again
  const sw = item.getByRole("switch").or(item.locator("button").nth(1)).first();
  const wasEnabled = s.enabled;
  await sw.click();
  await expect.poll(async () => (await sources(page)).find((x: any) => x.id === s.id)?.enabled).toBe(!wasEnabled);
  await sw.click();
  await expect.poll(async () => (await sources(page)).find((x: any) => x.id === s.id)?.enabled).toBe(wasEnabled);
});
