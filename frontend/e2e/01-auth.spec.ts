import * as fs from "node:fs";
import * as path from "node:path";
import { AUTH_FILE, NEW_PW, ONE_TIME, expect, seeToast, signIn, test } from "./helpers";

test.describe.configure({ mode: "serial" });

test("a wrong password is refused with a clear message", async ({ page }) => {
  await signIn(page, "admin", "definitely-wrong-password");
  await expect(page.getByText("Login yoki parol noto'g'ri")).toBeVisible();
  await expect(page).toHaveURL(/\/login$/);
});

test("an unknown address goes to the sign-in page when signed out", async ({ page }) => {
  await page.goto("/there/is/nothing/here");
  await expect(page).toHaveURL(/\/login$/);
});

test("the first-start password forces a new one before anything else opens", async ({ page }) => {
  await signIn(page, "admin", ONE_TIME);
  await expect(page).toHaveURL(/change-password/);
  await expect(page.getByRole("heading", { name: "Yangi parol o'rnating" })).toBeVisible();

  // The rest of the panel is locked until the password is changed.
  await page.goto("/users");
  await expect(page).toHaveURL(/change-password/);

  const pw = page.locator('input[type="password"]');
  const submit = page.getByRole("button", { name: "Parolni yangilash" });

  await pw.nth(0).fill(ONE_TIME);
  await pw.nth(1).fill("Different-Pass-1234");
  await pw.nth(2).fill("Another-Pass-5678");
  await submit.click();
  await seeToast(page, "Yangi parollar bir xil emas");

  await pw.nth(1).fill("short");
  await pw.nth(2).fill("short");
  await submit.click();
  await seeToast(page, /kamida 8 belgi/);

  await pw.nth(1).fill("admin123");
  await pw.nth(2).fill("admin123");
  await submit.click();
  await expect(page).toHaveURL(/change-password/); // the server refuses a common password

  await pw.nth(1).fill(NEW_PW);
  await pw.nth(2).fill(NEW_PW);
  await submit.click();
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();

  fs.mkdirSync(path.dirname(AUTH_FILE), { recursive: true });
  await page.context().storageState({ path: AUTH_FILE });
});

test.describe("signed in", () => {
  test.use({ storageState: AUTH_FILE });

  test("the session survives a page reload and shows who is signed in", async ({ page }) => {
    await page.goto("/");
    await page.reload();
    await expect(page.getByText("Administrator")).toBeVisible();
    await expect(page.getByText("Squid ishlamoqda")).toBeVisible();
  });

  test("an unknown address goes to the dashboard when signed in", async ({ page }) => {
    await page.goto("/there/is/nothing/here");
    await expect(page).toHaveURL(/\/$/);
  });

  test("signing out ends the session for good", async ({ page }) => {
    // A second session, so the saved one stays valid for the later tests.
    await signIn(page, "admin", NEW_PW);
    await expect(page).toHaveURL(/\/$/);
    await page.getByRole("button", { name: "Chiqish" }).click();
    await expect(page).toHaveURL(/\/login$/);
    await page.goto("/users");
    await expect(page).toHaveURL(/\/login$/);
  });
});
