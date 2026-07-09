// Conformance: drive the `google-play` httpmock service with Google's
// OFFICIAL `googleapis` clients — androidpublisher v3 (reviews) and Play
// Developer Reporting v1beta1 (apps:search) — pointed at the mock via
// rootUrl. Proves reviews.list/get/reply and the stateful reply write
// match the real wire contract.
import test from "node:test";
import assert from "node:assert/strict";
import { google } from "googleapis";
import { preflight, createInstance, googleAuth } from "./lib/fab.mjs";

const PKG = "com.acme.shopping";

test("google-play mock ⇄ googleapis client", async (t) => {
  const skip = preflight();
  if (skip) return t.skip(skip);

  const inst = createInstance("google-play", "reviews-demo", "sdk-googleplay");
  try {
    const auth = googleAuth(google);
    const publisher = google.androidpublisher({ version: "v3", auth, rootUrl: inst.url + "/" });
    const reporting = google.playdeveloperreporting({ version: "v1beta1", auth, rootUrl: inst.url + "/" });

    await t.test("apps.search (Play Developer Reporting)", async () => {
      const { data } = await reporting.apps.search({ pageSize: 10 });
      assert.ok(data.apps.length >= 1);
      assert.ok(data.apps.some((a) => a.packageName === PKG));
    });

    let reviewId;
    await t.test("reviews.list", async () => {
      const { data } = await publisher.reviews.list({ packageName: PKG });
      assert.ok(data.reviews.length >= 1);
      reviewId = data.reviews[0].reviewId;
      assert.ok(reviewId);
    });

    await t.test("reviews.get returns the userComment envelope", async () => {
      const { data } = await publisher.reviews.get({ packageName: PKG, reviewId });
      const user = data.comments.find((c) => c.userComment)?.userComment;
      assert.ok(user, "expected a userComment");
      assert.ok(typeof user.starRating === "number");
    });

    // ---- stateful write ----

    await t.test("reviews.reply posts a developer reply (write)", async () => {
      const { data } = await publisher.reviews.reply({
        packageName: PKG,
        reviewId,
        requestBody: { replyText: "Thanks — fix shipping in v2." },
      });
      assert.match(data.result.replyText, /fix shipping/);
    });

    await t.test("reviews.get now carries the developerComment (re-read reflects the write)", async () => {
      const { data } = await publisher.reviews.get({ packageName: PKG, reviewId });
      const dev = data.comments.find((c) => c.developerComment)?.developerComment;
      assert.ok(dev, "expected a developerComment after reply");
      assert.match(dev.text, /fix shipping/);
    });
  } finally {
    inst.destroy();
  }
});
