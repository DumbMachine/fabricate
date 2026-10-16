import assert from "node:assert/strict";
import {readFile, writeFile} from "node:fs/promises";

import okta from "@okta/okta-sdk-nodejs";

const [mode] = process.argv.slice(2);
if (mode !== "direct" && mode !== "proxy") {
  throw new Error("usage: node okta-sdk.mjs <direct|proxy>");
}

const token = process.env.FAB_OKTA_TOKEN;
if (!token) {
  throw new Error("FAB_OKTA_TOKEN is required");
}

let orgUrl;
if (mode === "direct") {
  orgUrl = process.env.FAB_OKTA_URL;
  if (!orgUrl) {
    throw new Error("FAB_OKTA_URL is required in direct mode");
  }
} else {
  // Do not configure a Fabricate URL here. The official client calls its
  // normal org host; the SDK's https-proxy-agent picks up HTTPS_PROXY and
  // Node trusts the Fabricate CA through NODE_EXTRA_CA_CERTS.
  if (!process.env.HTTPS_PROXY) {
    throw new Error("HTTPS_PROXY is required in proxy mode");
  }
  orgUrl = "https://acme.okta.com";
}

// Configured the way an app configures an Okta API token: the default
// authorizationMode is SSWS, so every request carries `Authorization: SSWS`.
const client = new okta.Client({orgUrl, token, authorizationMode: "SSWS"});

const requested = [];
client.requestExecutor.on("request", (request) => {
  requested.push({method: request.method ?? "GET", url: new URL(request.url)});
});

async function collect(collection) {
  const items = [];
  for await (const item of collection) {
    items.push(item);
  }
  return items;
}

const operations = {};

// The SDK's Collection iterator follows Link rel="next" across pages. With
// limit=3 the eight listable users (DEPROVISIONED is omitted) take 3 pages.
const pageStart = requested.length;
const paged = await collect(await client.userApi.listUsers({limit: 3}));
const pageRequests = requested.slice(pageStart).filter((r) => r.url.pathname === "/api/v1/users");
assert.equal(paged.length, 8, "expected every listable seeded user across pages");
assert.equal(new Set(paged.map((user) => user.id)).size, 8, "pages must not repeat users");
assert.equal(pageRequests.length, 3, "limit=3 should take exactly three pages");
assert.equal(pageRequests[0].url.searchParams.get("after"), null, "first page has no cursor");
for (const [index, request] of pageRequests.entries()) {
  assert.equal(request.url.searchParams.get("limit"), "3", `page ${index + 1} keeps limit`);
  if (mode === "proxy") {
    assert.equal(request.url.host, "acme.okta.com", `page ${index + 1} stays on the production host`);
    assert.equal(request.url.protocol, "https:", `page ${index + 1} stays on https`);
  }
}
assert.equal(pageRequests[1].url.searchParams.get("after"), paged[2].id);
assert.equal(pageRequests[2].url.searchParams.get("after"), paged[5].id);
const byEmail = new Map(paged.map((user) => [user.profile.email, user]));
assert.equal(byEmail.get("val@acme.example")?.status, "ACTIVE");
assert.equal(byEmail.get("jordan.hale@acme.example")?.status, "STAGED");
assert.ok(paged.every((user) => user.status !== "DEPROVISIONED"), "default list omits DEPROVISIONED");

const unpaged = await collect(await client.userApi.listUsers());
assert.deepEqual(unpaged.map((user) => user.id), paged.map((user) => user.id), "paging preserves order");

const deprovisioned = await collect(await client.userApi.listUsers({search: 'status eq "DEPROVISIONED"'}));
assert.deepEqual(deprovisioned.map((user) => user.profile.login), ["old.contractor@acme.example"]);
const seededUsers = paged.length + deprovisioned.length;
assert.equal(seededUsers, 9, "expected all nine seeded Acme users");
operations.paginate = "passed";

const val = await client.userApi.getUser({userId: "00u-val"});
assert.equal(val.profile.email, "val@acme.example");
assert.equal(val.status, "ACTIVE");
const byLogin = await client.userApi.getUser({userId: "aisha@acme.example"});
assert.equal(byLogin.id, "00u-aisha");
operations.read = "passed";

const groups = await collect(await client.groupApi.listGroups());
assert.deepEqual(groups.map((group) => group.profile.name).sort(), ["Finance", "Support"]);
const finance = await collect(await client.groupApi.listGroupUsers({groupId: "00g-finance"}));
assert.deepEqual(finance.map((user) => user.profile.email).sort(), ["aisha@acme.example", "val@acme.example"]);

const apps = await collect(await client.applicationApi.listApplications());
assert.deepEqual(apps.map((app) => app.label).sort(), ["Acme Checkout", "Shopify Admin"]);
const checkout = await client.applicationApi.getApplication({appId: "0oa-acme-checkout"});
assert.equal(checkout.label, "Acme Checkout");
assert.match(checkout.settings?.notes?.admin ?? "", /contoso-eu/);
const assigned = await collect(await client.applicationApi.listApplicationUsers({appId: "0oa-shopify-admin"}));
assert.deepEqual(assigned.map((user) => user.id).sort(), ["00u-ravi", "00u-val"]);
operations.directory = "passed";

const created = await client.userApi.createUser({
  body: {
    profile: {
      firstName: "Casey",
      lastName: "Ng",
      email: "casey.ng@acme.example",
      login: "casey.ng@acme.example",
    },
  },
});
assert.ok(created.id, "createUser should return an Okta user id");
assert.equal(created.status, "ACTIVE");
operations.write = "passed";

const fetched = await client.userApi.getUser({userId: created.id});
assert.equal(fetched.profile.email, "casey.ng@acme.example");
const after = await collect(await client.userApi.listUsers({limit: 3}));
assert.equal(after.length, 9, "created user should persist across paged reads");
assert.ok(after.some((user) => user.id === created.id));
operations.persistence = "passed";

const modeReport = {
  messagesAfter: after.length,
  messagesBefore: paged.length,
  operations,
  status: "passed",
};
const reportPath = process.env.FAB_COMPATIBILITY_REPORT;
if (reportPath) {
  let report = {
    api: "Okta Management API 2024-07",
    environment: {label: "Acme Okta", manifest: "environments/acme-okta.yaml", messages: seededUsers},
    integration: "okta",
    modes: {},
    operationLabels: {
      paginate: "Page through users",
      read: "Retrieve user",
      directory: "List groups and apps",
      write: "Create user",
      persistence: "Confirm created user",
    },
    testedAt: new Date().toISOString(),
    testedCommit: process.env.FAB_COMPATIBILITY_COMMIT ?? "unknown",
    verification: {
      client: `@okta/okta-sdk-nodejs@${process.env.FAB_COMPATIBILITY_SDK_VERSION ?? "unknown"}`,
      kind: "official-sdk",
      label: "Official SDK",
      title: "Official SDK verification",
    },
  };
  try {
    report = JSON.parse(await readFile(reportPath, "utf8"));
  } catch (error) {
    if (error?.code !== "ENOENT") throw error;
  }
  report.modes[mode] = modeReport;
  report.testedAt = new Date().toISOString();
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
}

console.log(JSON.stringify({mode, ...modeReport, createdID: created.id, pages: pageRequests.length}));
