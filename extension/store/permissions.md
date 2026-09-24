# Chrome Web Store: permission justifications (MH-484)

Paste each answer into the "Privacy practices" tab of the listing. The upload is built with
`node extension/build.mjs --store`, which leaves out the self-update `key` and `update_url` because
the store assigns both.

## Single purpose

Moharrik lets a business's Moharrik workspace run the browser tasks its members approved, such as
reading a page, filling a form or publishing a post. It runs them in this browser, only on the sites
the workspace lists for it, and shows each run on the Moharrik site.

## Permissions

| Permission | Why Moharrik needs it |
|---|---|
| `storage` | Keeps this browser's connection token, its name, the sites its workspace allows, the pause switch and the upload folder, so they survive a restart. Nothing is synced to other devices. |
| `tabs` | Opens each run in its own tab in a "Moharrik" window, reads that tab's address to check it is still on an allowed site, and closes it when the run ends. |
| `scripting` | Runs a step's action (click, type, read text) inside the page of the run's own tab. It is injected only into a page whose site the workspace allowed. |
| `alarms` | Wakes the extension once a minute to keep the connection to the Moharrik site alive, and every six hours to check for a new version. |
| `webNavigation` | Sees where a run's tab actually lands after each navigation or redirect. A run that leaves the allowed sites is stopped at once. |
| `offscreen` | Holds the connection to the Moharrik site in an offscreen document, because Chrome stops an idle service worker and would drop it. |
| `debugger` | Sends clicks and key presses as real input to the run's own tab, which many sites require before they react. Chrome shows its "is debugging this browser" banner on that tab while a run is going, and it closes when the run ends. It is never attached to any other tab. |
| `tabGroups` | Groups the tabs of runs that are in progress, so the person at the browser can see which tabs Moharrik is using. |

## Host permissions

| Host | Why |
|---|---|
| `https://app.moharrik.com/*` (required) | The Moharrik site: connecting the browser, and the connection that carries the steps to run. |
| `http://*/*`, `https://*/*` (optional) | Asked for once, when the person presses "Connect to the site", so the sites the workspace lists for this browser work without a second prompt for each one. Declining is supported: each site then waits for the person's OK in the extension. Either way the extension only acts on the sites the workspace lists. The server and the extension both check every step against that list, and a run that leaves it is stopped. |

## Remote code

No. All the extension's code is in the package. The Moharrik site sends only data: which step to
run, on which site, with which values.

## Data use

- **Collected**: the connection token the Moharrik site issued, and what a run reads from a page
  (for example a list of headlines) when the workflow asks for it. The run's results are sent to the
  person's own Moharrik workspace, which is where the task was defined.
- **Not collected**: browsing history, other tabs, passwords typed into pages (the recorder never
  records password characters, and a typing step's text is not echoed back).
- **Not sold, not used for advertising, not used for credit decisions.**

Certify the three standard statements on the form: data is not sold to third parties, not used or
transferred for purposes unrelated to the single purpose, and not used to determine
creditworthiness.
