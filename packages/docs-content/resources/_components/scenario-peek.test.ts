import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import {fileURLToPath} from "node:url";
import test from "node:test";

import {
  classifyPeekException,
  classifyPeekHttpStatus,
  collectionColumns,
  formatCell,
  limitedRows,
  loadScenarioPeek,
  missingPeekSource,
  officialScenarioSource,
  parseScenarioPeek,
  peekTitle,
  readScenarioPeekPayload,
  selectCollection,
} from "./scenario-peek.ts";

const resourcesRoot = path.resolve(fileURLToPath(new URL("../../../../resources", import.meta.url)));

function officialScenarioFiles(): string[] {
  const files: string[] = [];
  for (const resource of fs.readdirSync(resourcesRoot, {withFileTypes: true})) {
    if (!resource.isDirectory()) {
      continue;
    }
    const dir = path.join(resourcesRoot, resource.name, "scenarios");
    if (!fs.existsSync(dir)) {
      continue;
    }
    for (const entry of fs.readdirSync(dir)) {
      if (entry.endsWith(".json")) {
        files.push(path.join(dir, entry));
      }
    }
  }
  return files.sort();
}

test("officialScenarioSource maps catalog ids onto the committed JSON path", () => {
  const resolved = officialScenarioSource("asana", "asana.acme-sprint.v1");
  assert.ok(resolved);
  assert.equal(resolved.path, "resources/asana/scenarios/acme-sprint.v1.json");
  assert.equal(
    resolved.blobURL,
    "https://github.com/DumbMachine/fabricate/blob/main/resources/asana/scenarios/acme-sprint.v1.json",
  );
  assert.equal(
    resolved.rawURL,
    "https://raw.githubusercontent.com/DumbMachine/fabricate/main/resources/asana/scenarios/acme-sprint.v1.json",
  );
  assert.equal(officialScenarioSource("asana", "not a file"), null);
});

test("peekTitle derives the catalog id from an official path", () => {
  const resolved = officialScenarioSource("asana", "acme-sprint.v1");
  assert.equal(peekTitle(resolved), "asana.acme-sprint.v1");
  assert.equal(peekTitle(resolved, "gmail.acme-corp.v1"), "asana.acme-sprint.v1");
  assert.equal(peekTitle(null, "asana.minimal.v1"), "asana.minimal.v1");
  assert.equal(peekTitle(null), "Scenario data");
});

test("parseScenarioPeek reads seeded collections from every official scenario", () => {
  const files = officialScenarioFiles();
  assert.ok(files.length > 1);
  for (const file of files) {
    const parsed = parseScenarioPeek(JSON.parse(fs.readFileSync(file, "utf8")));
    assert.ok(parsed, file);
    assert.ok(parsed.collections.length > 0, file);
    assert.match(file, new RegExp(`${parsed.resource}/scenarios/`));
  }
});

test("parseScenarioPeek keeps collection keys from the scenario state", () => {
  const asana = parseScenarioPeek(
    JSON.parse(fs.readFileSync(path.join(resourcesRoot, "asana/scenarios/acme-sprint.v1.json"), "utf8")),
  );
  const gmail = parseScenarioPeek(
    JSON.parse(fs.readFileSync(path.join(resourcesRoot, "gmail/scenarios/acme-corp.v1.json"), "utf8")),
  );
  assert.ok(asana);
  assert.ok(gmail);
  assert.deepEqual(
    asana.collections.map((entry) => entry.key),
    ["workspaces", "users", "projects", "sections", "tasks", "stories"],
  );
  assert.equal(selectCollection(asana.collections, "tasks")?.rows.length, 10);
  assert.deepEqual(
    gmail.collections.map((entry) => entry.key),
    ["labels", "messages"],
  );
  assert.equal(gmail.scalars.find((entry) => entry.key === "emailAddress")?.value, "support@acme.example");
});

test("table helpers cap rows, prefer identity columns, and truncate cells", () => {
  const rows = limitedRows(
    Array.from({length: 40}, (_, index) => ({gid: `row-${index}`, extra: index, notes: "ok"})),
    25,
  );
  assert.equal(rows.length, 25);
  assert.deepEqual(collectionColumns([{notes: "a", name: "Board", gid: "proj-1", extra: true}]), [
    "gid",
    "name",
    "notes",
    "extra",
  ]);
  assert.equal(formatCell("one   two\nthree"), "one two three");
  assert.equal(formatCell(["proj-checkout", "proj-onboarding"]), "proj-checkout, proj-onboarding");
  assert.equal(formatCell("x".repeat(120)).endsWith("…"), true);
});

test("peek load failures are user-facing and ignore unmount aborts", () => {
  assert.equal(classifyPeekHttpStatus(200), null);
  const missing = classifyPeekHttpStatus(404);
  assert.equal(missing?.kind, "not-found");
  assert.equal(missing?.title, "Scenario file not found");
  assert.match(missing?.message ?? "", /GitHub does not have this scenario/);
  assert.equal(classifyPeekHttpStatus(429)?.title, "GitHub blocked the request");
  assert.equal(classifyPeekHttpStatus(503)?.title, "GitHub had an error");
  const timedOut = new AbortController();
  timedOut.abort("timeout");
  const timeout = classifyPeekException(new DOMException("Aborted", "AbortError"), timedOut.signal);
  assert.equal(timeout?.kind, "timeout");
  assert.equal(timeout?.title, "This is taking too long");
  const cancelled = new AbortController();
  cancelled.abort();
  assert.equal(classifyPeekException(new DOMException("Aborted", "AbortError"), cancelled.signal), null);
  const invalid = readScenarioPeekPayload({not: "a scenario"});
  assert.equal(invalid.ok, false);
  if (!invalid.ok) {
    assert.equal(invalid.error.title, "No seeded data to show");
  }
  assert.equal(missingPeekSource().title, "Peek is not configured");
});

test("loadScenarioPeek maps HTTP and parse failures, and swallows cancel", async () => {
  const original = globalThis.fetch;
  const signal = new AbortController().signal;
  try {
    globalThis.fetch = async () => new Response("404: Not Found", {status: 404});
    const missing = await loadScenarioPeek("https://example.test/missing.json", signal);
    assert.ok(missing);
    assert.equal(missing.ok, false);
    if (!missing.ok) {
      assert.equal(missing.error.kind, "not-found");
      assert.equal(missing.error.title, "Scenario file not found");
    }
    globalThis.fetch = async () => new Response("{not json", {status: 200, headers: {"Content-Type": "application/json"}});
    const invalid = await loadScenarioPeek("https://example.test/broken.json", signal);
    assert.ok(invalid);
    assert.equal(invalid.ok, false);
    if (!invalid.ok) {
      assert.equal(invalid.error.kind, "invalid");
      assert.equal(invalid.error.title, "Scenario file could not be read");
    }
    globalThis.fetch = async () =>
      new Response(fs.readFileSync(path.join(resourcesRoot, "gmail/scenarios/minimal.v1.json")), {status: 200});
    const ready = await loadScenarioPeek("https://example.test/gmail.json", signal);
    assert.ok(ready);
    assert.equal(ready.ok, true);
    if (ready.ok) {
      assert.equal(ready.data.resource, "gmail");
      assert.ok(ready.data.collections.length > 0);
    }
    const aborted = new AbortController();
    aborted.abort();
    globalThis.fetch = async () => {
      throw new DOMException("Aborted", "AbortError");
    };
    assert.equal(await loadScenarioPeek("https://example.test/cancelled.json", aborted.signal), null);
  } finally {
    globalThis.fetch = original;
  }
});
