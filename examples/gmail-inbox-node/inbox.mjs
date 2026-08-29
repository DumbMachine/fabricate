import {google} from "googleapis";

const USAGE = `gmail-inbox: run this app under Fabricate.

  fab run acme-gmail -- node inbox.mjs
  fab run acme-gmail --proxy -- node inbox.mjs

Optional search: node inbox.mjs INV-4812
`;

function env(...names) {
  for (const name of names) {
    const value = process.env[name];
    if (value) return value;
  }
  return undefined;
}

function usingProxy() {
  return Boolean(env("HTTPS_PROXY", "https_proxy"));
}

function gmailClient() {
  const auth = new google.auth.OAuth2("fab-client", "fab-secret");
  if (usingProxy()) {
    // Official client keeps oauth2.googleapis.com and gmail.googleapis.com.
    // Fabricate answers the refresh grant and the Gmail calls locally.
    auth.setCredentials({
      access_token: "expired",
      expiry_date: Date.now() - 60_000,
      refresh_token: "fab-refresh-token",
    });
    return google.gmail({version: "v1", auth});
  }
  const token = env("FAB_SUPPORT_MAIL_TOKEN", "FAB_GMAIL_TOKEN");
  const serviceURL = env("FAB_SUPPORT_MAIL_URL", "FAB_GMAIL_URL");
  if (!token || !serviceURL) {
    console.error(USAGE);
    process.exit(1);
  }
  auth.setCredentials({
    access_token: token,
    expiry_date: Date.now() + 60 * 60 * 1_000,
  });
  return google.gmail({
    version: "v1",
    auth,
    rootUrl: `${serviceURL.replace(/\/$/, "")}/`,
  });
}

function header(message, name) {
  const headers = message.data?.payload?.headers ?? [];
  const match = headers.find((item) => item.name?.toLowerCase() === name.toLowerCase());
  return match?.value ?? "";
}

const query = process.argv.slice(2).join(" ").trim();
const gmail = gmailClient();
const profile = await gmail.users.getProfile({userId: "me"});
const listed = await gmail.users.messages.list({
  userId: "me",
  maxResults: 8,
  ...(query ? {q: query} : {}),
});

const email = profile.data.emailAddress ?? "unknown";
const total = profile.data.messagesTotal ?? "?";
const estimate = listed.data.resultSizeEstimate ?? 0;
const messages = listed.data.messages ?? [];
let title = `${email}  ·  ${total} messages`;
if (query) title += `  ·  search ${JSON.stringify(query)} (${estimate})`;
console.log(title);
console.log();
if (messages.length === 0) {
  console.log("  (no messages)");
  process.exit(0);
}
for (const item of messages) {
  const message = await gmail.users.messages.get({userId: "me", id: item.id});
  const subject = header(message, "Subject") || "(no subject)";
  const sender = header(message, "From") || "(unknown sender)";
  const snippet = (message.data.snippet ?? "").trim();
  console.log(`  ${subject}`);
  console.log(`    ${sender}`);
  if (snippet) console.log(`    ${snippet}`);
  console.log();
}
