// Source docs name the default branch. Serving the site from another ref
// rewrites those Fabricate GitHub URLs so curl examples and scenario peeks
// fetch the same commit the site was built from.

export const FABRICATE_REPOSITORY = "DumbMachine/fabricate";
export const DEFAULT_FABRICATE_GIT_REF = "main";

const REF_PATTERN = /^[A-Za-z0-9._/-]+$/;

declare global {
  interface ImportMetaEnv {
    readonly PUBLIC_FABRICATE_GIT_REF?: string;
  }
  interface ImportMeta {
    readonly env: ImportMetaEnv;
  }
}

function configuredGitRef(): string | undefined {
  // The member expression must stay written as import.meta.env.PUBLIC_FABRICATE_GIT_REF
  // so Vite can inline it into client bundles. Node tests and the docs config
  // loader have no import.meta.env, and that read falls through to process.env.
  let fromMeta: string | undefined;
  try {
    const value = import.meta.env.PUBLIC_FABRICATE_GIT_REF;
    if (typeof value === "string" && value.trim()) {
      fromMeta = value;
    }
  } catch {
    fromMeta = undefined;
  }
  if (fromMeta) {
    return fromMeta;
  }
  if (typeof process !== "undefined" && process.env) {
    return process.env.PUBLIC_FABRICATE_GIT_REF || process.env.FABRICATE_GIT_REF;
  }
  return undefined;
}

export function fabricateGitRef(explicit?: string | null): string {
  const candidate = (explicit ?? configuredGitRef() ?? DEFAULT_FABRICATE_GIT_REF).trim();
  if (!candidate || candidate === "HEAD" || candidate.includes("..") || !REF_PATTERN.test(candidate)) {
    return DEFAULT_FABRICATE_GIT_REF;
  }
  return candidate;
}

export function rewriteFabricateGitRef(value: string, ref = fabricateGitRef()): string {
  if (!value || ref === DEFAULT_FABRICATE_GIT_REF) {
    return value;
  }
  const repo = FABRICATE_REPOSITORY;
  return value
    .replaceAll(
      `https://raw.githubusercontent.com/${repo}/${DEFAULT_FABRICATE_GIT_REF}/`,
      `https://raw.githubusercontent.com/${repo}/${ref}/`,
    )
    .replaceAll(
      `https://github.com/${repo}/blob/${DEFAULT_FABRICATE_GIT_REF}/`,
      `https://github.com/${repo}/blob/${ref}/`,
    )
    .replaceAll(
      `https://github.com/${repo}/tree/${DEFAULT_FABRICATE_GIT_REF}/`,
      `https://github.com/${repo}/tree/${ref}/`,
    );
}
