// Source docs name the default branch. Serving the site from another ref
// rewrites those Fabricate GitHub URLs so curl examples and scenario peeks
// fetch the same commit the site was built from. This module is pure: callers
// pass the configured ref in from the process or Vite edge.

export const FABRICATE_REPOSITORY = "DumbMachine/fabricate";
export const DEFAULT_FABRICATE_GIT_REF = "main";

const REF_PATTERN = /^[A-Za-z0-9._/-]+$/;
const FAB_COMMAND = /(^|[^A-Za-z0-9-])fab(?![A-Za-z0-9-])/g;

export type FabricateContentKind = "blob" | "raw" | "tree";

export function sanitizeGitRef(value?: string | null): string {
  const candidate = (value ?? "").trim();
  if (!candidate || candidate === "HEAD" || candidate.includes("..") || !REF_PATTERN.test(candidate)) {
    return DEFAULT_FABRICATE_GIT_REF;
  }
  return candidate;
}

export function fabricateContentURL(kind: FabricateContentKind, path: string, ref?: string | null): string {
  const safe = sanitizeGitRef(ref);
  const suffix = path.replace(/^\/+/, "");
  const repo = FABRICATE_REPOSITORY;
  switch (kind) {
    case "raw":
      return `https://raw.githubusercontent.com/${repo}/${safe}/${suffix}`;
    case "blob":
      return `https://github.com/${repo}/blob/${safe}/${suffix}`;
    case "tree":
      return `https://github.com/${repo}/tree/${safe}/${suffix}`;
    default: {
      const unexpected: never = kind;
      return unexpected;
    }
  }
}

const CONTENT_KINDS: FabricateContentKind[] = ["raw", "blob", "tree"];

export function rewritePublishedFabricateURLs(value: string, ref?: string | null): string {
  const safe = sanitizeGitRef(ref);
  if (!value || safe === DEFAULT_FABRICATE_GIT_REF) {
    return value;
  }
  let rewritten = value;
  for (const kind of CONTENT_KINDS) {
    rewritten = rewritten.replaceAll(
      fabricateContentURL(kind, "", DEFAULT_FABRICATE_GIT_REF),
      fabricateContentURL(kind, "", safe),
    );
  }
  return rewritten;
}

export function replaceFabCommand(value: string, command = "fab"): string {
  return value.replace(FAB_COMMAND, `$1${command}`);
}

export function rewritePublishedDocs(value: string, ref?: string | null, command = "fab"): string {
  return rewritePublishedFabricateURLs(replaceFabCommand(value, command), ref);
}
