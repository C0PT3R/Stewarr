import { createServer } from "node:http";
import { readFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const require = createRequire(import.meta.url);
const modules = process.env.PLAYWRIGHT_NODE_MODULES || process.env.CODEX_PRIMARY_RUNTIME_NODE_MODULES;
if (!modules) throw new Error("Set PLAYWRIGHT_NODE_MODULES to a node_modules directory containing playwright.");
const { chromium } = require(join(modules, "playwright"));

const repository = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
const staticRoot = join(repository, "internal/httpui/static");
let planRequests = 0;
let executeRequests = 0;

const model = Buffer.from(JSON.stringify({
  kind: "media",
  mediaType: "movie",
  dryRun: false,
  torrentStatuses: { abc: "CURRENT" },
  files: [
    { path: "/library/movie.mkv", owner: "media", ownerKey: "radarr:9", actionName: "managed_file", actionValue: "radarr:9", physicalKey: "1:2", selected: true, exists: true, sizeBytes: 4096, identityKnown: true, device: 1, inode: 2, links: 2 },
    { path: "/downloads/movie.mkv", owner: "torrent", ownerKey: "abc", actionName: "torrent", actionValue: "abc", physicalKey: "1:2", selected: false, exists: true, sizeBytes: 4096, identityKnown: true, device: 1, inode: 2, links: 2 }
  ]
})).toString("base64");

const removal = `<div class="removal-overlay" data-controller="removal" data-removal-model-value="${model}" data-action="click->removal#backdrop">
<main class="removal-dialog" role="dialog" aria-modal="true" tabindex="-1"><div class="top"><div><h2 id="removal-title">Remove media</h2><div class="muted">Movie</div></div></div>
<div data-removal-consequences><div class="summary"><div><div class="muted">Storage reclaimed</div><div class="big" data-removal-target="reclaimed">0 B</div><div data-removal-target="selectedSummary"></div></div></div><div data-removal-target="guidance" hidden><strong data-removal-target="guidanceTitle"></strong><p data-removal-target="guidanceAction"></p></div><div data-removal-target="warnings"></div></div>
<form data-removal-target="selection" data-action="change->removal#selectionChanged"><input id="media-pick" data-managed-pick data-physical-pick data-physical-key="1:2" type="checkbox" checked><input id="torrent-pick" data-linked-pick type="checkbox" name="torrent" value="abc"></form>
<form id="removal-execute-form" data-removal-target="execute" data-action="submit->removal#submit" method="post" action="/removal/execute"><input type="hidden" name="kind" value="media"><input type="hidden" name="media_type" value="movie"><input type="hidden" name="media_id" value="1"><input type="hidden" name="operation_token" value="browser-token"><div data-removal-target="generatedInputs"></div><p data-removal-target="error" hidden></p></form><div class="actions"><button id="cancel" type="button" data-action="removal#cancel">Cancel</button><button id="confirm" form="removal-execute-form" data-removal-target="submitButton" type="submit">Remove selected</button></div>
</main></div>`;

const index = `<!doctype html><html><head><script defer src="/assets/vendor/htmx-2.0.10.min.js"></script><script defer src="/assets/vendor/stimulus-3.2.2.umd.js"></script><script defer src="/assets/app.js"></script></head><body data-controller="shell"><button id="open" data-removal-url="/removal/media">Remove</button><div id="removal-modal"></div><div id="ui-announcer"></div><div id="updates-available" hidden></div></body></html>`;

const server = createServer(async (request, response) => {
  if (request.url === "/") {
    response.setHeader("Content-Type", "text/html");
    response.end(index);
    return;
  }
  if (request.url === "/removal/media") {
    planRequests += 1;
    response.setHeader("Content-Type", "text/html");
    response.end(removal);
    return;
  }
  if (request.url === "/removal/execute" && request.method === "POST") {
    executeRequests += 1;
    request.resume();
    await new Promise(resolveDelay => setTimeout(resolveDelay, 30));
    response.writeHead(202, { "Content-Type": "application/json" });
    response.end(JSON.stringify({ operationId: 7, status: "queued" }));
    return;
  }
  if (request.url?.startsWith("/assets/")) {
    try {
      const file = await readFile(join(staticRoot, request.url.slice("/assets/".length)));
      response.setHeader("Content-Type", request.url.endsWith(".js") ? "text/javascript" : "text/css");
      response.end(file);
    } catch (_) {
      response.writeHead(404).end();
    }
    return;
  }
  response.writeHead(404).end();
});

await new Promise(resolveListen => server.listen(0, "127.0.0.1", resolveListen));
const address = server.address();
if (process.env.CONNARR_FIXTURE_ONLY === "1") {
  console.log(`http://127.0.0.1:${address.port}/`);
  await new Promise(resolveStop => {
    process.once("SIGINT", resolveStop);
    process.once("SIGTERM", resolveStop);
  });
  await new Promise(resolveClose => server.close(resolveClose));
  process.exit(0);
}
const executablePath = process.env.CONNARR_BROWSER_EXECUTABLE || chromium.executablePath();
const browser = await chromium.launch({ executablePath, headless: true });
const page = await browser.newPage();

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

try {
  await page.goto(`http://127.0.0.1:${address.port}/`);
  await page.click("#open");
  await page.waitForSelector(".removal-dialog");
  const dialogIdentity = await page.evaluate(() => {
    const dialog = document.querySelector(".removal-dialog");
    dialog.dataset.identity = crypto.randomUUID();
    return dialog.dataset.identity;
  });
  await page.check("#torrent-pick");
  assert(await page.textContent('[data-removal-target="reclaimed"]') === "4.00 KiB", "selection did not calculate reclaimed bytes locally");
  assert(await page.isChecked("#media-pick"), "physical media selection did not synchronize with the torrent action");
  assert(planRequests === 1, `selection made ${planRequests} removal-plan requests`);
  assert(await page.getAttribute(".removal-dialog", "data-identity") === dialogIdentity, "selection replaced the dialog DOM");
  await page.click("#cancel");
  await page.waitForSelector(".removal-dialog", { state: "detached" });

  await page.click("#open");
  await page.waitForSelector(".removal-dialog");
  await page.check("#torrent-pick");
  await page.keyboard.press("Escape");
  await page.waitForSelector(".removal-dialog", { state: "detached" });

  await page.click("#open");
  await page.waitForSelector(".removal-dialog");
  await page.evaluate(() => {
    document.getElementById("confirm").click();
    document.getElementById("confirm").click();
  });
  await page.waitForSelector(".removal-dialog", { state: "detached" });
  assert(executeRequests === 1, `duplicate confirmation submitted ${executeRequests} operations`);
  assert(planRequests === 3, `unexpected plan request count ${planRequests}`);
  console.log("Connarr browser reactivity tests passed");
} finally {
  await browser.close();
  await new Promise(resolveClose => server.close(resolveClose));
}
