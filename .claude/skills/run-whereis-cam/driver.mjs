// Headless-browser driver for whereis.cam. Start the server first with serve.sh.
//
//   node driver.mjs shot <path> [--vp desktop|phone|landscape|se] [--login] [--full] [--out name]
//   node driver.mjs sweep
//
// Screenshots land in $SHOTS_DIR (default ${TMPDIR:-/tmp}/whereis-run/shots).
// `sweep` clicks every public-page and dashboard control at desktop + phone sizes
// and exits non-zero if anything is unclickable, off-screen, overflowing or throws.
import { chromium } from "playwright";
import fs from "fs";
import os from "os";
import path from "path";

const BASE = process.env.BASE_URL || `http://localhost:${process.env.APP_PORT || 8099}`;
const PASSWORD = process.env.ADMIN_PASSWORD || "testpass";
const SHOTS = process.env.SHOTS_DIR || path.join(process.env.TMPDIR || os.tmpdir(), "whereis-run", "shots");
fs.mkdirSync(SHOTS, { recursive: true });

const VIEWPORTS = {
  desktop: { width: 1280, height: 800 },
  phone: { width: 390, height: 844 },
  landscape: { width: 844, height: 390 },
  se: { width: 320, height: 568 },
};

// Prefer installed Google Chrome; fall back to Playwright's bundled Chromium.
async function launch() {
  const channel = process.env.PW_CHANNEL || "chrome";
  try {
    return await chromium.launch({ channel });
  } catch (e) {
    console.error(`(channel "${channel}" unavailable, using bundled chromium)`);
    return chromium.launch();
  }
}

const findings = [];
function finding(msg) { findings.push(msg); console.log(`  !! ${msg}`); }

function watch(page, tag) {
  page.on("pageerror", (e) => finding(`[${tag}] page error: ${e.message}`));
  page.on("console", (m) => {
    // Headless Chrome can't do Web Push; that error is expected noise.
    if (m.type() === "error" && !/Push API|push|Registration failed|status of 401/i.test(m.text())) finding(`[${tag}] console.error: ${m.text()}`);
  });
  page.on("response", (r) => { if (r.status() >= 500) finding(`[${tag}] HTTP ${r.status()} ${r.url()}`); });
  page.on("dialog", (d) => d.accept());
}

async function login(page) {
  await page.goto(BASE + "/dashboard", { waitUntil: "networkidle" });
  if (!(await page.$("#password"))) return;
  await page.fill("#password", PASSWORD);
  await page.press("#password", "Enter");
  await page.waitForLoadState("networkidle");
  if (await page.$("#password")) throw new Error("login failed (wrong ADMIN_PASSWORD?)");
}

async function rect(page, sel) {
  return page.$eval(sel, (el) => {
    const r = el.getBoundingClientRect();
    const cs = getComputedStyle(el);
    return { display: cs.display, top: Math.round(r.top), bottom: Math.round(r.bottom), left: Math.round(r.left), right: Math.round(r.right), vw: innerWidth, vh: innerHeight };
  });
}

async function overflowCheck(page, tag) {
  const over = await page.evaluate(() => document.documentElement.scrollWidth - innerWidth);
  if (over > 0) finding(`[${tag}] page scrolls horizontally by ${over}px`);
}

async function shot(args) {
  const target = args[0] || "/";
  const vp = args.includes("--vp") ? args[args.indexOf("--vp") + 1] : "desktop";
  const out = args.includes("--out") ? args[args.indexOf("--out") + 1] : `${vp}_${target.replace(/[^a-z0-9]+/gi, "_").replace(/^_+|_+$/g, "") || "root"}`;
  const browser = await launch();
  const page = await (await browser.newContext({ viewport: VIEWPORTS[vp] })).newPage();
  watch(page, out);
  if (args.includes("--login")) await login(page);
  await page.goto(BASE + target, { waitUntil: "networkidle" });
  const file = path.join(SHOTS, `${out}.png`);
  await page.screenshot({ path: file, fullPage: args.includes("--full") });
  console.log(file);
  await browser.close();
}

async function sweepIndex(browser, vp, query) {
  const tag = `index${query}/${vp}`;
  console.log(`== ${tag}`);
  const ctx = await browser.newContext({ viewport: VIEWPORTS[vp], permissions: ["clipboard-read", "clipboard-write"] });
  const page = await ctx.newPage();
  watch(page, tag);
  await page.goto(BASE + "/" + query, { waitUntil: "networkidle" });
  await overflowCheck(page, tag);

  const n = await page.locator(".btn-hero-address, .pub-chip").count();
  for (let i = 0; i < n; i++) {
    const trigger = page.locator(".btn-hero-address, .pub-chip").nth(i);
    const label = (await trigger.innerText()).trim().split("\n")[0];
    try { await trigger.click({ timeout: 3000 }); } catch { finding(`[${tag}] "${label}" not clickable`); continue; }
    await page.waitForTimeout(350);
    const s = await rect(page, ".address-sheet");
    if (s.top < 0 || s.left < 0 || s.right > s.vw) finding(`[${tag}] sheet for "${label}" off-screen ${JSON.stringify(s)}`);
    if (i === 0) await page.screenshot({ path: path.join(SHOTS, `sweep_${vp}${query ? "_pub" : ""}_sheet.png`) });
    const closeSel = i % 2 ? null : ".btn-sheet-close";
    if (closeSel) await page.click(closeSel, { timeout: 3000 }).catch(() => finding(`[${tag}] Cancel not clickable`));
    else await page.mouse.click(5, 5);
    await page.waitForTimeout(350);
    if ((await rect(page, "#address-modal")).display !== "none") finding(`[${tag}] sheet did not close (${closeSel ? "cancel" : "backdrop"})`);
  }
  console.log(`  address sheet: ${n} triggers checked`);

  // The "Sweep Taps" button only renders in the not-at-pub state.
  if (await page.$("#sweep-btn")) await page.click("#sweep-btn").catch(() => finding(`[${tag}] sweep button not clickable`));
  await page.locator("#subscribe-email").scrollIntoViewIfNeeded();
  await page.fill("#subscribe-email", `sweep-${vp}-${Date.now()}@example.com`);
  await page.click("#subscribe-btn").catch(() => finding(`[${tag}] subscribe not clickable`));
  await page.waitForTimeout(1000);
  if ((await rect(page, "#subscribe-success")).display === "none") finding(`[${tag}] subscribe success card not shown`);
  else console.log("  subscribe: ok");
  await page.screenshot({ path: path.join(SHOTS, `sweep_${vp}${query ? "_pub" : ""}_page.png`), fullPage: true });
  await ctx.close();
}

async function sweepDashboard(browser, vp) {
  const tag = `dashboard/${vp}`;
  console.log(`== ${tag}`);
  const ctx = await browser.newContext({ viewport: VIEWPORTS[vp] });
  const page = await ctx.newPage();
  watch(page, tag);
  await login(page);
  await overflowCheck(page, tag);

  const toast = async (what) => {
    await page.waitForTimeout(300);
    const t = await rect(page, "#toast");
    const text = (await page.innerText("#toast")).trim();
    if (t.display === "none") finding(`[${tag}] no toast after ${what}`);
    else if (t.left < 0 || t.right > t.vw || t.bottom > t.vh) finding(`[${tag}] toast off-screen after ${what}`);
    else console.log(`  ${what}: "${text}"`);
    await page.waitForLoadState("networkidle");
    await page.waitForTimeout(1000); // most actions reload the page after 800ms
  };

  for (const mode of ["secret_pint_stealth", "secret_pint_cheeky"]) {
    await page.locator(`button[onclick="setSecretPint('${mode}')"]`).click().catch(() => finding(`[${tag}] ${mode} not clickable`));
    await toast(mode);
  }
  await page.locator('button[onclick="clearOverride()"]').first().click().catch(() => finding(`[${tag}] clear override not clickable`));
  await toast("clear override");

  await page.selectOption("#pubSelect", { index: 1 });
  await page.click('#manualPubForm button[type="submit"]').catch(() => finding(`[${tag}] manual pub submit not clickable`));
  await toast("manual pub");
  const st = await (await fetch(BASE + "/api/status")).json();
  if (!st.at_pub) finding(`[${tag}] manual pub override did not show Cam at the pub`);

  await page.fill("#testEmailInput", "cam@example.com");
  await page.click('form[onsubmit="sendTestEmail(event)"] button').catch(() => finding(`[${tag}] test email not clickable`));
  await toast("test email");

  const buttons = await page.$$eval(".subs-table button", (bs) => bs.map((b) => Math.round(b.getBoundingClientRect().right)));
  const offscreen = buttons.filter((r) => r > VIEWPORTS[vp].width);
  if (offscreen.length) finding(`[${tag}] ${offscreen.length} Deactivate buttons off-screen`);
  if (buttons.length) {
    await page.locator(".subs-table button").first().click().catch(() => finding(`[${tag}] Deactivate not clickable`));
    await toast("deactivate");
  }
  await page.screenshot({ path: path.join(SHOTS, `sweep_${vp}_dashboard.png`), fullPage: true });

  await page.locator('button[onclick="clearOverride()"]').first().click().catch(() => {});
  await page.waitForTimeout(1200);
  await ctx.close();
}

async function sweep() {
  const browser = await launch();
  for (const vp of ["desktop", "phone"]) {
    await sweepIndex(browser, vp, "");
    await sweepIndex(browser, vp, "?mock=pub");
    await sweepDashboard(browser, vp);
  }
  for (const vp of ["landscape", "se"]) {
    await sweepIndex(browser, vp, "?mock=pub");
  }
  await browser.close();
  console.log(`\nscreenshots: ${SHOTS}`);
  console.log(findings.length ? `${findings.length} finding(s):\n- ${findings.join("\n- ")}` : "no findings");
  process.exit(findings.length ? 1 : 0);
}

const [cmd, ...rest] = process.argv.slice(2);
if (cmd === "shot") await shot(rest);
else if (cmd === "sweep") await sweep();
else { console.error("usage: node driver.mjs shot <path> [--vp desktop|phone|landscape|se] [--login] [--full] [--out name] | sweep"); process.exit(2); }
