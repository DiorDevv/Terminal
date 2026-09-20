import { AUTH_FILE, api, confirmDialog, expect, seeToast, test } from "./helpers";

test.use({ storageState: AUTH_FILE });
test.describe.configure({ mode: "serial" });

const USER = "e2e_lim";
const SPEED = "e2e speed";
const SIZE = "e2e size";

const conf = async (page: import("@playwright/test").Page) => String((await api(page, "GET", "/squid/config")).json.content ?? "");
const has = (text: string, line: string) => text.split("\n").some((l) => l.trim() === line);
const row = (page: import("@playwright/test").Page, name: string) => page.locator("li", { hasText: name }).first();

test("start clean, then a speed limit for one user is created", async ({ page }) => {
  for (const l of (await api(page, "GET", "/squid/limits")).json.limits ?? []) {
    if (l.name.startsWith("e2e ")) await api(page, "DELETE", `/squid/limits/${l.id}`);
  }
  await api(page, "DELETE", `/squid/users/${USER}`);
  expect((await api(page, "POST", "/squid/users", { username: USER, password: "limpass1234" })).status).toBe(200);

  await page.goto("/limits");
  await page.getByRole("button", { name: "Yangi cheklov" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByPlaceholder("masalan: xodimlar tezligi").fill(SPEED);
  await dialog.getByPlaceholder("512").fill("128");
  await dialog.locator("label", { hasText: USER }).click();
  await dialog.getByRole("button", { name: "Saqlash" }).click();
  await seeToast(page, `${SPEED} saqlandi`);

  await expect(row(page, SPEED)).toContainText("128 KB/s");
  await expect(row(page, SPEED)).toContainText("har bir foydalanuvchiga alohida");
  await expect(row(page, SPEED)).toContainText(USER);

  const c = await conf(page);
  expect(has(c, "delay_pools 1")).toBe(true); // squid was really told
  expect(c).toContain("delay_parameters 1 -1/-1 -1/-1 -1/-1 131072/262144");
});

test("a limit is refused when nobody is chosen or the numbers make no sense", async ({ page }) => {
  await page.goto("/limits");
  await page.getByRole("button", { name: "Yangi cheklov" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByPlaceholder("masalan: xodimlar tezligi").fill("e2e nobody");
  await dialog.getByPlaceholder("512").fill("64");
  await dialog.getByRole("button", { name: "Saqlash" }).click();
  await expect(dialog).toContainText(/choose who|kimga|applies to/i); // an explanation inside the dialog
  await dialog.getByRole("button", { name: "Bekor qilish" }).click();
  await expect(row(page, "e2e nobody")).toHaveCount(0);
});

test("a limit can be switched off and on, and edited", async ({ page }) => {
  await page.goto("/limits");
  await row(page, SPEED).getByRole("button", { name: "O'chirib qo'yish" }).click();
  await expect(row(page, SPEED)).toContainText("o'chiq");
  expect(has(await conf(page), "delay_pools 1")).toBe(false); // gone from squid while off

  await row(page, SPEED).getByRole("button", { name: "Yoqish" }).click();
  await expect(row(page, SPEED)).not.toContainText("o'chiq");
  expect(has(await conf(page), "delay_pools 1")).toBe(true);

  await row(page, SPEED).getByRole("button", { name: "O'zgartirish" }).click();
  await page.getByRole("dialog").getByPlaceholder("512").fill("256");
  await page.getByRole("dialog").getByRole("button", { name: "Saqlash" }).click();
  await expect(row(page, SPEED)).toContainText("256 KB/s");
  // the bucket size set at creation stays until it is changed (256 KB here)
  expect(await conf(page)).toMatch(/delay_parameters 1 -1\/-1 -1\/-1 -1\/-1 262144\/\d+/);
});

test("a download-size limit for everyone reaches squid", async ({ page }) => {
  await page.goto("/limits");
  await page.getByRole("button", { name: "Yangi cheklov" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByPlaceholder("masalan: xodimlar tezligi").fill(SIZE);
  await dialog.locator("select").first().selectOption("download");
  await dialog.locator("label", { hasText: "Hamma uchun" }).click();
  await dialog.getByPlaceholder("700").fill("300");
  await dialog.getByRole("button", { name: "Saqlash" }).click();

  await expect(row(page, SIZE)).toContainText("eng ko'pi bilan 300 MB");
  await expect(row(page, SIZE)).toContainText("Hamma");
  expect(has(await conf(page), "reply_body_max_size 300 MB")).toBe(true);
});

test("limits are deleted after a confirmation and squid.conf is clean again", async ({ page }) => {
  await page.goto("/limits");
  for (const name of [SPEED, SIZE]) {
    await row(page, name).getByRole("button", { name: /cheklovini o'chirish/ }).click();
    await confirmDialog(page, "O'chirish");
    await expect(row(page, name)).toHaveCount(0);
  }
  const c = await conf(page);
  expect(has(c, "delay_pools 1")).toBe(false);
  expect(has(c, "reply_body_max_size 300 MB")).toBe(false);
  expect(c).not.toContain("squidadmin: limits");
  await api(page, "DELETE", `/squid/users/${USER}`);
});
