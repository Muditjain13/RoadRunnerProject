// Drives the real embedded UI in headless Chromium: load the page, search for
// a bus trip, pick a seat, sign in, book, and check the confirmation. Fails on
// any HTTP error from the /v1 API or any JavaScript error on the page.
//
//   BASE_URL=http://localhost:8080 node ui.mjs
import { chromium } from "playwright";

const BASE = process.env.BASE_URL || "http://localhost:8080";
const TIMEOUT = 30_000;

const problems = [];
const apiCalls = [];

function step(msg) {
  console.log(`ui-e2e: ${msg}`);
}

const browser = await chromium.launch();
try {
  const page = await browser.newPage();
  page.setDefaultTimeout(TIMEOUT);

  page.on("response", (res) => {
    const url = new URL(res.url());
    if (url.origin !== new URL(BASE).origin) {
      problems.push(`request left the gateway origin: ${res.url()}`);
    }
    if (url.pathname.startsWith("/v1/")) {
      apiCalls.push(`${res.request().method()} ${url.pathname} -> ${res.status()}`);
      if (res.status() >= 400) {
        problems.push(`API error: ${res.request().method()} ${url.pathname} -> ${res.status()}`);
      }
    }
  });
  page.on("pageerror", (err) => problems.push(`page error: ${err.message}`));
  page.on("console", (msg) => {
    if (msg.type() === "error") problems.push(`console error: ${msg.text()}`);
  });

  await page.goto(BASE);
  const title = await page.title();
  if (!title.includes("RoadRunner")) problems.push(`unexpected page title: ${title}`);
  step(`page served, title "${title}"`);

  await page.selectOption("#origin", "PNQ");
  await page.selectOption("#dest", "BOM");
  await page.click('.mode-opt[data-mode="bus"]');
  await page.click('#searchForm button[type="submit"]');
  await page.waitForSelector(".trip-card");
  step("search returned trip cards");

  const chip = page.locator(".class-chip", { hasText: /left/ }).first();
  await chip.waitFor();
  await chip.click();
  await page.waitForSelector(".seat-chip");
  step("seat picker opened with live seat availability");

  await page.locator(".seat-chip").first().click();
  await page.click("#confirmBookBtn");

  await page.waitForSelector("#signInName", { state: "visible" });
  await page.fill("#signInName", "CI Bot");
  await page.click("#signInBtn");

  await page.waitForSelector(".booking-result h3");
  const heading = (await page.textContent(".booking-result h3")).trim();
  if (heading !== "Booking confirmed!") {
    problems.push(`booking result heading was "${heading}", want "Booking confirmed!"`);
  } else {
    step("booking confirmed in the UI");
  }
} catch (err) {
  problems.push(`test aborted: ${err.message}`);
} finally {
  await browser.close();
}

console.log("API calls the UI made:");
for (const c of apiCalls) console.log(`  ${c}`);

if (problems.length > 0) {
  console.error("\nui-e2e FAILED:");
  for (const p of problems) console.error(`  - ${p}`);
  process.exit(1);
}
console.log("\nui-e2e PASSED");
