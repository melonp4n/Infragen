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

## History — how `internal/tofu` was built

Kept because the reasoning is worth having; everything below is **done**. See `TOFU-MAPPING.md`
for the decisions in their settled form.

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

## Done — account settings, and addresses that are actually populated

Completed 2026-08-31, from two reports against the Ansible work.

**Inventory lines came out blank for EC2 hosts.** `aws_instance.public_ip` is an empty string on an
instance with no public address, and `associate_public_ip_address` defaults to `false` — so the
inventory named an attribute that resolved to nothing and the apply succeeded. The same class of bug
was latent on Azure and GCP, and worse there: the generated NIC never received a public IP, and
`google_compute_instance` had no `access_config` block at all, so the address expression indexed a
block that did not exist.

| Change | State |
|---|---|
| `ResourceType.AddressRequires`, and `hostAddress` refusing when the gate is off | **done** |
| Warning names the drawer field label, not the HCL argument | **done** |
| Static toggle attaches the address on Azure (`public_ip_address_id`) and GCP (`access_config.nat_ip`) | **done** |
| `companionResource` honours `Fixed.RequiresParam`, which it previously ignored | **done** |
| `everytype-static` integration case: every type with a `StaticAddr`, with one | **done** |

**An account could not be configured at all.** Region and SSH key existed only as Terraform
variables, so neither could be saved with a session.

| Change | State |
|---|---|
| `Provider.AccountParams`, `AccountSettings()`, `Provider.Region()` | **done** |
| `Account.Params`, normalised and coerced like an asset's | **done** |
| Account panel in the drawer, reached from a gear in the card header | **done** |
| Account key as a source in `sshKeyExpr`, ahead of both variables | **done** |
| Account region as the default of `var.<accountID>_region` | **done** |

The account key also suppresses the no-key precondition, which would otherwise be a check that can
only ever raise a false alarm. `defaultRegion()` is gone — it was a `switch` on provider inside
`internal/tofu`, which the catalog now owns.

Verified with `go test -tags integration ./internal/tofu` against real providers, and by hand:
setting a region and an account key in the drawer, generating, and confirming the region reaches the
variable default, the key becomes the first `coalesce` source for hosts in that account only, and an
Ansible EC2 moves from a warning to a populated inventory line when the public IP is turned on.

## Done — region-correct AMI selection for EC2

Plan: `~/.claude/plans/ultra-lucky-puzzle.md`, the increment at the end.

`aws_instance` defaulted to the literal `ami-0c55b159cbfafe1f0`, which is valid only in us-east-1.
Now the chart names an operating system and a `data "aws_ami"` lookup resolves the ID at plan time
in the account's region.

| Change | State |
|---|---|
| `RequiresValue` on `ParamField`, `Fixed` and `Companion`; `required()` takes a value | **done** |
| `Companion.Data`, emitting a `data` block rather than a `resource` | **done** |
| Five OS presets plus `Custom AMI ID`, one gated lookup each | **done** |
| `Never replace on a newer AMI` toggle emitting `ignore_changes = [ami]` | **done** |
| AMI ID box hidden unless `Custom AMI ID` is chosen | **done** |
| Warning when a gated field is opted into and left blank | **done** |
| Collision invariants sharpened to "can these gates both apply?" | **done** |
| `TestAMIFiltersResolve` — the check validate cannot do | **written, not yet run** |
| Public SSM parameters for four presets, after a name pattern failed at apply | **done** |

Two invariant tests failed when the presets landed, both correctly: five companions share the
suffix `ami`, and both a companion and an editable field write the `ami` argument. Rather than
loosen them, they now ask whether two writers' gates can ever both be satisfied — which still
catches the original bug and permits the mutually exclusive case. That is the more useful
invariant, and it was worth the detour.

**Amazon Linux failed in ap-southeast-2, exactly as the unverified filters risked.** A user hit
"Your query returned no results" at apply: `al2023-ami-2023.*-x86_64` matched nothing. The emitted
HCL was correct, so this was not a generation bug — the pattern itself was a guess about AWS's
naming, and there is no way to tell a good guess from a bad one without asking the API.

Rather than guess a second pattern, four of the five presets now resolve through the public SSM
parameters AWS and Canonical maintain, which point at the current image per region and assume
nothing about image names. Debian keeps a name match because Debian publishes no parameter.

**Still outstanding: nothing here can confirm the parameter paths resolve.** There is no AWS CLI or
credentials in the development environment, and `tofu validate` never executes a data source — it
proved `aws_ssm_parameter` and its `name` argument are real and nothing more. Run this wherever AWS
access exists:

```
go test -tags integration ./internal/tofu -run TestAMIFiltersResolve
```

It now checks both kinds — a parameter must exist *and* return something beginning `ami-`, a filter
must match at least one image. It skips silently without credentials, so a green run in an
environment without them means nothing.

Two known rough edges, both documented in `TOFU-MAPPING.md` and neither fixed: `Login user` still
defaults to `ec2-user` whatever the image, and nothing warns that Ansible over SSH cannot reach
Windows Server.

## Done — duplicate firewall rules refused by AWS

A user hit this at apply, on a chart that generated and validated cleanly:

```
InvalidPermission.Duplicate: the specified rule "peer: 0.0.0.0/0, TCP, from port: 80,
to port: 80, ALLOW" already exists
```

Rules were accumulated by appending, and `ruleName` includes an index, so two identical permissions
became two resources with different Terraform names and identical content. AWS accepts the first
and refuses the second, failing the apply partway through. `tofu validate` was never going to catch
it: both resources are valid alone and only collide at the API.

`dedupeRules` now collapses them at the single loop every provider's rules pass through. Identity is
the rule minus its comment; the comments are joined so a merged rule still reports both reasons.
Golden files did not change, which is the evidence that valid charts generate exactly as before.

Reproduced first, then fixed: `TestIdenticalRulesAreMergedNotRepeated` covers the collapse, and
`TestRulesDifferingOnlyByPortAreKept` guards against collapsing too much.

## Done — AWS subnets had no route to the internet

A user could not SSH to an EC2 instance on a chart that generated and validated cleanly.

`networkBlock` emitted a VPC, two subnets and an `aws_internet_gateway`, and **no route table
existed anywhere in the codebase**. Nothing routed `0.0.0.0/0` to that gateway, so both subnets
were private: an instance could hold a public IP and allow port 22 and still refuse connections,
because the reply had no way out. The gateway was allocated and attached to nothing.

The diagnosis ruled out the two plausible culprits before anything changed, and both are worth
keeping:

- **Security groups are stateful**, so a missing egress rule cannot break inbound SSH. The reply
  to an allowed inbound connection is permitted automatically.
- **No NACLs are emitted at all**, so AWS's default network ACL applies and allows everything.

| Change | State |
|---|---|
| `aws_route_table` with an inline default route to the gateway | **done** |
| `aws_route_table_association` for both subnets | **done** |
| `noPublicAddress` extracted from `hostAddress`, shared with the new warning | **done** |
| `unreachableWarnings` — inbound from outside the account to an asset with no address | **done** |

AWS is the only provider affected: Azure and GCP create default internet routes, and DigitalOcean
droplets get a public interface.

Network ACLs were considered and rejected — stateless, per-subnet against a per-asset chart, and
absent on GCP and DigitalOcean. The reasoning is in `TOFU-MAPPING.md` because "add ingress and
egress VPC rules" is the plausible wrong answer to an unreachable host. The single thing that
would justify revisiting is denying a specific CIDR, which security groups cannot express.

**What this does not prove.** `tofu validate` confirms `aws_route_table`, its nested `route` block
and `aws_route_table_association` are real arguments — it cannot confirm traffic flows. Only an
apply and a connection do that. The honest claim is that the missing route is now emitted.

## Done — cleanup pass, and six firewalls that named resources that did not exist

A fresh-eyes review of the whole codebase. The report is at
`~/.claude/plans/this-poc-is-going-luminous-hinton.md`.

**Three generation bugs, all found by one new fixture.** `everytype-firewalled` draws an inbound
connection to every `NetFirewalled` type, because the existing fixtures did not: `EveryType()` has
no connections at all, and `Seed()`'s eight never touch an Azure VM or a DigitalOcean managed
database — so the Azure and GCP firewall emitters were never exercised by the acceptance gate.

| Bug | State |
|---|---|
| Azure emitted a NIC association for every firewalled asset; only `VM` has a NIC. `SQL`, `AKS`, `APG` produced a dangling reference | **fixed** — `ResourceType.FirewallRef`, empty means nothing attaches |
| DigitalOcean hardcoded `droplet_ids`; `DB`, `K8S` and `LB` are not droplets | **fixed** — same mechanism |
| DigitalOcean emitted `protocol = "all"`, which the provider rejects at apply | **fixed** — an `ALL` rule becomes tcp, udp and icmp |
| `attachmentGaps()` kept a second copy of `attach()`'s type list, and switched on `TofuType` outside the two places the rule permits | **fixed** — `attached()` reads `attachArg()` |

Two UI bugs, both one line: a stray `</style>` in `app.css` had been swallowing
`.account-dot{background:var(--p)}` since the port from the prototype, so account dots rendered
unfilled; and `openTypeMenu` moved the menu into `#canvas`, where the next import destroyed it.

**Removed:** `report.go` (50 lines, `Report.Text()` had no callers), `resourceName()` (the identity
function), `textError` (a hand-rolled `errors.New`), `orDefault`, `Variable.Type` and `varDecl.Type`
(never anything but string), the nil-regions branch in `AccountSettings`, `nodeLabel()` in `app.js`,
three dead CSS rules, and the client-side param default seeding — which was keyed by `key` where Go
keys by `ParamKey()`, and which `Normalise` redoes on every round trip anyway.

**Single-sourced:** `catalog.GateOpen` (the drawer and the generator each had a copy, and the Go
comment said so), `producesRule` (the strategy partition was written twice, inverted, in two files),
`boolParam`, `dashed`, `ports`, `lowerProto`, `portOrAny`, and the `cidr:` prefix, which two of four
renderers stripped and two did not.

**Split:** `firewall.go` into one file per provider — the README's own to-do — and `emit.go` into
`emit.go`, `scaffold.go` and `warnings.go`. `emit.go` went from 782 lines to 528.

**Performance:** `updateLines()` now coalesces to one redraw per animation frame and reads every
connector position before writing any SVG. It previously ran on every mousemove, forcing a layout
recalculation per connection per event.

`inventory(s)` is computed once per generate rather than three times, which removed the `panic()`
that existed only to check the three results agreed — in a function called straight from an HTTP
handler with no recover.

**Not done: `app.js` is still one file.** Splitting it into modules means converting shared mutable
closure state — `state`, `selection`, `zoom`, `connectFrom`, `linesLayer` — into module state with
accessors, which is more code than it removes, and there is no JavaScript test suite to catch a
mistake. Worth doing with a browser open, as a design change rather than a cleanup.

## Done — every GCP region, and the project as an account setting

Two gaps in the GCP account panel.

**The region list held six of 47.** It now carries every region in Google's published cloud IP range
file (`https://www.gstatic.com/ipranges/cloud.json`, whose `scope` field is the region name), sorted
by code. Written from that published list rather than from memory, which is the discipline the AMI
presets settled on. `TestGCPRegionsResolve` compares the picker against `gcloud compute regions list`
and skips without credentials — the check `tofu validate` cannot do, because nothing in a provider
schema knows which region strings are real.

That file lists regions Google operates, not regions every account may deploy into; some need
allowlisting, the same caveat the opt-in AWS regions carry.

**There was nowhere to set the project.** `var.<account>_project` defaulted to `"my-project"` — the
region-specific-AMI problem again, a plausible value wrong for everyone. The project is now a text
field on the account and its value becomes the variable's default, so `-var` and `TF_VAR` still
override per apply. Blank keeps the old fallback and produces a warning naming the field.

| Change | State |
|---|---|
| `AccountSettings(regions, default, extra...)` — per-cloud settings between region and keys | **done** |
| `Provider.AccountParam(params, key)`; `Region` is now a wrapper on it | **done** |
| `Variable.DefaultFromParam`, honoured in `providerBlock` | **done** |
| `accountSettingWarnings` — blank param behind a placeholder default | **done** |
| `TestGCPProjectReachesTheVariable`, `TestBlankGCPProjectIsWarnedAbout` | **done** |
| `TestGCPRegionsResolve` | **written, skips without gcloud** |

Golden files did not move: a blank project resolves to the same `"my-project"` the variable always
defaulted to.

## Done — the inventory carries private addresses too

Reported against the GCP work: the generated inventory held only public IPs.

Every host now gets both. The public address stays the line Ansible connects to, because that is
where infrachart is being run from, and the private one sits commented directly beneath it — so
running Ansible from a bastion inside the network is uncommenting a line rather than looking an
address up.

```ini
# GCP Ops → Mythic C2
${google_compute_address.asset_3.address} ansible_user=ansible
# ${google_compute_instance.asset_3.network_interface[0].network_ip} ansible_user=ansible
```

**A host with no public address is now in the inventory instead of missing from it.** It used to be
dropped: the asset was marked Ansible-managed, the apply succeeded, and the host was simply absent.
The private address becomes the line itself, the comment says `(private address only)`, and the
warning says the inventory fell back to it.

| Change | State |
|---|---|
| `ResourceType.PrivateAddressAttr`, set on `EC2`, `VM`, `GCE`, `DRP` | **done** |
| `privateAddress()`; `inventoryHost.Private`; the commented line | **done** |
| A private-only host is included, labelled, and warned about | **done** |
| `TestInventoryCarriesBothAddresses`, `TestPrivateOnlyHostIsStillInTheInventory` | **done** |
| `ansible-private` integration fixture — proves all four attributes are real | **done** |

The attributes are ordinary Terraform references even inside a comment, because `#` means nothing to
HCL inside a heredoc — so `terraform validate` resolves them and a wrong name fails there. That is
the only check that could catch one.

## Done — a CDN line now wires the origin, on AWS and GCP

A line from a CDN to an asset used to produce a firewall rule and nothing else. Two charts looked
finished and served nothing:

- CloudFront's origin was pinned to `var.<asset>_origin_domain`, default `"origin.example.com"`.
  Drawing `CDN → EC2` opened the managed prefix list on the instance and still fetched from the
  placeholder.
- GCP's only CDN type was `google_compute_backend_bucket`, which fronts a bucket. Drawing
  `CDN → GCE` opened Google's health-check ranges on an instance with no load balancer behind
  them.

Both are the failure this project keeps meeting: a rule permits traffic, and something else has
to deliver it.

The classification half already existed — `StratEdgeNative` and `edgeConstruct()` were correct.
This is the resource half, in the same shape as the firewalls: `origin.go` holds the interface and
everything true of every cloud, `origin_aws.go` and `origin_gcp.go` hold the per-cloud wiring, and
`newOrigin(provider)` picks one.

**Ports are the chart's, not the generator's.** The port drawn on the line is the port the origin
is fetched on, and on GCP it has to reach three arguments at once — the instance group's
`named_port`, the backend service's `port_name`, and the health check's port. The load balancer's
own listening port is a separate field on the node, because listening on 443 and reaching the
application on 8080 is the ordinary case.

| Change | State |
|---|---|
| `catalog.Origin` + `ResourceType.Origin`, set on AWS `EC2`/`ALB`/`λ` and GCP `GCE` | **done** |
| `origin.go`, `origin_aws.go`, `origin_gcp.go` — interface, dispatch, shared refusals | **done** |
| GCP `GLB` type: backend service, URL map, proxy, global address, forwarding rule | **done** |
| `custom_origin_config` on CloudFront — required for every non-S3 origin, and absent until now | **done** |
| Lambda function URL: `ParamFunctionURL`, its companion, and the CloudFront access control that signs for `AWS_IAM` | **done** |
| `{{param:key}}` expansion, so a companion argument can be the user's to set | **done** |
| Refusals go through `classify.go`, so the drawer shows them on the rule row as it is typed | **done** |
| `originWarnings` — nothing drawn behind an edge, a line with no rule, a plaintext 443 | **done** |
| `origin_test.go`, and the `cdn-origins` integration fixture | **done** |

The fixture is the part that matters: `custom_origin_config`, `origin_access_control_id`,
`aws_lambda_permission.function_url_auth_type`, `google_compute_instance_group.named_port` and the
whole load-balancer chain are arguments nothing else in the repo emits, and `terraform validate`
against the real schemas is the only thing that proves they exist. One of its lines runs on 8080
so the drawn port is proved to reach every argument that must carry it.

Still open: Azure and DigitalOcean have no origin adapter, so an edge there behaves as before.
Cloud Run and Cloud Functions origins need a serverless NEG rather than an instance group — a
second branch in `origin_gcp.go`, noted in `TOFU-MAPPING.md`. The GCP proxy is
`target_http_proxy`; HTTPS needs a managed certificate, which needs a domain the chart cannot
know. And none of this has been applied against a real account yet — `validate` proves the
arguments are real and nothing about whether traffic actually arrives.

## Done — a broken chart is visible on the chart

Reported from a real deployment: a GCP load balancer was drawn, connected to a VM, and applied. It
was created with no backends.

The generator knew, and said so, and it did not matter. Three things stacked up:

- **The two rule directions are indistinguishable.** They render with the same size, weight,
  colour and button text, and which one is "A to B" depends on which connector was clicked first.
  The rule went in the other one.
- **The warning was wrong.** It read "has no rule on it" — about a line the user had just put a
  rule on. A message someone can see is false teaches them to skip the rest.
- **The warning was invisible.** Warnings were `#   ! …` lines inside the same `<pre>` as the HCL,
  in the same mono font and the same colour, so they read as generated Terraform comments.

| Change | State |
|---|---|
| `tofu.Warning` — severity plus the account, asset and connection it is about | **done** |
| All five reporters converted; `Generate` returns `[]Warning` | **done** |
| `originPair.reverse`, so a misplaced rule can be named instead of denied | **done** |
| `Session.Label` joins with `/`, so `→` only ever means direction | **done** |
| `OriginNote` + the drawer note on the direction that needs the rule | **done** |
| `.has-problem` / `.asset-alert` on the tile, fed by `POST /api/warnings` | **done** |
| `/api/generate` returns a fragment: warning panel first, code block second | **done** |
| Warning rows are clickable and select what they name; errors sort first | **done** |
| `TestRuleOnTheWrongDirectionSaysSo`, `TestNoRuleEitherWaySaysWhatToAdd`, `TestEveryOriginWarningNamesItsAsset` | **done** |

The message now reads: *the rule is on "Prod / web-01 → Prod / Edge LB", and an origin fetch runs
the other way — move it to "Prod / Edge LB → Prod / web-01", or no backend is generated.* Moving
the rule clears the badge, the note and the panel, and the output gains its
`google_compute_instance_group` and its `backend` block.

**The browser half is unverified.** The badge toggle, the debounced refresh and click-to-select
were checked by reading the code and by driving the endpoints with `curl`; no browser was
available in the build environment, so nothing has clicked them. `STATUS.md` has said the same
about the interaction layer since the beginning, and this change adds to that debt rather than
paying it off.

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
