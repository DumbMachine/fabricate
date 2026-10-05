import assert from "node:assert/strict";
import test from "node:test";

import {fabricateGitRef, rewriteFabricateGitRef} from "./git-ref.ts";

const raw = "https://raw.githubusercontent.com/DumbMachine/fabricate/main/environments/acme-gmail.yaml";
const blob = "https://github.com/DumbMachine/fabricate/blob/main/resources/gmail/scenarios/acme-corp.v1.json";
const tree = "https://github.com/DumbMachine/fabricate/tree/main/examples";
const other = "https://github.com/MarkusPfundstein/mcp-gsuite/blob/main/gmail-api-openapi-spec.yaml";

function withoutConfiguredRef(run: () => void) {
  const previousPublic = process.env.PUBLIC_FABRICATE_GIT_REF;
  const previous = process.env.FABRICATE_GIT_REF;
  delete process.env.PUBLIC_FABRICATE_GIT_REF;
  delete process.env.FABRICATE_GIT_REF;
  try {
    run();
  } finally {
    if (previousPublic === undefined) delete process.env.PUBLIC_FABRICATE_GIT_REF;
    else process.env.PUBLIC_FABRICATE_GIT_REF = previousPublic;
    if (previous === undefined) delete process.env.FABRICATE_GIT_REF;
    else process.env.FABRICATE_GIT_REF = previous;
  }
}

test("fabricateGitRef defaults to main and rejects unsafe values", () => {
  withoutConfiguredRef(() => {
    assert.equal(fabricateGitRef(), "main");
  });
  assert.equal(fabricateGitRef("  "), "main");
  assert.equal(fabricateGitRef("HEAD"), "main");
  assert.equal(fabricateGitRef("../main"), "main");
  assert.equal(fabricateGitRef("cursor/marque-resources-f1bf"), "cursor/marque-resources-f1bf");
});

test("rewriteFabricateGitRef leaves main and other repositories unchanged", () => {
  const source = `${raw}\n${blob}\n${tree}\n${other}`;
  assert.equal(rewriteFabricateGitRef(source, "main"), source);
  withoutConfiguredRef(() => {
    assert.equal(rewriteFabricateGitRef(source), source);
  });
  const rewritten = rewriteFabricateGitRef(source, "cursor/marque-resources-f1bf");
  assert.match(rewritten, /fabricate\/cursor\/marque-resources-f1bf\/environments\/acme-gmail.yaml/);
  assert.match(rewritten, /blob\/cursor\/marque-resources-f1bf\/resources\/gmail/);
  assert.match(rewritten, /tree\/cursor\/marque-resources-f1bf\/examples/);
  assert.match(rewritten, /MarkusPfundstein\/mcp-gsuite\/blob\/main\//);
});

test("rewriteFabricateGitRef reads PUBLIC_FABRICATE_GIT_REF", () => {
  const previous = process.env.PUBLIC_FABRICATE_GIT_REF;
  process.env.PUBLIC_FABRICATE_GIT_REF = "feature/docs";
  try {
    assert.equal(fabricateGitRef(), "feature/docs");
    assert.match(rewriteFabricateGitRef(raw), /fabricate\/feature\/docs\/environments/);
  } finally {
    if (previous === undefined) {
      delete process.env.PUBLIC_FABRICATE_GIT_REF;
    } else {
      process.env.PUBLIC_FABRICATE_GIT_REF = previous;
    }
  }
});
