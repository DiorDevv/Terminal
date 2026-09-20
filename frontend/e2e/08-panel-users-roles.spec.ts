import { AUTH_FILE, api, confirmDialog, expect, seeToast, signIn, test } from "./helpers";

test.use({ storageState: AUTH_FILE });
test.describe.configure({ mode: "serial" });

const VIEWER = "e2e_viewer";
const OPERATOR = "e2e_operator";
const TEMP_PW = "Temporary-Pass-2026";
const OWN_PW = "Own-Chosen-Pass-2026";

const row = (page: import("@playwright/test").Page, name: string) => page.locator("li", { hasText: name }).first();

test("an admin creates a viewer and an operator", async ({ page }) => {
  // start clean
  for (const u of (await api(page, "GET", "/panel/users")).json.users ?? []) {
    if (u.username === VIEWER || u.username === OPERATOR) await api(page, "DELETE", `/panel/users/${u.id}`);
  }
  await page.goto("/panel-users");

  for (const [name, roleLabel] of [[VIEWER, "Kuzatuvchi"], [OPERATOR, "Operator"]]) {
    await page.getByPlaceholder("masalan: jamshid").fill(name);
    await page.getByPlaceholder("kamida 8 belgi").fill(TEMP_PW);
    await page.locator("select").first().selectOption({ label: roleLabel });
    await page.getByRole("button", { name: /Qo'shish|Yaratish/ }).click();
    await expect(row(page, name)).toBeVisible();
  }
  await expect(row(page, VIEWER)).toContainText("Kuzatuvchi");
  await expect(row(page, OPERATOR)).toContainText("Operator");
});

test("a weak temporary password is refused", async ({ page }) => {
  await page.goto("/panel-users");
  await page.getByPlaceholder("masalan: jamshid").fill("e2e_weak");
  await page.getByPlaceholder("kamida 8 belgi").fill("short");
  await page.getByRole("button", { name: /Qo'shish|Yaratish/ }).click();
  await expect(row(page, "e2e_weak")).toHaveCount(0);
});

test("the viewer must choose a password, then sees a reduced, read-only panel", async ({ browser }) => {
  const context = await browser.newContext({ storageState: undefined });
  const page = await context.newPage();
  await signIn(page, VIEWER, TEMP_PW);
  await expect(page).toHaveURL(/change-password/);
  const pw = page.locator('input[type="password"]');
  await pw.nth(0).fill(TEMP_PW);
  await pw.nth(1).fill(OWN_PW);
  await pw.nth(2).fill(OWN_PW);
  await page.getByRole("button", { name: "Parolni yangilash" }).click();
  await expect(page).toHaveURL(/\/$/);

  const nav = page.locator("nav");
  await expect(page.getByText("Kuzatuvchi", { exact: true })).toBeVisible();
  for (const shown of ["Dashboard", "Bloklangan domenlar", "Kirish qoidalari", "Proxy foydalanuvchilari", "Sozlamalar"]) {
    await expect(nav.getByRole("link", { name: shown, exact: true })).toBeVisible();
  }
  for (const hidden of ["Statistika", "Loglar", "Cheklovlar", "Squid sozlamalari", "Konfiguratsiya", "Config tarixi", "Panel foydalanuvchilari", "Audit log", "Ogohlantirishlar"]) {
    await expect(nav.getByRole("link", { name: hidden, exact: true })).toHaveCount(0);
  }

  // pages above the role bounce back to the dashboard
  for (const forbidden of ["/config", "/audit", "/panel-users", "/stats", "/limits", "/alerts"]) {
    await page.goto(forbidden);
    await expect(page).toHaveURL(/\/$/);
  }

  // a page it may see is read-only: a notice, and the buttons are switched off
  await page.goto("/blacklist");
  await expect(page.getByText("Faqat o'qish rejimi")).toBeVisible();
  await expect(page.getByRole("button", { name: "Qo'shish" })).toBeDisabled();
  await expect(page.getByPlaceholder("masalan: example.com")).toBeDisabled();

  // ...and the API refuses even if the page were bypassed
  const blocked = await api(page, "POST", "/squid/blacklist", { domain: "viewer-should-not.example" });
  expect(blocked.status).toBe(403);
  const cfg = await api(page, "GET", "/squid/config");
  expect(cfg.status).toBe(403);
  await context.close();
});

test("the operator can manage traffic but not the server or accounts", async ({ browser }) => {
  const context = await browser.newContext({ storageState: undefined });
  const page = await context.newPage();
  await signIn(page, OPERATOR, TEMP_PW);
  const pw = page.locator('input[type="password"]');
  await pw.nth(0).fill(TEMP_PW);
  await pw.nth(1).fill(OWN_PW);
  await pw.nth(2).fill(OWN_PW);
  await page.getByRole("button", { name: "Parolni yangilash" }).click();
  await expect(page).toHaveURL(/\/$/);

  const nav = page.locator("nav");
  for (const shown of ["Statistika", "Loglar", "Cheklovlar", "Squid sozlamalari", "Proxy foydalanuvchilari"]) {
    await expect(nav.getByRole("link", { name: shown, exact: true })).toBeVisible();
  }
  for (const hidden of ["Konfiguratsiya", "Config tarixi", "Panel foydalanuvchilari", "Audit log", "Ogohlantirishlar"]) {
    await expect(nav.getByRole("link", { name: hidden, exact: true })).toHaveCount(0);
  }
  for (const forbidden of ["/config", "/audit", "/panel-users", "/alerts", "/history"]) {
    await page.goto(forbidden);
    await expect(page).toHaveURL(/\/$/);
  }

  // an operator's everyday work goes through
  await page.goto("/blacklist");
  await page.getByPlaceholder("masalan: example.com").fill("e2e-operator-added.example");
  await page.getByRole("button", { name: "Qo'shish" }).click();
  await expect(row(page, "e2e-operator-added.example")).toBeVisible();
  await row(page, "e2e-operator-added.example").getByRole("button").click();
  await confirmDialog(page, "O'chirish");
  await expect(row(page, "e2e-operator-added.example")).toHaveCount(0);

  // ...but the raw configuration and the audit log are admin-only in the API too
  expect((await api(page, "GET", "/squid/config")).status).toBe(403);
  expect((await api(page, "GET", "/audit")).status).toBe(403);
  expect((await api(page, "POST", "/squid/service/stop")).status).toBe(403);
  await context.close();
});

test("an admin can reset a password, change a role, switch an account off and delete it", async ({ page }) => {
  await page.goto("/panel-users");

  // reset password: the dialog will not accept a short one
  await row(page, VIEWER).getByRole("button", { name: "Parolni tiklash" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.locator('input[type="password"]').fill("short");
  await expect(dialog.getByRole("button", { name: "O'rnatish" })).toBeDisabled();
  await dialog.locator('input[type="password"]').fill("Reset-By-Admin-2026");
  await dialog.getByRole("button", { name: "O'rnatish" }).click();
  await seeToast(page, /vaqtinchalik parol o'rnatildi/);

  // role
  await row(page, VIEWER).locator("select").selectOption({ label: "Operator" });
  await seeToast(page, /roli "Operator"/);
  await expect(row(page, VIEWER)).toContainText("Operator");

  // switch off (account keeps existing, but cannot sign in)
  await row(page, VIEWER).getByRole("button", { name: /o'chirib qo'yish|to'xtatish|bloklash/i }).click();
  await expect(row(page, VIEWER)).toContainText(/o'chirilgan|bloklangan|nofaol/i);

  // delete both after a confirmation
  for (const name of [VIEWER, OPERATOR]) {
    await row(page, name).getByRole("button", { name: /O'chirish/ }).last().click();
    await confirmDialog(page, "O'chirish");
    await expect(row(page, name)).toHaveCount(0);
  }
});
