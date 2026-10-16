import assert from "node:assert/strict";
import test from "node:test";

import { developmentGitRef, isUpstreamRemoteUrl } from "./git-ref.mjs";

test("isUpstreamRemoteUrl accepts only DumbMachine/fabricate on GitHub", () => {
  for (const url of [
    "git@github.com:DumbMachine/fabricate.git",
    "git@github.com:dumbmachine/fabricate",
    "https://github.com/DumbMachine/fabricate",
    "https://github.com/DumbMachine/fabricate.git",
    "https://token@github.com/DumbMachine/fabricate.git",
    "ssh://git@github.com/DumbMachine/fabricate.git",
  ]) {
    assert.equal(isUpstreamRemoteUrl(url), true, url);
  }
  for (const url of [
    "git@github.com:neerajnabar/fabricate.git",
    "https://github.com/DumbMachine/fabricate-fork.git",
    "https://github.com/other/DumbMachine/fabricate.git",
    "https://gitlab.com/DumbMachine/fabricate.git",
    "/Users/me/Code/fabricate",
    "",
    undefined,
  ]) {
    assert.equal(isUpstreamRemoteUrl(url), false, String(url));
  }
});

// fakeGit answers the four git calls developmentGitRef makes from a small
// repository description, and throws for anything unset, as git does.
const fakeGit = ({ branch, remote, merge, urls = {} }) => (args) => {
  const key = args.join(" ");
  if (key === "symbolic-ref --quiet --short HEAD" && branch) return branch;
  if (key === `config --get branch.${branch}.remote` && remote) return remote;
  if (key === `config --get branch.${branch}.merge` && merge) return merge;
  if (args[0] === "remote" && args[1] === "get-url" && urls[args[2]]) return urls[args[2]];
  throw new Error(`git ${key} failed`);
};

const urls = {
  origin: "git@github.com:DumbMachine/fabricate.git",
  fork: "git@github.com:neerajnabar/fabricate.git",
};

test("developmentGitRef follows a branch tracked on the upstream repository", () => {
  const git = fakeGit({ branch: "docs-fix", remote: "origin", merge: "refs/heads/feature/docs", urls });
  assert.equal(developmentGitRef({ git }), "feature/docs");
});

test("developmentGitRef falls back to main off the upstream repository", () => {
  const cases = {
    fork: { branch: "feat/x", remote: "fork", merge: "refs/heads/feat/x", urls },
    "local only": { branch: "feat/x", urls },
    "local tracking": { branch: "feat/x", remote: ".", merge: "refs/heads/main", urls },
    "detached HEAD": { urls },
    "unknown remote": { branch: "feat/x", remote: "gone", merge: "refs/heads/feat/x", urls },
    "non-branch merge": { branch: "feat/x", remote: "origin", merge: "refs/tags/v1", urls },
  };
  for (const [name, repository] of Object.entries(cases)) {
    assert.equal(developmentGitRef({ git: fakeGit(repository) }), "main", name);
  }
  assert.equal(developmentGitRef({ git: () => { throw new Error("git missing"); } }), "main");
});

test("developmentGitRef reads this checkout without failing", () => {
  assert.equal(typeof developmentGitRef({ cwd: process.cwd() }), "string");
});
