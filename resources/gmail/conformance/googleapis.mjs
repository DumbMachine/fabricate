import assert from "node:assert/strict";
import {readFile, writeFile} from "node:fs/promises";

import {google} from "googleapis";

const [mode] = process.argv.slice(2);
if (mode !== "direct" && mode !== "proxy") {
  throw new Error("usage: node googleapis.mjs <direct|proxy>");
}

const token = process.env.FAB_SUPPORT_MAIL_TOKEN;
if (!token) {
  throw new Error("FAB_SUPPORT_MAIL_TOKEN is required");
}

const auth = new google.auth.OAuth2("fab-client", "fab-secret");
let gmail;
if (mode === "direct") {
  const serviceURL = process.env.FAB_SUPPORT_MAIL_URL;
  if (!serviceURL) {
    throw new Error("FAB_SUPPORT_MAIL_URL is required in direct mode");
  }
  // google-auth-library refreshes within five minutes of expiry. Keep the
  // direct credential comfortably outside that window; proxy mode below is
  // deliberately expired to prove the synthetic refresh route.
  auth.setCredentials({access_token: token, expiry_date: Date.now() + 60 * 60 * 1_000});
  gmail = google.gmail({
    version: "v1",
    auth,
    rootUrl: `${serviceURL.replace(/\/$/, "")}/`,
  });
} else {
  // Do not configure a Fabricate URL here. The official client must refresh
  // against its normal OAuth host, then call gmail.googleapis.com via FAB's
  // process-scoped proxy and CA settings.
  auth.setCredentials({
    access_token: "expired",
    expiry_date: Date.now() - 60_000,
    refresh_token: "fab-refresh-token",
  });
  gmail = google.gmail({version: "v1", auth});
}

const operations = {};
const before = await gmail.users.messages.list({userId: "me", maxResults: 50});
assert.ok(before.data.messages?.length, "expected readable messages in the Acme environment");
const beforeProfile = await gmail.users.getProfile({userId: "me"});
assert.equal(beforeProfile.data.messagesTotal, 12, "expected the Acme environment");
operations.read = "passed";

const raw = Buffer.from(
  [
    "From: qa@acme.test",
    "To: support@acme.test",
    "Subject: SDK conformance",
    "",
    "Sent through the official Google APIs Node client.",
  ].join("\r\n"),
).toString("base64url");
const sent = await gmail.users.messages.send({userId: "me", requestBody: {raw}});
assert.ok(sent.data.id, "send should return a Gmail message ID");
operations.send = "passed";

const afterProfile = await gmail.users.getProfile({userId: "me"});
assert.equal(afterProfile.data.messagesTotal, 13, "sent message should persist");
operations.persistence = "passed";

const modeReport = {
  messagesAfter: afterProfile.data.messagesTotal,
  messagesBefore: beforeProfile.data.messagesTotal,
  operations,
  status: "passed",
};
const reportPath = process.env.FAB_COMPATIBILITY_REPORT;
if (reportPath) {
  let report = {
    api: "Gmail API v1",
    environment: {label: "Acme Gmail", manifest: "environments/acme-gmail.yaml", messages: 12},
    integration: "gmail",
    modes: {},
    operationLabels: {
      persistence: "Confirm persistence",
      read: "Read mailbox",
      send: "Send message",
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
    report = JSON.parse(await readFile(reportPath, "utf8"));
  } catch (error) {
    if (error?.code !== "ENOENT") throw error;
  }
  report.modes[mode] = modeReport;
  report.testedAt = new Date().toISOString();
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
}

console.log(JSON.stringify({mode, ...modeReport, sentID: sent.data.id}));
