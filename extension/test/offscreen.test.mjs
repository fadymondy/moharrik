// MH-478: the socket's reconnect backoff (offscreen.js).
import { test } from "node:test"
import assert from "node:assert/strict"

import { backoffDelay } from "../offscreen.js"

test("backoff doubles from one second to a minute, and is jittered within its upper half", () => {
  const low = (a) => backoffDelay(a, () => 0)
  const high = (a) => backoffDelay(a, () => 1)
  assert.deepEqual([0, 1, 2, 3].map(high), [1000, 2000, 4000, 8000])
  assert.deepEqual([0, 1, 2, 3].map(low), [500, 1000, 2000, 4000])
  // Capped: a browser that was offline all night comes back within a minute.
  assert.equal(high(20), 60000)
  assert.equal(low(20), 30000)
  // Jitter: two browsers that lost the same network do not retry in lockstep.
  const seen = new Set(Array.from({ length: 50 }, () => Math.round(backoffDelay(4))))
  assert.ok(seen.size > 10)
})
