// Conformance: drive fab's GitHub engine (vercel-labs emulate) with
// GitHub's official JS client, @octokit/rest — the same package apps
// and agents use. baseUrl is the Enterprise/mock seam; auth is the
// seeded bearer token. Reads + one stateful write (create issue).
import test from "node:test";
import assert from "node:assert/strict";
import { Octokit } from "@octokit/rest";
import { preflight, createInstance } from "./lib/fab.mjs";

test("github mock ⇄ @octokit/rest", async (t) => {
  const skip = preflight({ runtime: "emulate" });
  if (skip) return t.skip(skip);

  const inst = createInstance("github", "issues", "sdk-github");
  try {
    const octokit = new Octokit({
      auth: inst.token,
      baseUrl: inst.url.replace(/\/$/, ""),
    });

    await t.test("users.getAuthenticated is the seeded octocat", async () => {
      const { data } = await octokit.rest.users.getAuthenticated();
      assert.equal(data.login, "octocat");
    });

    await t.test("repos.get returns octocat/hello-world", async () => {
      const { data } = await octokit.rest.repos.get({
        owner: "octocat",
        repo: "hello-world",
      });
      assert.equal(data.full_name, "octocat/hello-world");
    });

    let before;
    await t.test("issues.listForRepo sees the seeded backlog", async () => {
      const { data } = await octokit.rest.issues.listForRepo({
        owner: "octocat",
        repo: "hello-world",
        state: "all",
        per_page: 100,
      });
      assert.ok(data.length >= 1, "expected seeded issues");
      before = data.length;
    });

    let created;
    await t.test("issues.create is a stateful write", async () => {
      const { data } = await octokit.rest.issues.create({
        owner: "octocat",
        repo: "hello-world",
        title: "sdk-conformance: octokit write",
        body: "planted by e2e/sdk/github.test.mjs",
      });
      assert.ok(data.number > 0);
      assert.equal(data.title, "sdk-conformance: octokit write");
      created = data;
    });

    await t.test("issues.get + list reflect the write", async () => {
      const { data: got } = await octokit.rest.issues.get({
        owner: "octocat",
        repo: "hello-world",
        issue_number: created.number,
      });
      assert.equal(got.title, created.title);

      const { data: listed } = await octokit.rest.issues.listForRepo({
        owner: "octocat",
        repo: "hello-world",
        state: "all",
        per_page: 100,
      });
      assert.equal(listed.length, before + 1);
    });
  } finally {
    inst.destroy();
  }
});
