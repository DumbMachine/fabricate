import { cp, mkdir, rm } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const scriptDir = dirname(fileURLToPath(import.meta.url));
const siteRoot = resolve(scriptDir, "..");
const repositoryRoot = resolve(siteRoot, "..", "..");
const output = resolve(siteRoot, "dist");

await rm(output, { force: true, recursive: true });
await mkdir(output, { recursive: true });
await cp(resolve(repositoryRoot, "apps/landing/dist"), output, { recursive: true });
await cp(resolve(repositoryRoot, "apps/docs/dist"), resolve(output, "docs"), { recursive: true });
