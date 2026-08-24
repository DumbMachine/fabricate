// Lifecycle helpers shared by the SDK-conformance tests. They drive the
// real `fab` binary the same way the Go e2e does: an isolated state file
// (never the developer's ~/.config/fab), the built-in profile catalog,
// create → hand back the URL → destroy on teardown.
//
// A missing prerequisite (no fab binary, no Docker, no image) is a SKIP
// with a clear message — never a silent pass. This mirrors
// e2e/e2e_test.go's requireDocker + haveImage gates.
import { execFileSync } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const FAB_BIN = process.env.FAB_BIN || "fab";
const HTTPMOCK_IMAGE = process.env.FAB_HTTPMOCK_IMAGE || "fabricate/httpmock:local";
const EMULATE_IMAGE = process.env.FAB_EMULATE_IMAGE || "fabricate/emulate:local";

function runs(cmd, args) {
  try {
    execFileSync(cmd, args, { stdio: "ignore" });
    return true;
  } catch {
    return false;
  }
}

// preflight returns a human-readable skip reason, or null when the suite
// can run. Check it at the top of each test and `t.skip(reason)` if set.
//
// runtime: "httpmock" (default) or "emulate" (GitHub / vercel-labs emulate).
export function preflight({ runtime = "httpmock" } = {}) {
  if (!runs(FAB_BIN, ["--help"])) {
    return `fab binary not runnable — set FAB_BIN or run via 'make sdk-e2e' (got: ${FAB_BIN})`;
  }
  if (!runs("docker", ["info"])) {
    return "docker daemon not available";
  }
  const image = runtime === "emulate" ? EMULATE_IMAGE : HTTPMOCK_IMAGE;
  if (!runs("docker", ["image", "inspect", image])) {
    return `${image} missing — run 'make images' first`;
  }
  return null;
}

// missingProfile is a skip reason when the catalog has no such
// engine/profile yet (Stripe isn't shipped). Used so the SDK test
// can land before the mock, then become a hard gate.
export function missingProfile(engine, profile) {
  try {
    execFileSync(FAB_BIN, ["profiles", "show", profile, "-o", "json"], {
      encoding: "utf8",
      stdio: ["ignore", "pipe", "ignore"],
    });
    return null;
  } catch {
    return `${engine}/${profile} is not in the catalog yet — SDK test is the acceptance gate once it ships`;
  }
}

// createInstance provisions a real fab instance from a built-in profile
// in an isolated state dir, and returns { url, name, destroy }. Call
// destroy() in a finally so a failed assertion still tears the container
// down.
export function createInstance(engine, profile, name) {
  const dir = mkdtempSync(join(tmpdir(), "fab-sdk-"));
  const env = {
    ...process.env,
    FAB_STATE_FILE: join(dir, "state.json"),
    // Point user-profiles at an empty dir so only the embedded catalog
    // is visible — the test must not depend on the developer's profiles.
    FAB_PROFILES_DIR: join(dir, "no-user-profiles"),
  };
  const out = execFileSync(
    FAB_BIN,
    ["create", engine, "-p", profile, "--name", name, "-o", "json"],
    { env, encoding: "utf8" },
  );
  const info = JSON.parse(out);
  const creds = info.creds || {};
  return {
    url: creds.url,
    token: creds.password || "",
    host: creds.host,
    port: creds.port,
    name,
    destroy() {
      try {
        execFileSync(FAB_BIN, ["destroy", name], { env, stdio: "ignore" });
      } catch {
        // destroy is best-effort on teardown; a leaked container is
        // visible via `docker ps` and reaped by the next `fab destroy`.
      }
      rmSync(dir, { recursive: true, force: true });
    },
  };
}

// bearerAuth builds a fake Google OAuth2 client. The mock ignores auth,
// but the official googleapis client refuses to issue a request without a
// token attached, so we set a throwaway one.
export function googleAuth(google, token = "sdk-conformance") {
  const auth = new google.auth.OAuth2();
  auth.setCredentials({ access_token: token });
  return auth;
}
