// MH-1108: which workspace opens after sign-in.
import { test } from "node:test"
import assert from "node:assert/strict"
import { pickHomeWorkspace } from "./last-workspace.ts"

const list = [
  { slug: "acme", last_active_at: "2026-10-01T10:00:00Z" },
  { slug: "3x1", last_active_at: "2026-10-04T09:00:00Z" },
  { slug: "zeta", last_active_at: "2026-09-01T10:00:00Z" },
]

test("no workspaces opens nothing", () => {
  assert.equal(pickHomeWorkspace([], "acme"), null)
})

test("the workspace last opened on this device wins", () => {
  assert.equal(pickHomeWorkspace(list, "zeta"), "zeta")
})

test("a remembered workspace I left falls back to the most recently active", () => {
  assert.equal(pickHomeWorkspace(list, "gone"), "3x1")
  assert.equal(pickHomeWorkspace(list, null), "3x1")
})

test("without any activity dates the last workspace is opened", () => {
  assert.equal(pickHomeWorkspace([{ slug: "a" }, { slug: "b" }], null), "b")
})
