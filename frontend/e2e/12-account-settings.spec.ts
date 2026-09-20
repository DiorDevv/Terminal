import { AUTH_FILE, api, expect, seeToast, signIn, test } from "./helpers";

test.use({ storageState: AUTH_FILE });
test.describe.configure({ mode: "serial" });

// A throwaway account: the admin's own password is never touched by these tests.
const USER = "e2e_self";
const FIRST = "First-Given-Pass-1";
const CHOSEN = "Chosen-By-Me-Pass-2";
const CHANGED = "Changed-Later-Pass-3";

const passwordFields = (page: import("@playwright/test").Page) => page.locator('input[type="password"]');

test("a new account is forced to pick its own password first", async ({ page, browser }) => {
  for (const u of (await api(page, "GET", "/panel/users")).json.users ?? []) {
    if (u.username === USER) await api(page, "DELETE", `/panel/users/${u.id}`);
  }
  expect((await api(page, "POST", "/panel/users", { username: USER, password: FIRST, role: "operator" })).status).toBe(200);

  const ctx = await browser.newContext({ storageState: undefined });
  const p = await ctx.newPage();
  await signIn(p, USER, FIRST);
  await expect(p).toHaveURL(/change-password/);
  await p.goto("/"); // cannot walk around it
  await expect(p).toHaveURL(/change-password/);

  await passwordFields(p).nth(0).fill(FIRST);
  await passwordFields(p).nth(1).fill(FIRST); // the same as the old one
  await passwordFields(p).nth(2).fill(FIRST);
  await p.getByRole("button", { name: "Parolni yangilash" }).click();
  await expect(p).toHaveURL(/change-password/);

  await passwordFields(p).nth(1).fill(CHOSEN);
  await passwordFields(p).nth(2).fill(CHOSEN);
  await p.getByRole("button", { name: "Parolni yangilash" }).click();
  await expect(p).toHaveURL(/\/$/);
  await ctx.close();
});

test("changing the own password checks the old one and the confirmation", async ({ browser }) => {
  const ctx = await browser.newContext({ storageState: undefined });
  const p = await ctx.newPage();
  await signIn(p, USER, CHOSEN);
  await expect(p).toHaveURL(/\/$/);
  await p.goto("/settings");
  await expect(p.getByText("Parolni o'zgartirish").first()).toBeVisible();
  await expect(p.getByText("Faol sessiyalar")).toBeVisible();

  const submit = () => p.getByRole("button", { name: "Parolni yangilash" }).click();

  // confirmation differs
  await passwordFields(p).nth(0).fill(CHOSEN);
  await passwordFields(p).nth(1).fill(CHANGED);
  await passwordFields(p).nth(2).fill(CHANGED + "x");
  await submit();
  await seeToast(p, "Yangi parollar bir xil emas");

  // too short
  await passwordFields(p).nth(1).fill("short");
  await passwordFields(p).nth(2).fill("short");
  await submit();
  await seeToast(p, /kamida \d+ belgidan/);

  // wrong current password
  await passwordFields(p).nth(0).fill("Not-My-Password-9");
  await passwordFields(p).nth(1).fill(CHANGED);
  await passwordFields(p).nth(2).fill(CHANGED);
  await submit();
  await expect(p.getByText("Parol muvaffaqiyatli o'zgartirildi")).toHaveCount(0);

  // the right one
  await passwordFields(p).nth(0).fill(CHOSEN);
  await submit();
  await seeToast(p, "Parol muvaffaqiyatli o'zgartirildi");
  await ctx.close();

  // the old password no longer opens the door, the new one does
  const ctx2 = await browser.newContext({ storageState: undefined });
  const q = await ctx2.newPage();
  await signIn(q, USER, CHOSEN);
  await expect(q).toHaveURL(/login/);
  await signIn(q, USER, CHANGED);
  await expect(q).toHaveURL(/\/$/);
  await ctx2.close();
});

test("active sessions are listed and another device can be signed out remotely", async ({ browser }) => {
  const a = await browser.newContext({ storageState: undefined });
  const b = await browser.newContext({ storageState: undefined });
  const pa = await a.newPage();
  const pb = await b.newPage();
  await signIn(pa, USER, CHANGED);
  await signIn(pb, USER, CHANGED);
  await expect(pa).toHaveURL(/\/$/);
  await expect(pb).toHaveURL(/\/$/);

  await pa.goto("/settings");
  await expect(pa.getByText("shu qurilma")).toHaveCount(1);
  const revokeButtons = pa.getByRole("button", { name: "Tugatish" });
  await expect(revokeButtons.first()).toBeVisible();

  await revokeButtons.first().click();
  await seeToast(pa, "Sessiya tugatildi");

  // the other browser is really signed out: its next request is refused
  await pb.reload();
  await expect(pb).toHaveURL(/login/);
  // ...while this one carries on
  await pa.reload();
  await expect(pa).toHaveURL(/settings/);
  await a.close();
  await b.close();
});

test("clean up the throwaway account", async ({ page }) => {
  for (const u of (await api(page, "GET", "/panel/users")).json.users ?? []) {
    if (u.username === USER) expect((await api(page, "DELETE", `/panel/users/${u.id}`)).status).toBe(200);
  }
});
