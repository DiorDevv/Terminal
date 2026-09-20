import { expect, test as base, type Page } from "@playwright/test";
import * as path from "node:path";

// The first-start admin password (see `journalctl -u squidadmin`). Only 01-auth needs it.
export const ONE_TIME = process.env.E2E_ADMIN_PW ?? "";
export const NEW_PW = "Ui-E2E-Pass-7331x";
// Playwright runs from the frontend folder (where playwright.config.ts is).
export const AUTH_FILE = path.resolve(process.cwd(), "e2e/.auth/admin.json");

/**
 * Every test also watches the browser: an uncaught script error, a console error
 * or a 5xx answer fails it, however well the visible part looked. Expected 4xx
 * answers (wrong password, validation, 401 before signing in) are not problems.
 */
export const test = base.extend({
  page: async ({ page }, provide) => {
    const problems: string[] = [];
    page.on("pageerror", (e) => problems.push(`script error: ${e}`));
    page.on("console", (m) => {
      if (m.type() === "error" && !/status of 4\d\d/.test(m.text())) problems.push(`console error: ${m.text().slice(0, 200)}`);
    });
    page.on("response", (r) => {
      if (r.status() >= 500) problems.push(`HTTP ${r.status()} ${r.request().method()} ${r.url()}`);
    });
    await provide(page);
    expect(problems, "problems seen in the browser").toEqual([]);
  },
});
export { expect };

/** Calls the panel's API with the browser's own session (checks what the UI did). */
export async function api(page: Page, method: string, url: string, body?: unknown) {
  const res = await page.request.fetch("/api" + url, {
    method,
    headers: { "X-Requested-With": "squidadmin", "Content-Type": "application/json" },
    data: body === undefined ? undefined : JSON.stringify(body),
  });
  let json: any = {};
  try {
    json = await res.json();
  } catch {
    /* not JSON */
  }
  return { status: res.status(), json, text: async () => res.text() };
}

export async function signIn(page: Page, user: string, password: string) {
  await page.goto("/login");
  await page.locator('input:not([type="password"])').first().fill(user);
  await page.locator('input[type="password"]').first().fill(password);
  await page.getByRole("button", { name: "Kirish" }).click();
}

/** A toast (the small message in the corner) with this text is on screen. */
export async function seeToast(page: Page, text: string | RegExp) {
  await expect(page.getByText(text).first()).toBeVisible();
}

/** Confirms only if the panel actually asked (some deletions ask, some do not). */
export async function confirmIfAsked(page: Page, label: string) {
  const overlay = page.locator(".fixed.inset-0").first();
  if (await overlay.isVisible({ timeout: 1500 }).catch(() => false)) await confirmDialog(page, label);
}

/** Clicks the confirm button of the panel's own confirmation dialog. */
export async function confirmDialog(page: Page, label: string) {
  await page.getByRole("dialog").or(page.locator(".fixed.inset-0")).getByRole("button", { name: label }).last().click();
}
