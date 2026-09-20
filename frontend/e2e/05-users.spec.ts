import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { AUTH_FILE, api, confirmDialog, expect, seeToast, test } from "./helpers";

test.use({ storageState: AUTH_FILE });
test.describe.configure({ mode: "serial" });

const USER = "e2e_user";
const CSV_USER = "e2e_csv";
const TEAM = "e2e_team";

const conf = async (page: import("@playwright/test").Page) => String((await api(page, "GET", "/squid/config")).json.content ?? "");
const row = (page: import("@playwright/test").Page, name: string) => page.locator("li", { hasText: name }).first();

test("start clean, then add a proxy user; short names and passwords are refused", async ({ page }) => {
  for (const u of [USER, CSV_USER]) await api(page, "DELETE", `/squid/users/${u}`);
  for (const g of (await api(page, "GET", "/squid/user-groups")).json.groups ?? []) {
    if (g.name === TEAM) await api(page, "DELETE", `/squid/user-groups/${g.id}`);
  }

  await page.goto("/users");
  await page.getByPlaceholder("foydalanuvchi nomi").fill("ab");
  await page.getByPlaceholder("parol").fill("longenough123");
  await page.getByRole("button", { name: "Qo'shish" }).click();
  await expect(row(page, "ab").filter({ hasText: /^ab/ })).toHaveCount(0);

  await page.getByPlaceholder("foydalanuvchi nomi").fill(USER);
  await page.getByPlaceholder("parol").fill("abc");
  await page.getByRole("button", { name: "Qo'shish" }).click();
  await expect(row(page, USER)).toHaveCount(0);

  await page.getByPlaceholder("parol").fill("e2epass1234");
  await page.getByRole("button", { name: "Qo'shish" }).click();
  await seeToast(page, `${USER} qo'shildi`);
  await expect(row(page, USER)).toContainText("Faol");
});

test("an account can be switched off without deleting it, and on again", async ({ page }) => {
  await page.goto("/users");
  await row(page, USER).getByRole("button", { name: "To'xtatish" }).click();
  await expect(row(page, USER)).toContainText("O'chirilgan");
  await expect(row(page, USER).getByRole("button", { name: "Yoqish" })).toBeVisible();
  expect(await conf(page)).toContain(`proxy_auth ${USER}`); // squid really has it blocked

  await row(page, USER).getByRole("button", { name: "Yoqish" }).click();
  await expect(row(page, USER)).toContainText("Faol");
  expect(await conf(page)).not.toContain(`proxy_auth ${USER}`);
});

test("expiry, daily quota and a note are saved and shown", async ({ page }) => {
  await page.goto("/users");
  await row(page, USER).getByRole("button", { name: "Sozlamalar" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText(`${USER} sozlamalari`);
  await dialog.locator('input[type="date"]').fill("2031-06-30");
  await dialog.locator('input[type="number"]').fill("5");
  await dialog.getByPlaceholder("masalan: sinov muddati").fill("sinov muddati");
  await dialog.getByRole("button", { name: "Saqlash" }).click();
  await seeToast(page, `${USER} sozlamalari saqlandi`);

  await expect(row(page, USER)).toContainText("Muddat: 2031-06-30");
  await expect(row(page, USER)).toContainText("5.0 MB");
  await expect(row(page, USER)).toContainText("sinov muddati");

  // a wrong quota is refused inside the dialog
  await row(page, USER).getByRole("button", { name: "Sozlamalar" }).click();
  await page.getByRole("dialog").locator('input[type="number"]').fill("-4");
  await page.getByRole("dialog").getByRole("button", { name: "Saqlash" }).click();
  await expect(page.getByRole("dialog")).toBeVisible(); // still open: nothing was saved
  await page.getByRole("dialog").getByRole("button", { name: "Bekor qilish" }).click();
  await expect(row(page, USER)).toContainText("5.0 MB");
});

test("a user group can be created and given members", async ({ page }) => {
  await page.goto("/users");
  await page.getByPlaceholder("guruh nomi, masalan: xodimlar").fill(TEAM);
  await page.getByRole("button", { name: "Yaratish" }).click();
  const group = page.locator("li", { hasText: TEAM }).last();
  await expect(group).toContainText("a'zosi yo'q");

  await group.getByRole("button", { name: "A'zolar" }).click();
  await page.getByRole("dialog").locator("label", { hasText: USER }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Saqlash" }).click();
  await expect(page.locator("li", { hasText: TEAM }).last()).toContainText(USER);
  await expect(row(page, USER)).toContainText(TEAM); // the account row shows its group
});

test("the accounts can be exported to CSV without any password", async ({ page }) => {
  await page.goto("/users");
  const [download] = await Promise.all([page.waitForEvent("download"), page.getByRole("button", { name: "CSV" }).click()]);
  const text = fs.readFileSync((await download.path())!, "utf8");
  expect(download.suggestedFilename()).toMatch(/^proxy-users-\d{4}-\d{2}-\d{2}\.csv$/);
  expect(text.split("\n")[0]).toBe("username,status,disabled,expires,daily_quota_mb,groups,note");
  expect(text).toContain(USER);
  expect(text).toContain(TEAM);
  expect(text).not.toContain("e2epass1234");
  expect(text).not.toContain("$apr1$");
});

test("a CSV file is checked first and only then imported", async ({ page }) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "e2e-csv-"));
  const good = path.join(dir, "good.csv");
  const bad = path.join(dir, "bad.csv");
  fs.writeFileSync(good, `username,password,note\n${CSV_USER},csvpass12345,from a file\n`);
  fs.writeFileSync(bad, "username,password\nab,x\n");

  await page.goto("/users");

  // A file with a wrong row: shown, and the import button stays off
  await page.locator('input[type="file"]').setInputFiles(bad);
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("xato bor");
  await expect(dialog.getByRole("button", { name: "Import qilish" })).toBeDisabled();
  await dialog.getByRole("button", { name: "Yopish" }).last().click();
  await expect(row(page, "ab").filter({ hasText: /^ab/ })).toHaveCount(0);

  // A good file: dry run first (nothing changed yet), then apply
  await page.locator('input[type="file"]').setInputFiles(good);
  await expect(page.getByRole("dialog")).toContainText("1 ta yangi hisob");
  await expect(row(page, CSV_USER)).toHaveCount(0); // not created by the preview
  await page.getByRole("dialog").getByRole("button", { name: "Import qilish" }).click();
  await seeToast(page, /Import qilindi/);
  await expect(row(page, CSV_USER)).toContainText("from a file");
});

test("accounts and the group are deleted after a confirmation", async ({ page }) => {
  await page.goto("/users");
  for (const u of [CSV_USER, USER]) {
    await row(page, u).getByRole("button", { name: "O'chirish" }).click();
    await confirmDialog(page, "O'chirish");
    await expect(page.locator("li", { hasText: u }).filter({ hasText: /^\s*e2e_/ })).toHaveCount(0);
  }
  const group = page.locator("li", { hasText: TEAM }).last();
  await group.getByRole("button", { name: /guruhini o'chirish/ }).click();
  await confirmDialog(page, "O'chirish");
  await expect(page.locator("li", { hasText: TEAM })).toHaveCount(0);
  expect(await conf(page)).not.toContain(`proxy_auth ${USER}`);
});
