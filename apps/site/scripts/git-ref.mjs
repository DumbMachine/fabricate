import { execFileSync } from "node:child_process";

// Matches the upstream repository in packages/docs-content/git-ref.ts
// (FABRICATE_REPOSITORY). Docs links only point at this repository, so a
// branch that lives only locally or on a fork has nothing to link to.
const UPSTREAM_REMOTE = /^(?:(?:https?|ssh|git):\/\/)?(?:[^@/]+@)?github\.com[:/]DumbMachine\/fabricate(?:\.git)?\/?$/i;

export function isUpstreamRemoteUrl(url) {
  return UPSTREAM_REMOTE.test((url || "").trim());
}

const runGit = (cwd) => (args) => execFileSync("git", args, {
  cwd,
  encoding: "utf8",
  stdio: ["ignore", "pipe", "ignore"],
}).trim();

// The ref a development server links to: the checked-out branch's upstream
// branch when that upstream is on DumbMachine/fabricate, otherwise main. This
// reads only local git config, so it works offline and never fetches. It does
// not validate the name; packages/docs-content/git-ref.ts does that for every
// reader.
export function developmentGitRef({ cwd, git = runGit(cwd) } = {}) {
  try {
    const branch = git(["symbolic-ref", "--quiet", "--short", "HEAD"]);
    const remote = git(["config", "--get", `branch.${branch}.remote`]);
    const merge = git(["config", "--get", `branch.${branch}.merge`]);
    if (!merge.startsWith("refs/heads/")) return "main";
    if (!isUpstreamRemoteUrl(git(["remote", "get-url", remote]))) return "main";
    return merge.slice("refs/heads/".length) || "main";
  } catch {
    return "main";
  }
}
