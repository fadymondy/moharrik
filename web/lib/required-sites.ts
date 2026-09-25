/*
A workflow's required sites (MH-517), the way the server works them out
(internal/automation/required_sites.go RequiredHosts): its declared sites and
the written-out hosts of its navigation steps. A host that only a value makes
— goto {{url}} — is not in it; it is known when the run starts.

Kept in step with the server by the same rule, so the canvas can show the list
for steps that are not saved yet.
*/

const NAV = new Set(["goto", "navigate", "open"])

function normalise(value: string): string {
  let v = String(value ?? "")
    .trim()
    .toLowerCase()
  if (!v) return ""
  const scheme = v.indexOf("://")
  if (scheme >= 0) v = v.slice(scheme + 3)
  v = v.split(/[/?#]/)[0]
  const at = v.lastIndexOf("@")
  if (at >= 0) v = v.slice(at + 1)
  v = v.replace(/:\d+$/, "").replace(/\.$/, "")
  if (v.startsWith("[") && v.endsWith("]")) v = v.slice(1, -1)
  return /\s/.test(v) ? "" : v
}

export function requiredSites(
  declared: string[] | undefined,
  steps: { command: string; args?: Record<string, unknown> }[],
): string[] {
  const set = new Set<string>()
  for (const h of declared ?? []) {
    const n = normalise(h)
    if (n && !n.includes("{{")) set.add(n)
  }
  for (const s of steps) {
    if (!NAV.has(String(s.command).toLowerCase())) continue
    const n = normalise(String(s.args?.url ?? ""))
    if (n && !n.includes("{{")) set.add(n)
  }
  return [...set].sort()
}
