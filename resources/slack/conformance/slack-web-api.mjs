import assert from "node:assert/strict";
import {readFile, writeFile} from "node:fs/promises";
import http from "node:http";

import {WebClient} from "@slack/web-api";

const [mode] = process.argv.slice(2);
if (mode !== "direct" && mode !== "proxy") {
  throw new Error("usage: node slack-web-api.mjs <direct|proxy>");
}

const token = process.env.FAB_SLACK_TOKEN;
if (!token) {
  throw new Error("FAB_SLACK_TOKEN is required");
}

const options = {
  // Fail fast instead of the default ten retries over thirty minutes.
  retryConfig: {retries: 0},
};
if (mode === "direct") {
  const serviceURL = process.env.FAB_SLACK_URL;
  if (!serviceURL) {
    throw new Error("FAB_SLACK_URL is required in direct mode");
  }
  options.slackApiUrl = `${serviceURL.replace(/\/$/, "")}/api/`;
} else {
  // Do not configure a Fabricate URL here. WebClient keeps its default
  // https://slack.com/api/ and uses Node's built-in fetch, which ignores
  // HTTPS_PROXY unless the app opts in. This is the documented Node opt-in
  // (equivalent to NODE_USE_ENV_PROXY=1); the Fabricate CA arrives through
  // NODE_EXTRA_CA_CERTS.
  if (!process.env.HTTPS_PROXY) {
    throw new Error("HTTPS_PROXY is required in proxy mode");
  }
  http.setGlobalProxyFromEnv();
}

const requested = [];
const baseFetch = globalThis.fetch;
options.fetch = (input, init) => {
  requested.push({method: init?.method ?? "GET", url: new URL(String(input)), body: init?.body});
  return baseFetch(input, init);
};

const client = new WebClient(token, options);
const operations = {};

const auth = await client.auth.test();
assert.equal(auth.ok, true);
assert.equal(auth.user, "val");
assert.equal(auth.user_id, "U-val");
assert.equal(auth.team_id, "T-acme");
if (mode === "proxy") {
  assert.equal(requested.at(-1).url.host, "slack.com", "auth.test stays on the production host");
} else {
  // A wrong token must surface as a Slack platform error the app can branch
  // on (error.data.error), not as an HTTP transport failure. The proxy always
  // injects the synthetic token, so only direct mode can present a bad one.
  const wrong = new WebClient("xoxb-wrong", {...options, fetch: undefined});
  await assert.rejects(wrong.auth.test(), (error) => {
    assert.equal(error.code, "slack_webapi_platform_error");
    assert.equal(error.data?.error, "invalid_auth");
    return true;
  });
}
operations.auth = "passed";

// paginate() follows response_metadata.next_cursor. limit=2 over the five
// public Acme channels takes three pages.
const pageStart = requested.length;
const channels = [];
let pages = 0;
for await (const page of client.paginate("conversations.list", {limit: 2})) {
  pages += 1;
  assert.equal(page.ok, true);
  channels.push(...page.channels);
}
const pageRequests = requested.slice(pageStart);
assert.equal(pages, 3, "limit=2 should take three pages");
assert.equal(pageRequests.length, 3);
assert.deepEqual(channels.map((channel) => channel.id), ["C-finance", "C-fulfillment", "C-hiring", "C-incidents", "C-support"]);
const all = await client.conversations.list();
assert.deepEqual(all.channels.map((channel) => channel.id), channels.map((channel) => channel.id), "paging preserves order");
assert.ok(!all.response_metadata?.next_cursor, "unpaged list has no next cursor");
operations.paginate = "passed";

const history = await client.conversations.history({channel: "C-fulfillment"});
assert.equal(history.ok, true);
assert.equal(history.messages.length, 2);
const ravi = history.messages.find((message) => message.user === "U-ravi");
const sam = history.messages.find((message) => message.user === "U-sam");
assert.match(ravi?.text ?? "", /#10482/);
assert.match(ravi?.text ?? "", /SR10482AWB/);
assert.match(sam?.text ?? "", /#10483/);
assert.ok(Number(history.messages[0].ts) > Number(history.messages[1].ts), "history is newest first");
const finance = await client.conversations.history({channel: "C-finance", limit: 1});
assert.equal(finance.messages[0].user, "U-aisha");
assert.match(finance.messages[0].text, /rfnd_Acme10484/);
assert.match(finance.messages[0].text, /INV-4812/);
operations.read = "passed";

const text = "Dock check for #10482, AWB SR10482AWB.";
const posted = await client.chat.postMessage({channel: "C-fulfillment", text});
assert.equal(posted.ok, true);
assert.equal(posted.channel, "C-fulfillment");
assert.ok(posted.ts, "chat.postMessage should return a message ts");
assert.equal(posted.message?.text, text);
operations.write = "passed";

const after = await client.conversations.history({channel: "C-fulfillment"});
assert.equal(after.messages.length, 3, "posted message should persist");
assert.equal(after.messages[0].ts, posted.ts, "posted message is newest");
assert.equal(after.messages[0].text, text);
assert.equal(after.messages[0].user, "U-val");
operations.persistence = "passed";

if (mode === "proxy") {
  for (const request of requested) {
    assert.equal(request.url.host, "slack.com", `${request.url.pathname} stays on the production host`);
    assert.equal(request.url.protocol, "https:");
  }
}

const modeReport = {
  messagesAfter: after.messages.length,
  messagesBefore: history.messages.length,
  operations,
  status: "passed",
};
const reportPath = process.env.FAB_COMPATIBILITY_REPORT;
if (reportPath) {
  let report = {
    api: "Slack Web API v2",
    environment: {label: "Acme Slack", manifest: "environments/acme-slack.yaml", messages: history.messages.length},
    integration: "slack",
    modes: {},
    operationLabels: {
      auth: "Test auth",
      paginate: "Page through channels",
      read: "Read channel history",
      write: "Post message",
      persistence: "Confirm posted message",
    },
    testedAt: new Date().toISOString(),
    testedCommit: process.env.FAB_COMPATIBILITY_COMMIT ?? "unknown",
    verification: {
      client: `@slack/web-api@${process.env.FAB_COMPATIBILITY_SDK_VERSION ?? "unknown"}`,
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

console.log(JSON.stringify({mode, ...modeReport, postedTS: posted.ts, pages}));
