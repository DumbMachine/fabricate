// Acceptance gate for the Stripe httpmock, driven by Stripe's official
// Node SDK (`stripe` / stripe-node) — the client companies actually use.
//
// stripe-node does NOT take apiBase. Point it at a mock with host + port
// + protocol (http). Bodies are application/x-www-form-urlencoded, lists
// are { object: "list", data, has_more }. Until `fab create stripe` exists
// this test skips with a message; once the catalog ships it is a hard fail.
import test from "node:test";
import assert from "node:assert/strict";
import Stripe from "stripe";
import { preflight, createInstance, missingProfile } from "./lib/fab.mjs";

function stripeClient(inst) {
  const url = new URL(inst.url);
  return new Stripe(inst.token || "sk_test_fab", {
    host: url.hostname,
    port: Number(url.port || (url.protocol === "https:" ? 443 : 80)),
    protocol: url.protocol.replace(":", ""),
    timeout: 20_000,
    maxNetworkRetries: 0,
    telemetry: false,
  });
}

test("stripe mock ⇄ stripe-node", async (t) => {
  const skip =
    preflight() ||
    (missingProfile("stripe", "language-learning") &&
      missingProfile("stripe", "minimal"));
  if (skip) return t.skip(skip);

  // Prefer a story state; fall back to minimal if only that exists.
  let inst;
  try {
    inst = createInstance("stripe", "language-learning", "sdk-stripe");
  } catch {
    inst = createInstance("stripe", "minimal", "sdk-stripe");
  }
  try {
    const stripe = stripeClient(inst);

    await t.test("customers.list returns seeded customers", async () => {
      const list = await stripe.customers.list({ limit: 10 });
      assert.equal(list.object, "list");
      assert.ok(Array.isArray(list.data));
      assert.ok(list.data.length >= 1, "expected at least one seeded customer");
      assert.equal(list.data[0].object, "customer");
    });

    await t.test("products.list + prices.list are Stripe-shaped", async () => {
      const products = await stripe.products.list({ limit: 10 });
      assert.equal(products.object, "list");
      assert.ok(products.data.length >= 1);
      const prices = await stripe.prices.list({ limit: 10 });
      assert.equal(prices.object, "list");
      assert.ok(prices.data.length >= 1);
      assert.equal(prices.data[0].object, "price");
    });

    let created;
    await t.test("customers.create is a stateful write (form-encoded)", async () => {
      created = await stripe.customers.create({
        email: "sdk-conformance@fab.test",
        name: "SDK Conformance",
        metadata: { source: "e2e/sdk/stripe.test.mjs" },
      });
      assert.equal(created.object, "customer");
      assert.match(created.id, /^cus_/);
      assert.equal(created.email, "sdk-conformance@fab.test");
    });

    await t.test("customers.retrieve reflects the write", async () => {
      const got = await stripe.customers.retrieve(created.id);
      assert.equal(got.id, created.id);
      assert.equal(got.email, "sdk-conformance@fab.test");
    });
  } finally {
    inst?.destroy();
  }
});
