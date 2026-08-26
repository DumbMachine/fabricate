import { cp, readFile, rm, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawn } from "node:child_process";

const scriptDir = dirname(fileURLToPath(import.meta.url));
const repositoryRoot = resolve(scriptDir, "..", "..", "..");
const [app, task, ...args] = process.argv.slice(2);

if (!(["landing", "docs"].includes(app) && ["dev", "build", "check"].includes(task))) {
  throw new Error("Usage: mode.mjs <landing|docs> <dev|build|check> [tool arguments]");
}

// This is the sole switch for public-site mode. Only the staging scripts opt
// into a development build, so a release build can never inherit a developer's
// shell environment by accident.
const mode = task === "dev" || (process.env.FABRICATE_ALLOW_DEVELOPMENT_BUILD === "1" && process.env.FABRICATE_SITE_MODE === "development")
  ? "development"
  : "production";
const command = mode === "development" ? "fab-dev" : "fab";
const docsBase = process.env.FABRICATE_DOCS_BASE || "/docs";
const productionOrigin = process.env.FABRICATE_PROD_SITE_URL || "https://fabricate.dmach.in";
// The two dev servers need distinct, stable ports. The landing prompt points
// agents to the docs server, not back to the landing server.
const landingOrigin = mode === "development"
  ? process.env.FABRICATE_DEV_LANDING_URL || process.env.FABRICATE_DEV_SITE_URL || `http://localhost:${process.env.FABRICATE_DEV_LANDING_PORT || "4321"}`
  : productionOrigin;
const docsOrigin = mode === "development"
  ? process.env.FABRICATE_DEV_DOCS_URL || `http://localhost:${process.env.FABRICATE_DEV_DOCS_PORT || "4322"}`
  : productionOrigin;
const siteUrl = app === "docs" ? docsOrigin : landingOrigin;
const docsUrl = `${docsOrigin.replace(/\/$/, "")}${docsBase}`;
const appRoot = resolve(repositoryRoot, "apps", app);
const env = {
  ...process.env,
  FABRICATE_SITE_MODE: mode,
  PUBLIC_FABRICATE_SITE_MODE: mode,
  PUBLIC_FABRICATE_COMMAND: command,
  PUBLIC_FABRICATE_SITE_URL: siteUrl,
  PUBLIC_FABRICATE_DOCS_BASE: docsBase,
  PUBLIC_FABRICATE_DOCS_URL: docsUrl,
};

const run = (executable, commandArgs, options = {}) => new Promise((resolveRun, rejectRun) => {
  const child = spawn(executable, commandArgs, { cwd: appRoot, env, stdio: "inherit", ...options });
  child.on("error", rejectRun);
  child.on("exit", (code, signal) => {
    if (code === 0) resolveRun();
    else rejectRun(new Error(`${executable} ${commandArgs.join(" ")} ${signal ? `ended with ${signal}` : `exited with ${code}`}`));
  });
});

const prepareDevelopmentDocs = async () => {
  if (mode !== "development") return undefined;

  const source = resolve(repositoryRoot, "packages", "docs-content");
  const output = resolve(appRoot, ".fabricate-mode", "docs-content");
  await rm(output, { force: true, recursive: true });
  await cp(source, output, { recursive: true });

  const replaceCommands = async (directory) => {
    for (const entry of await (await import("node:fs/promises")).readdir(directory, { withFileTypes: true })) {
      const path = resolve(directory, entry.name);
      if (entry.isDirectory()) await replaceCommands(path);
      else if (/\.mdx?$/.test(entry.name)) {
        const contents = await readFile(path, "utf8");
        await writeFile(path, contents.replace(/(?<![A-Za-z0-9-])fab(?![A-Za-z0-9-])/g, command));
      }
    }
  };
  await replaceCommands(output);
  return output;
};

const withDocsIcon = async (work) => {
  const icon = resolve(appRoot, "public", "icon.svg");
  const original = await readFile(icon);
  try {
    if (mode === "development") {
      await writeFile(icon, await readFile(resolve(appRoot, "public", "icon-dev.svg")));
    }
    await work();
  } finally {
    await writeFile(icon, original);
  }
};

if (app === "landing") {
  const portArgs = task === "dev" && !args.includes("--port") ? ["--port", new URL(landingOrigin).port || "4321"] : [];
  await run("pnpm", ["exec", "astro", task, ...portArgs, ...args]);
} else {
  const contentDirectory = await prepareDevelopmentDocs();
  if (contentDirectory) env.FABRICATE_DOCS_CONTENT_DIR = contentDirectory;
  const portArgs = task === "dev" && !args.includes("--port") ? ["--port", new URL(docsOrigin).port || "4322"] : [];
  const checkArgs = task === "check" && !args.includes("--isolated") ? ["--isolated"] : [];
  await withDocsIcon(() => run("pnpm", ["exec", "blume", task, ...(contentDirectory && task === "dev" ? ["--content-dir", contentDirectory] : []), ...portArgs, ...checkArgs, ...args]));
}
