import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import {fileURLToPath} from "node:url";
import test from "node:test";

import {
  parseCommandExample,
  parseCompatibility,
  parseOperationsCatalog,
  parseScenarioCatalog,
  formatScenarioCounts,
  readGeneratedJSON,
} from "./generated.ts";

test("readGeneratedJSON returns null for a missing file", () => {
  assert.equal(readGeneratedJSON("does-not-exist.json"), null);
});

test("readGeneratedJSON returns null for path escape", () => {
  assert.equal(readGeneratedJSON("../package.json"), null);
  assert.equal(readGeneratedJSON("/tmp/x.json"), null);
});

test("readGeneratedJSON returns null for corrupt JSON", () => {
  const dir = fileURLToPath(new URL("../_generated/", import.meta.url));
  const dest = path.join(dir, "generated-corrupt-test.json");
  fs.writeFileSync(dest, "{not json");
  try {
    assert.equal(readGeneratedJSON("generated-corrupt-test.json"), null);
  } finally {
    fs.unlinkSync(dest);
  }
});

test("parse helpers reject empty and partial payloads", () => {
  assert.equal(parseCommandExample(null), null);
  assert.equal(parseCommandExample({id: "x"}), null);
  assert.equal(parseCompatibility({integration: "gmail"}), null);
  assert.equal(parseOperationsCatalog({integration: "gmail", api: "Gmail v1", operations: []}), null);
  assert.equal(parseOperationsCatalog({integration: "gmail", api: "Gmail v1", operations: [{method: "GET"}]}), null);
});

test("parseOperationsCatalog keeps valid rows and drops junk", () => {
  const catalog = parseOperationsCatalog({
    integration: "gmail",
    api: "Gmail v1",
    operations: [
      {method: "GET", path: "/profile", operationId: "getProfile", summary: "Profile"},
      {method: "POST"},
    ],
  });
  assert.ok(catalog);
  assert.equal(catalog.operations.length, 1);
  assert.equal(catalog.operations[0].operationId, "getProfile");
});

test("parseScenarioCatalog keeps counts and listable", () => {
  const catalog = parseScenarioCatalog({
    integration: "gmail",
    scenarios: [
      {id: "gmail.acme-corp.v1", counts: {messages: 28, labels: 6}, listable: {messages: 27}},
      {id: "gmail.minimal.v1", counts: {messages: 0, labels: 0}},
      {id: "bad"},
    ],
  });
  assert.ok(catalog);
  assert.equal(catalog.scenarios.length, 2);
  assert.equal(formatScenarioCounts(catalog.scenarios[0]), "28 messages · 6 labels · 27 listed by default");
  assert.equal(formatScenarioCounts(catalog.scenarios[1]), "0 messages · 0 labels");
});

test("gmail seed counts agree across generated docs artifacts", () => {
  const catalog = parseScenarioCatalog(readGeneratedJSON("gmail.scenarios.json"));
  assert.ok(catalog);
  const acme = catalog.scenarios.find((entry) => entry.id === "gmail.acme-corp.v1");
  assert.ok(acme);
  assert.equal(acme.counts.messages, 28);
  assert.equal(acme.listable?.messages, 27);

  const compatibility = parseCompatibility(readGeneratedJSON("gmail.compatibility.json"));
  assert.ok(compatibility);
  assert.equal(compatibility.environment.messages, acme.counts.messages);

  const example = parseCommandExample(readGeneratedJSON("gmail-list-messages.json"));
  assert.ok(example);
  assert.ok(isRecord(example.output));
  assert.equal(example.output.resultSizeEstimate, acme.listable?.messages);
});

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

test("readGeneratedJSON loads a committed snapshot", () => {
  const got = readGeneratedJSON("gmail.operations.json");
  assert.ok(got && typeof got === "object");
  const catalog = parseOperationsCatalog(got);
  assert.ok(catalog);
  assert.equal(catalog.integration, "gmail");
  assert.ok(catalog.operations.length > 0);
});
