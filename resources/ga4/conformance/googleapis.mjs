import assert from "node:assert/strict";
import {readFile, writeFile} from "node:fs/promises";

import {google} from "googleapis";

// The GAPIC client (@google-analytics/data) is not used here: its gRPC
// transport needs a gRPC server, and its REST fallback hands gaxios a fixed
// keep-alive https.Agent, which makes gaxios ignore HTTPS_PROXY. The Google
// APIs Node client is the official REST client for the same Data API v1beta
// surface and honors the process proxy and CA settings.

const [mode] = process.argv.slice(2);
if (mode !== "direct" && mode !== "proxy") {
  throw new Error("usage: node googleapis.mjs <direct|proxy>");
}

const token = process.env.FAB_GA4_TOKEN;
if (!token) {
  throw new Error("FAB_GA4_TOKEN is required");
}

const auth = new google.auth.OAuth2("fab-client", "fab-secret");
let analyticsdata;
if (mode === "direct") {
  const serviceURL = process.env.FAB_GA4_URL;
  if (!serviceURL) {
    throw new Error("FAB_GA4_URL is required in direct mode");
  }
  // google-auth-library refreshes within five minutes of expiry. Keep the
  // direct credential comfortably outside that window; proxy mode below is
  // deliberately expired to prove the synthetic refresh route.
  auth.setCredentials({access_token: token, expiry_date: Date.now() + 60 * 60 * 1_000});
  analyticsdata = google.analyticsdata({
    version: "v1beta",
    auth,
    rootUrl: `${serviceURL.replace(/\/$/, "")}/`,
  });
} else {
  // Do not configure a Fabricate URL here. The official client must refresh
  // against oauth2.googleapis.com, then call analyticsdata.googleapis.com via
  // FAB's process-scoped proxy and CA settings.
  auth.setCredentials({
    access_token: "expired",
    expiry_date: Date.now() - 60_000,
    refresh_token: "fab-refresh-token",
  });
  analyticsdata = google.analyticsdata({version: "v1beta", auth});
}

const property = "properties/309712345";
const operations = {};

const report = await analyticsdata.properties.runReport({
  property,
  requestBody: {
    dateRanges: [{startDate: "2026-08-01", endDate: "2026-08-26"}],
    dimensions: [
      {name: "transactionId"},
      {name: "sessionSource"},
      {name: "sessionMedium"},
      {name: "sessionCampaignName"},
      {name: "eventName"},
    ],
    metrics: [{name: "eventCount"}, {name: "purchaseRevenue"}, {name: "itemRefundAmount"}],
    orderBys: [{dimension: {dimensionName: "transactionId"}}],
  },
});
assert.equal(report.data.kind, "analyticsData#runReport");
assert.equal(report.data.rowCount, 3, "expected the three Acme Goods transactions");
assert.equal(report.data.metadata?.currencyCode, "INR");
assert.deepEqual(
  report.data.dimensionHeaders.map((header) => header.name),
  ["transactionId", "sessionSource", "sessionMedium", "sessionCampaignName", "eventName"],
);
assert.deepEqual(
  report.data.metricHeaders.map((header) => [header.name, header.type]),
  [
    ["eventCount", "TYPE_INTEGER"],
    ["purchaseRevenue", "TYPE_CURRENCY"],
    ["itemRefundAmount", "TYPE_CURRENCY"],
  ],
);
const rows = Object.fromEntries(
  report.data.rows.map((row) => [
    row.dimensionValues[0].value,
    {
      dimensions: row.dimensionValues.slice(1).map((value) => value.value),
      metrics: row.metricValues.map((value) => value.value),
    },
  ]),
);
assert.deepEqual(Object.keys(rows), ["10482", "10483", "10484"]);
assert.deepEqual(rows["10482"].dimensions, ["whatsapp", "gupshup", "monsoon-tee", "purchase"]);
assert.equal(Number(rows["10482"].metrics[1]), 3097, "10482 purchase revenue");
assert.deepEqual(rows["10483"].dimensions.slice(0, 2), ["google", "cpc"]);
assert.equal(rows["10483"].dimensions[3], "purchase");
assert.equal(Number(rows["10483"].metrics[1]), 499, "10483 purchase revenue");
assert.equal(rows["10484"].dimensions[3], "refund");
assert.equal(Number(rows["10484"].metrics[2]), 1499, "10484 refund amount");

const metadata = await analyticsdata.properties.getMetadata({name: `${property}/metadata`});
assert.equal(metadata.data.name, `${property}/metadata`);
assert.ok(metadata.data.dimensions.some((dimension) => dimension.apiName === "transactionId"));
assert.ok(metadata.data.metrics.some((metric) => metric.apiName === "purchaseRevenue"));
operations.read = "passed";

const created = await analyticsdata.properties.audienceExports.create({
  parent: property,
  requestBody: {
    audience: `${property}/audiences/purchasers`,
    dimensions: [{dimensionName: "transactionId"}],
  },
});
assert.equal(created.data.done, true);
assert.equal(created.data.response?.["@type"], "type.googleapis.com/google.analytics.data.v1beta.AudienceExport");
assert.equal(created.data.metadata?.["@type"], "type.googleapis.com/google.analytics.data.v1beta.AudienceExportMetadata");
const exportName = created.data.response?.name;
assert.ok(exportName?.startsWith(`${property}/audienceExports/`), "audience export name");
assert.equal(created.data.response.state, "ACTIVE");
operations.write = "passed";

const got = await analyticsdata.properties.audienceExports.get({name: exportName});
assert.equal(got.data.audience, `${property}/audiences/purchasers`);
assert.equal(got.data.state, "ACTIVE");
const listed = await analyticsdata.properties.audienceExports.list({parent: property});
assert.ok(listed.data.audienceExports.some((item) => item.name === exportName));
operations.persistence = "passed";

const modeReport = {
  messagesAfter: listed.data.audienceExports.length,
  messagesBefore: 0,
  operations,
  status: "passed",
};
const reportPath = process.env.FAB_COMPATIBILITY_REPORT;
if (reportPath) {
  let compat = {
    api: "Google Analytics Data API v1beta",
    environment: {
      label: "Acme Google Analytics 4",
      manifest: "environments/acme-ga4.yaml",
      messages: 3,
    },
    integration: "ga4",
    modes: {},
    operationLabels: {
      read: "Run Acme Goods report",
      write: "Create audience export",
      persistence: "Confirm audience export",
    },
    testedAt: new Date().toISOString(),
    testedCommit: process.env.FAB_COMPATIBILITY_COMMIT ?? "unknown",
    verification: {
      client: `googleapis@${process.env.FAB_COMPATIBILITY_SDK_VERSION ?? "unknown"}`,
      kind: "official-sdk",
      label: "Official SDK",
      title: "Official SDK verification",
    },
  };
  try {
    compat = JSON.parse(await readFile(reportPath, "utf8"));
  } catch (error) {
    if (error?.code !== "ENOENT") throw error;
  }
  compat.modes[mode] = modeReport;
  compat.testedAt = new Date().toISOString();
  await writeFile(reportPath, `${JSON.stringify(compat, null, 2)}\n`);
}

console.log(JSON.stringify({mode, ...modeReport, audienceExport: exportName}));
