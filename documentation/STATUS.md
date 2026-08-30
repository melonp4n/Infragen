# Status

Where the build has got to, so a new session can pick up without re-reading everything.
**Update this file when a phase lands.**

## Done

- **Model** — `internal/model`. Session/Account/Asset/ExternalIP/Connection/Rule, plus
  `Validate` and `Normalise` (the untrusted-input boundary) and `Seed()` (the demo chart).
- **Catalog** — all four providers registered with their resource types, parameter schemas and
  OpenTofu type names.
- **Server** — `main.go`. Static file serving from `go:embed`, the page, `/api/catalog`, and
  the fragment endpoints for account, asset, external IP, canvas and drawer. Binds to
  `127.0.0.1`.
- **templ components** — page shell and top bar, account card, asset tile, internet node,
  external IP node, pre-rendered add panels and per-provider type menus, and the three drawer
  panels (connection rules, catalog-driven asset params, external IP).
- **Stylesheet** — ported from the prototype, with provider colours moved to catalog-generated
  custom properties, plus the drawer flag classes.
- **Canvas** — `web/static/app.js`. Dragging, click-to-click connecting, SVG line drawing with
  the full colour semantics, selection, adding and removing nodes through fragment endpoints,
  and JSON export/import.
- **Drawer wiring** — selection requests the panel from the server and inserts it; field edits
  write into `state` and redraw lines without re-rendering (so focus survives typing); adding
  or removing a rule re-requests the panel, because that changes its structure.
- **Pan** — drag empty canvas to scroll the view.
- **Zoom** — 25%–150% via top bar buttons, Ctrl+scroll and click-to-reset. See `UI.md` for the
  visual-versus-canvas pixel conversion it requires.
- **In-place account rename** — `contenteditable` on the card header.
- **Tests** — `internal/model`. Seed validity, a lossless JSON round-trip, sixteen rejection
  cases, param coercion, address masking, and a guard that no slice marshals to `null`.

### Fixed after first run-through

- **Nil slices marshalled as JSON `null`.** `Seed()` passes `nil` for every empty rule list, so
  `session-data` shipped `"bToA": null`. `updateLines()` then threw on the first internet
  connection at boot, which killed line drawing and every subsequent `select()` before it could
  reach the drawer — so connections looked like they were never created and node clicks looked
  like they did nothing. `Normalise` now guarantees every slice is non-nil, the index handler
  calls it, and `TestNormaliseLeavesNoNullSlices` guards it. **Any new session field that is a
  slice must be covered there too.**
- **Connector clicks were handled on `mousedown`**, so the following click fell through to the
  node underneath. Moved to the click handler.

### Verified

- `go build ./...`, `go vet ./...` and `go test ./...` are clean.
- `-seed` renders four accounts, nine assets, eight connections, one external IP and eleven
  connectors, with the session JSON embedded for the browser to pick up.
- All four drawer panels render with correct values and correct exposure classification: the
  Internet→CDN connection reports "publicly reachable", and EC2→Internet reports "outbound
  only".

## Not done

- **`/api/generate` and `internal/tofu`.** The Generate config button has no handler and no
  generator exists. **The design is settled** — see `TOFU-MAPPING.md`, which now records the
  decisions rather than an open question. Remaining steps, in order:

  1. ~~Add the network classification fields to `catalog.ResourceType` and set them across the
     four provider files.~~ **Done.** `Network`, `AddressAttr`, `AddressKind` and `StaticAddr`,
     guarded by `TestEveryResourceTypeIsClassified`. `AddressKind` replaced the planned
     `StableAddress bool`, which could not express "hostname only".
  2. ~~Add the `static_public_ip` param to firewalled types whose address is ephemeral.~~
     **Done.** `catalog.StaticAddressToggle()` on AWS `EC2`, Azure `VM` and GCP `GCE`. It is marked
     `Directive: true`, so `ResourceType.Arguments()` keeps it out of generated HCL — emitters must
     use that rather than `Params`.
  3. **Classifier done, emitters not.** `internal/tofu/classify.go` resolves every connection into
     directional flows and decides what each rule becomes — every row of the strategy table, with
     twelve tests covering them. `internetTraffic()` moved to `model.Connection.InternetTraffic()`
     so `ui` and `tofu` share one definition of internet exposure.

     Two things learned while building it, both now enforced by tests:
     - `Report.Warnings()` returns only outcomes where `NeedsAction()` is true — refusals and
       cross-cloud CDN edges. IAM-governed and public-by-design outcomes are explained inline
       instead. Listing those as warnings trains the user to ignore the list, and then the real
       refusals go unread.
     - A `Detail` field containing a CIDR overrides everything, including a refusal. That is the
       documented escape hatch, and it is checked before any other case.
  4. **Emitters done.** `internal/tofu/{hcl,emit,firewall}.go` render the whole configuration:
     provider blocks aliased per account, network scaffolding, resource blocks, static address
     resources, and firewall rules in each provider's own model. `POST /api/generate` returns it
     with warnings at the top, and the Generate config button shows it.

     Verified: `terraform fmt -check` passes on the output, so it parses and is already canonical
     — `TestGeneratedHCLIsValidAndFormatted` runs that in CI when a binary is present.
     `TestRenameDoesNotMoveResources` and `TestSwapTouchesOnlyTheSwappedAsset` guard the
     repeated-apply constraints. `TestGuardedAssetsAreAttached` guards the one that would have
     shipped silently: security groups were being created but never attached to the instances,
     making every rule decorative.

     Things worth knowing before changing it:
     - **`writer.arg()` buffers, `writer.line()` flushes.** Arguments are aligned as a run, which
       is what makes `tofu fmt` a no-op. Use `arg()` for arguments, never `line()`.
     - **`quote()` escapes `${` to `$${`.** Display names and notes are user input, and an
       unescaped `${...}` would be evaluated as an interpolation.
     - **The `firewall` interface accumulates then renders**, because DigitalOcean puts inbound
       and outbound lists inside one resource. Do not flatten it to one-block-per-rule.
     - Resource bodies carry only the params the chart models, so `tofu validate` will name
       missing required arguments for some types — Azure VMs need a NIC, an os_disk block and an
       image reference that no chart describes. Attachment is wired for AWS, GCP and DigitalOcean;
       Azure NSG association needs a NIC or subnet and is reported as a gap in the output.

  5. **Drawer flags done.** Each rule row shows what it will actually generate, styled by whether
     it needs action (amber) or is merely worth explaining (grey) — `Outcome.NeedsAction()` decides.
     So `Lambda → S3 on 443` says "governed by IAM, the port is irrelevant" as you write it, and a
     cross-cloud rule against an ephemeral address says "No rule will be generated" with the fix.

  The four decisions that shape all of it: one configuration with provider aliases; non-firewalled
  assets never get firewall rules; CDN edges use native controls or warn; and resource addresses
  derive from `Asset.ID` so a rename cannot destroy infrastructure.
- **Browser verification.** Everything above was checked by driving the HTTP endpoints and
  inspecting the served HTML. No browser was available in the build environment, so drag,
  click-to-click connecting, line drawing and the drawer have **not been exercised
  interactively**. Run `go run . -seed` and click through the seed chart before trusting the
  interaction layer.

## Done — generated OpenTofu passes `tofu validate`

Completed 2026-08-29. Plan: `~/.claude/plans/what-options-do-we-sequential-squid.md`.

The output parses and the firewall layer is correct, but resource bodies carry only the parameters a
chart models, and **fifteen of those parameters are not real arguments** — they were written from
memory rather than from provider schemas. See `INVALID-PARAMS.md`, which is the rollback record and
also the progress marker: a row still unstruck means that provider file has not been rewritten.

Steps, in order. Each lands with its own documentation edit before the next begins.

| Step | State |
|---|---|
| 0. Document decisions, required arguments, rollback record | **done** |
| 1. `ParamField.Block` / `.Advanced`, `Fixed`, `Companion` | **done** |
| 2. Drawer `<details>` disclosure for Advanced fields | **done** |
| 3. Emitter: block grouping, fixed arguments, companions, sensitive variables | **done** |
| 4a. Rewrite AWS catalog | **done** |
| 4b. Rewrite Azure catalog | **done** |
| 4c. Rewrite GCP catalog | **done** |
| 4d. Rewrite DigitalOcean catalog | **done** |
| 5. Close the deferred items | **done** |

Step 1 note: the writer needed **no** nested-block support after all. `block()` already flushes
buffered arguments before opening, so nesting and per-run alignment fall out of what was there.
`TestWriterNestsBlocks` records that, so the machinery the plan called for does not get added later.

The other step-1 finding: `Asset.Params` had to be re-keyed by `ParamKey()` (`Block + "." + Key`),
because two fields can share a leaf name in different blocks and one would have silently overwritten
the other.

Step 3 note: `Fixed` and companion expressions are static catalog strings that need to name
account-scoped resources, which the plan had not accounted for. Resolved with three placeholders —
`{{account}}`, `{{asset}}`, `{{region}}` — expanded at emit time, so companions stay a data edit
rather than becoming emitter code. See the placeholder table in `TOFU-MAPPING.md`.

**Acceptance gate: passing.** `terraform init && terraform validate` succeeds on a chart containing
every one of the 26 types, and on the seed chart, which unlike `EveryType` has connections and so
exercises the firewall rules and their attachments. Run it with:

```
go test -tags integration ./internal/tofu
```

Behind a build tag because it downloads providers and takes about 35 seconds. `terraform fmt`
passing proves only that the syntax is well formed — it says nothing about whether an argument
exists, which is exactly how the invalid parameters went unnoticed for so long.

Two invariants the rewrite must not break, both already covered by tests:

- `TestRenameDoesNotMoveResources` and `TestSwapTouchesOnlyTheSwappedAsset`. Companions are named
  `<assetID>_<suffix>` precisely so they inherit rename stability.
- `internal/tofu` gets no new `switch` on a specific `TofuType`. If a type seems to need one, the
  catalog model is missing something.

## Done — Ansible and user data

Completed 2026-08-31. Plan: `~/.claude/plans/what-options-do-we-sequential-squid.md`.

Adds a startup script to the four compute types, and an Ansible toggle that produces an inventory at
apply time.

| Step | State |
|---|---|
| 0. Document the ephemeral SSH key investigation | **done** |
| 1. `FieldScript`, `ParamField.Wrap`, newline escaping, textarea | **done** |
| 2. User data on `EC2`, `VM`, `GCE`, `DRP` | **done** |
| 3. Ansible toggles, key resolution, warnings | **done** |
| 4. Inventory, `.gitignore`, feature-driven provider requirements | **done** |

The settled position worth not relitigating: **nothing generates an SSH key.** The reasoning, and
the three experiments that ruled out ephemeral resources, are in `TOFU-MAPPING.md`.

Two things to watch while building it:

- **The inventory is written by Terraform, not by infrachart.** It needs real IPs, which do not exist
  until after apply, so it is a `local_file` resource whose content interpolates address attributes.
- **`quote()` does not escape newlines**, and a literal newline in an HCL string is a parse error.
  Nothing hit that until user data, because validation rejected newlines everywhere.

## Resolved — the two deferred items

Both are closed by the required-argument work, and both `ponytail:` markers are gone from the
source. Recorded here because the reasoning is worth keeping.

**Incomplete resource bodies.** Required arguments a chart does not model now come from
`ResourceType.Fixed` and `Companions`, so every type validates. What a chart genuinely cannot know —
a Lambda deployment package path, a CDN origin host, an SSH public key — is a `Variable` instead,
sensitive where it is a secret.

**Azure firewall attachment.** Azure attaches a security group to a network interface, not to a
machine, and a chart describes no NIC. The `azurerm_network_interface` companion added for the VM's
required `network_interface_ids` is exactly what that association needs, so the Azure firewall
emitter now writes an `azurerm_network_interface_security_group_association` alongside each NSG.

`attachmentGaps()` stays. It currently reports nothing, which is the point — it names anything a
future type leaves unwired rather than letting the output look complete.

## Deliberately out of scope

Undo/redo, canvas zoom and pan, multi-user editing, authentication, a database, server-side
session storage, and per-resource-type templ components. None are needed to build and export a
chart, and each can be added later without reworking the model.

## Known future direction — drift and monitoring

Eventually a session should be checkable against live infrastructure: query the real accounts,
compare what exists to what the chart declares, and surface drift on the diagram — an asset
present in the cloud but not on the chart, a security group rule wider than the connection that
generated it, an asset that has quietly gained a public IP.

Nothing built so far blocks this. Two properties it will depend on already hold, so **preserve
them**:

- **IDs are stable across export and re-import.** Drift reporting needs a durable handle to map
  a cloud resource back to a chart node.
- **A session is fully described by its JSON.** A drift checker can read a session file and
  know exactly what was intended, without running the UI.

The one thing that would have to change is the stateless server — continuous monitoring implies
stored sessions and a background poller. That is a deliberate later decision, and not a reason
to add a session store now.
