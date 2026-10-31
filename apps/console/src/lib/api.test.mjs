import test from "node:test"
import assert from "node:assert/strict"
import { ApiError, getCatalog, getAuthSession, signIn, signOut } from "./api.ts"

test("unauthorized API errors stay structured even if an upstream returns plain text", async () => {
  const previous = globalThis.fetch,
    oldWindow = globalThis.window
  const browser = new EventTarget()
  globalThis.window = browser
  let expired = 0
  browser.addEventListener("fabricate:unauthorized", () => expired++)
  try {
    globalThis.fetch = async () =>
      new Response("control credential required", { status: 401 })
    await assert.rejects(
      getCatalog(),
      (error) =>
        error instanceof ApiError &&
        error.status === 401 &&
        !error.message.includes("Unexpected token")
    )
    assert.equal(expired, 1)
  } finally {
    globalThis.fetch = previous
    globalThis.window = oldWindow
  }
})
test("sign-in uses a same-origin session; credentials never appear in the URL", async () => {
  const previous = globalThis.fetch
  const calls = []
  try {
    globalThis.fetch = async (path, init) => {
      calls.push({ path, init })
      return new Response(
        JSON.stringify({ required: true, authenticated: true }),
        { status: 200 }
      )
    }
    const session = await signIn("synthetic-operator-token")
    assert.equal(session.authenticated, true)
    assert.equal(calls[0].path, "/api/auth/session")
    assert.equal(calls[0].init.credentials, "same-origin")
    assert.equal(calls[0].init.headers["content-type"], "application/json")
    assert.deepEqual(JSON.parse(calls[0].init.body), {
      token: "synthetic-operator-token",
    })
    assert.equal(calls[0].init.headers.Authorization, undefined)
  } finally {
    globalThis.fetch = previous
  }
})
test("session discovery and sign-out handle JSON and empty responses", async () => {
  const previous = globalThis.fetch
  try {
    globalThis.fetch = async () =>
      new Response(JSON.stringify({ required: true, authenticated: false }), {
        status: 200,
      })
    assert.equal((await getAuthSession()).authenticated, false)
    globalThis.fetch = async () => new Response(null, { status: 204 })
    assert.equal(await signOut(), undefined)
    globalThis.fetch = async () => new Response("not-json", { status: 200 })
    await assert.rejects(getCatalog(), /invalid response/)
  } finally {
    globalThis.fetch = previous
  }
})
