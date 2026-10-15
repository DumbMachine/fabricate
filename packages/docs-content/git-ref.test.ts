import assert from "node:assert/strict";
import test from "node:test";

import {
  fabricateContentURL,
  replaceFabCommand,
  rewritePublishedDocs,
  rewritePublishedFabricateURLs,
  sanitizeGitRef,
} from "./git-ref.ts";

const raw = "https://raw.githubusercontent.com/DumbMachine/fabricate/main/environments/acme-gmail.yaml";
const blob = "https://github.com/DumbMachine/fabricate/blob/main/resources/gmail/scenarios/acme-corp.v1.json";
const tree = "https://github.com/DumbMachine/fabricate/tree/main/examples";
const other = "https://github.com/MarkusPfundstein/mcp-gsuite/blob/main/gmail-api-openapi-spec.yaml";

test("sanitizeGitRef defaults to main and rejects unsafe values", () => {
  assert.equal(sanitizeGitRef(), "main");
  assert.equal(sanitizeGitRef(null), "main");
  assert.equal(sanitizeGitRef("  "), "main");
  assert.equal(sanitizeGitRef("HEAD"), "main");
  assert.equal(sanitizeGitRef("../main"), "main");
  assert.equal(sanitizeGitRef("cursor/marque-resources-f1bf"), "cursor/marque-resources-f1bf");
});

test("fabricateContentURL builds raw, blob, and tree links from the sanitized ref", () => {
  assert.equal(
    fabricateContentURL("raw", "environments/acme-gmail.yaml", "cursor/marque-resources-f1bf"),
    "https://raw.githubusercontent.com/DumbMachine/fabricate/cursor/marque-resources-f1bf/environments/acme-gmail.yaml",
  );
  assert.equal(
    fabricateContentURL("blob", "/resources/gmail/scenarios/acme-corp.v1.json", "HEAD"),
    "https://github.com/DumbMachine/fabricate/blob/main/resources/gmail/scenarios/acme-corp.v1.json",
  );
  assert.equal(
    fabricateContentURL("tree", "examples"),
    "https://github.com/DumbMachine/fabricate/tree/main/examples",
  );
});

test("rewritePublishedFabricateURLs sanitizes an explicit ref and leaves other repositories", () => {
  const source = `${raw}\n${blob}\n${tree}\n${other}`;
  assert.equal(rewritePublishedFabricateURLs(source, "main"), source);
  assert.equal(rewritePublishedFabricateURLs(source, "../main"), source);
  assert.equal(rewritePublishedFabricateURLs(source), source);
  const rewritten = rewritePublishedFabricateURLs(source, "cursor/marque-resources-f1bf");
  assert.match(rewritten, /fabricate\/cursor\/marque-resources-f1bf\/environments\/acme-gmail.yaml/);
  assert.match(rewritten, /blob\/cursor\/marque-resources-f1bf\/resources\/gmail/);
  assert.match(rewritten, /tree\/cursor\/marque-resources-f1bf\/examples/);
  assert.match(rewritten, /MarkusPfundstein\/mcp-gsuite\/blob\/main\//);
});

test("rewritePublishedDocs rewrites the fab command and the git ref together", () => {
  const source = "fab run /dev/stdin\n" + raw;
  const rewritten = rewritePublishedDocs(source, "feature/docs", "fab-dev");
  assert.match(rewritten, /^fab-dev run/);
  assert.match(rewritten, /fabricate\/feature\/docs\/environments/);
  assert.equal(replaceFabCommand("prefab fab fabricate", "fab-dev"), "prefab fab-dev fabricate");
});
