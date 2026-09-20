import { AUTH_FILE, api, confirmIfAsked, expect, seeToast, test } from "./helpers";

test.use({ storageState: AUTH_FILE });
test.describe.configure({ mode: "serial" });

const GROUP = "e2e_grp";
const RULE = "e2e_lunch";

async function squidConf(page: import("@playwright/test").Page): Promise<string> {
  const res = await api(page, "GET", "/squid/config");
  return String(res.json.content ?? "");
}

test("an IP group can be created and a bad address is refused", async ({ page }) => {
  // Start clean if an earlier interrupted run left its objects behind.
  for (const r of (await api(page, "GET", "/squid/restrictions")).json.restrictions ?? []) {
    if (r.name === RULE) await api(page, "DELETE", `/squid/restrictions/${r.id}`);
  }
  for (const g of (await api(page, "GET", "/squid/groups")).json.groups ?? []) {
    if (g.name === GROUP) await api(page, "DELETE", `/squid/groups/${g.id}`);
  }
  await page.goto("/groups");
  await page.getByPlaceholder("guruh nomi (masalan it_bolimi)").fill(GROUP);
  await page.getByPlaceholder("IP/CIDR, vergul bilan: 127.0.0.1, 10.0.0.0/24").fill("10.9.0.0/24, 10.9.1.5");
  await page.getByRole("button", { name: "Yaratish" }).click();
  await expect(page.locator("li", { hasText: GROUP })).toBeVisible();
  await expect(page.locator("li", { hasText: GROUP })).toContainText("10.9.0.0/24");

  await page.getByPlaceholder("guruh nomi (masalan it_bolimi)").fill("e2e_bad");
  await page.getByPlaceholder("IP/CIDR, vergul bilan: 127.0.0.1, 10.0.0.0/24").fill("not-an-ip");
  await page.getByRole("button", { name: "Yaratish" }).click();
  await expect(page.locator("li", { hasText: "e2e_bad" })).toHaveCount(0);
});

test("a time restriction with an exempt group reaches squid.conf", async ({ page }) => {
  await page.goto("/restrictions");
  await page.getByPlaceholder("ijtimoiy_tarmoq_ish_vaqti").fill(RULE);
  await page.getByPlaceholder("youtube.com, facebook.com").fill("e2e-social.example");
  await page.getByRole("button", { name: "Dush", exact: true }).click();
  await page.getByRole("button", { name: "Sesh", exact: true }).click();
  await page.getByText("Boshlanish vaqti").locator("xpath=following::input[1]").fill("12:00");
  await page.getByText("Tugash vaqti").locator("xpath=following::input[1]").fill("13:30");
  await page.locator("select").selectOption({ label: GROUP });
  await page.getByRole("button", { name: "Qoida yaratish" }).click();

  const row = page.locator("li", { hasText: RULE });
  await expect(row).toBeVisible();
  await expect(row).toContainText("e2e-social.example");
  await expect(row).toContainText("12:00");

  const conf = await squidConf(page);
  expect(conf).toContain("e2e-social.example");
  expect(conf).toMatch(/time [A-Z]*M[A-Z]*T?[A-Z]* 12:00-13:30|time MT 12:00-13:30/);
  expect(conf).toContain("10.9.0.0/24"); // the exempt group's addresses
});

test("a restriction needs a valid name and at least one day", async ({ page }) => {
  await page.goto("/restrictions");
  await page.getByPlaceholder("ijtimoiy_tarmoq_ish_vaqti").fill("ab");
  await page.getByPlaceholder("youtube.com, facebook.com").fill("x.example");
  await page.getByRole("button", { name: "Qoida yaratish" }).click();
  await expect(page.locator("li", { hasText: "x.example" })).toHaveCount(0);
});

test("the restriction and the group can be deleted", async ({ page }) => {
  await page.goto("/restrictions");
  const row = page.locator("li", { hasText: RULE });
  await row.getByRole("button", { name: /O'chirish/ }).click();
  await confirmIfAsked(page, "O'chirish");
  await expect(page.locator("li", { hasText: RULE })).toHaveCount(0);
  expect(await squidConf(page)).not.toContain("e2e-social.example");

  await page.goto("/groups");
  await page.locator("li", { hasText: GROUP }).getByRole("button", { name: /O'chirish/ }).click();
  await confirmIfAsked(page, "O'chirish");
  await expect(page.locator("li", { hasText: GROUP })).toHaveCount(0);
  await seeToast(page, /./);
});
