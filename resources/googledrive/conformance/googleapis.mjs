import assert from "node:assert/strict";
import {readFile, writeFile} from "node:fs/promises";

import {google} from "googleapis";

const [mode] = process.argv.slice(2);
if (mode !== "direct" && mode !== "proxy") {
  throw new Error("usage: node googleapis.mjs <direct|proxy>");
}

const token = process.env.FAB_GOOGLEDRIVE_TOKEN;
if (!token) {
  throw new Error("FAB_GOOGLEDRIVE_TOKEN is required");
}

const auth = new google.auth.OAuth2("fab-client", "fab-secret");
let drive;
if (mode === "direct") {
  const serviceURL = process.env.FAB_GOOGLEDRIVE_URL;
  if (!serviceURL) {
    throw new Error("FAB_GOOGLEDRIVE_URL is required in direct mode");
  }
  // google-auth-library refreshes within five minutes of expiry. Keep the
  // direct credential comfortably outside that window; proxy mode below is
  // deliberately expired to prove the synthetic refresh route.
  auth.setCredentials({access_token: token, expiry_date: Date.now() + 60 * 60 * 1_000});
  drive = google.drive({
    version: "v3",
    auth,
    rootUrl: `${serviceURL.replace(/\/$/, "")}/`,
  });
} else {
  // Do not configure a Fabricate URL here. The official client must refresh
  // against oauth2.googleapis.com, then call www.googleapis.com/drive/v3 via
  // FAB's process-scoped proxy and CA settings.
  auth.setCredentials({
    access_token: "expired",
    expiry_date: Date.now() - 60_000,
    refresh_token: "fab-refresh-token",
  });
  drive = google.drive({version: "v3", auth});
}

const operations = {};

// files.list the way the Drive quickstart does: partial-response fields and a
// page size.
const before = await drive.files.list({
  pageSize: 100,
  fields: "nextPageToken, files(id, name, mimeType, parents)",
});
assert.equal(before.data.files?.length, 13, "expected the 13 Acme Drive items");
assert.ok(
  before.data.files.some((file) => file.id === "file-inv-4812-northwind" && file.name === "INV-4812-northwind.pdf"),
  "expected INV-4812-northwind.pdf in the listing",
);

const folder = await drive.files.list({
  q: "'folder-finance-august-2026' in parents and trashed = false",
  orderBy: "name",
  fields: "files(id, name)",
});
assert.deepEqual(
  folder.data.files.map((file) => file.name),
  ["INV-4812-northwind.pdf", "razorpay-settlement-2026-08-25.csv", "shiprocket-ndr-10483.pdf"],
  "expected the August 2026 finance folder contents",
);

const settlement = await drive.files.get({
  fileId: "file-razorpay-settlement-2026-08-25",
  fields: "id, name, mimeType, description, parents, size, md5Checksum",
});
assert.equal(settlement.data.name, "razorpay-settlement-2026-08-25.csv");
assert.equal(settlement.data.mimeType, "text/csv");
assert.deepEqual(settlement.data.parents, ["folder-finance-august-2026"]);
for (const id of ["pay_Acme10482", "pay_Acme10483", "pay_Acme10484", "rfnd_Acme10484"]) {
  assert.ok(settlement.data.description.includes(id), `settlement description should mention ${id}`);
}
operations.read = "passed";

const created = await drive.files.create({
  requestBody: {
    name: "acme-drive-note.txt",
    mimeType: "text/plain",
    parents: ["folder-finance"],
    description: "Created through the official Google APIs Node client.",
  },
  fields: "id, name, parents",
});
assert.ok(created.data.id, "files.create should return a Drive file ID");
assert.equal(created.data.name, "acme-drive-note.txt");
assert.deepEqual(created.data.parents, ["folder-finance"]);
operations.write = "passed";

const updated = await drive.files.update({
  fileId: created.data.id,
  addParents: "folder-finance-august-2026",
  removeParents: "folder-finance",
  requestBody: {description: "Filed under August 2026 finance.", starred: true},
  fields: "id, parents, description, starred",
});
assert.deepEqual(updated.data.parents, ["folder-finance-august-2026"]);
assert.equal(updated.data.starred, true);
operations.update = "passed";

const again = await drive.files.get({fileId: created.data.id});
assert.equal(again.data.name, "acme-drive-note.txt");
assert.equal(again.data.description, "Filed under August 2026 finance.");
assert.deepEqual(again.data.parents, ["folder-finance-august-2026"]);
const after = await drive.files.list({pageSize: 100, fields: "files(id)"});
assert.equal(after.data.files.length, 14, "created file should persist");
assert.ok(after.data.files.some((file) => file.id === created.data.id));
operations.persistence = "passed";

const modeReport = {
  messagesAfter: after.data.files.length,
  messagesBefore: before.data.files.length,
  operations,
  status: "passed",
};
const reportPath = process.env.FAB_COMPATIBILITY_REPORT;
if (reportPath) {
  let report = {
    api: "Google Drive API v3",
    environment: {
      label: "Acme Google Drive",
      manifest: "environments/acme-googledrive.yaml",
      messages: 13,
    },
    integration: "googledrive",
    modes: {},
    operationLabels: {
      read: "Read Drive files",
      write: "Create file",
      update: "Move and update file",
      persistence: "Confirm persistence",
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

console.log(JSON.stringify({mode, ...modeReport, createdID: created.data.id}));
