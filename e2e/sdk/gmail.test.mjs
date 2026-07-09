// Conformance: drive the `gmail` httpmock service with Google's OFFICIAL
// `googleapis` client (the same package a real Gmail integration uses),
// pointed at the mock via rootUrl. Proves the mock matches the real wire
// contract for reads AND stateful writes — a regression here fails CI, so
// changes to services/gmail can't silently break real-client behaviour.
import test from "node:test";
import assert from "node:assert/strict";
import { google } from "googleapis";
import { preflight, createInstance, googleAuth } from "./lib/fab.mjs";

test("gmail mock ⇄ googleapis client", async (t) => {
  const skip = preflight();
  if (skip) return t.skip(skip);

  const inst = createInstance("gmail", "support-inbox", "sdk-gmail");
  try {
    const gmail = google.gmail({ version: "v1", auth: googleAuth(google), rootUrl: inst.url + "/" });

    await t.test("users.getProfile", async () => {
      const { data } = await gmail.users.getProfile({ userId: "me" });
      assert.equal(data.emailAddress, "support@acme.dev");
      assert.equal(data.messagesTotal, 10);
    });

    await t.test("messages.list honours the q filter (5 unread)", async () => {
      const { data } = await gmail.users.messages.list({ userId: "me", q: "is:unread" });
      assert.equal(data.resultSizeEstimate, 5);
      assert.ok(data.messages.length >= 1);
    });

    await t.test("messages.get decodes into the real message envelope", async () => {
      const { data } = await gmail.users.messages.get({ userId: "me", id: "msg-0002" });
      assert.ok(data.labelIds.includes("UNREAD"));
      const subject = data.payload.headers.find((h) => h.name === "Subject")?.value;
      assert.match(subject, /Charged twice/);
      // body is base64url in payload.data, exactly like the real API
      assert.ok(data.payload.body.data.length > 0);
    });

    await t.test("threads.get returns the 2-message double-charge thread", async () => {
      const { data } = await gmail.users.threads.get({ userId: "me", id: "thr-doublecharge" });
      assert.equal(data.messages.length, 2);
    });

    // ---- stateful writes: the reason this project exists ----

    await t.test("messages.modify clears UNREAD (write)", async () => {
      const { data } = await gmail.users.messages.modify({
        userId: "me",
        id: "msg-0002",
        requestBody: { removeLabelIds: ["UNREAD"] },
      });
      assert.ok(!data.labelIds.includes("UNREAD"));
    });

    await t.test("the unread backlog shrank 5 → 4 (re-read reflects the write)", async () => {
      const { data } = await gmail.users.messages.list({ userId: "me", q: "is:unread" });
      assert.equal(data.resultSizeEstimate, 4);
    });

    await t.test("messages.send appends to the thread 2 → 3 (write)", async () => {
      const raw = Buffer.from(
        "To: dana@northwind.io\r\nSubject: Re: Charged twice\r\n\r\nRefund on its way.",
      ).toString("base64url");
      const sent = await gmail.users.messages.send({
        userId: "me",
        requestBody: { raw, threadId: "thr-doublecharge" },
      });
      assert.equal(sent.data.threadId, "thr-doublecharge");
      assert.ok(sent.data.labelIds.includes("SENT"));

      const { data } = await gmail.users.threads.get({ userId: "me", id: "thr-doublecharge" });
      assert.equal(data.messages.length, 3);
    });
  } finally {
    inst.destroy();
  }
});
