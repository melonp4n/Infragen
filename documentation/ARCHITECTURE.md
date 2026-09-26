# Architecture

## What this is

infrachart is a localhost Go web app for drawing short-to-medium-lived infrastructure as a
chart, and generating OpenTofu from that chart. Accounts are containers, assets are nodes
inside them, and the lines between nodes are the allowed traffic — which become security
group rules.

The point of the visual form is reachability. Text HCL hides which asset can reach which,
and hides what the public internet can reach at all. The chart shows it: internet ingress is
drawn in red, egress-only in orange.

## The one architectural decision that explains everything else

**The server is stateless. The browser owns the chart. All node markup comes from the server.**

Those three things sound contradictory, so here is how they fit:

- While you are editing, the live graph is a JavaScript object in the browser. Dragging a node
  200 times a second never touches the network.
- But JavaScript never *builds* node HTML. When you add an asset, the browser posts a
  descriptor to the server and inserts the HTML fragment that comes back.
- The server stores nothing between requests. It renders fragments, validates imported
  sessions and generates configuration. A session leaves as a JSON file and returns the same
  way.

The reason for the middle rule: if the browser built node markup too, every component would
exist twice — once in `.templ` and once as JavaScript string concatenation — and the two
copies would drift. One renderer, one place to change markup.

The cost is a round-trip when adding a node or opening the drawer. On localhost this is
imperceptible, and it never happens during a drag.

## Request flow

```
GET  /                     full page; the session is embedded as JSON in <script id="session-data">
GET  /static/*             app.css, app.js (embedded in the binary via go:embed)
GET  /api/catalog          provider table as JSON — lets the browser compute param defaults
POST /api/render/account   mints an account ID, returns the card
POST /api/render/asset     mints an asset ID, returns the tile
POST /api/render/extip     validates the address, mints an ID, returns the node
POST /api/render/canvas    whole chart — this is how import works
POST /api/render/drawer    edit panel for the current selection
POST /api/generate         session in, OpenTofu out
POST /api/validate         session in, `init` + `validate` verdict out
```

### IDs

The server mints IDs for accounts, assets and external IPs (`crypto/rand`, 8 bytes hex).
The browser reads the new ID back off the fragment it just inserted — the ID is already in
`data-asset-id` and friends, so no extra plumbing is needed.

Connection IDs are minted in the browser, because connections have no server-rendered markup.
They use `crypto.randomUUID()` for the same reason the server uses random bytes: two sessions
merged by hand must not collide.

IDs survive export and re-import. Keep it that way — see the drift note in `STATUS.md`.

### Import

Import is deliberately the same code path as first load:

1. Browser reads the file and posts the parsed JSON to `/api/render/canvas`.
2. Server runs `model.Validate` (rejects with 400 and a readable message) then
   `model.Normalise`.
3. Server returns the whole canvas, **including a fresh `<script id="session-data">`**.
4. Browser replaces `#canvas`, re-reads the JSON, redraws the lines.

Because the normalised session travels back inside the markup, the browser never has to
replicate normalisation rules.

### Export

No endpoint. The browser serialises its own state to a Blob and downloads it.

## Trust boundary

Every POST body is untrusted — it comes from the browser, and on import it comes from a file
the user was handed by someone else.

- Bodies are capped at 4 MB and decoded with `DisallowUnknownFields`, so a typo in a
  hand-edited session surfaces as an error rather than a silent default.
- `model.Validate` rejects unknown providers, unknown resource codes, duplicate IDs, dangling
  `NodeRef`s, self-connections, malformed ports and malformed addresses.
- `model.Normalise` then coerces every asset param to the type the catalog declares: unknown
  keys dropped, missing keys filled from defaults, values type-converted.

That normalisation step is not tidiness. Params end up inside generated HCL, so it is what
stops arbitrary JSON reaching the configuration writer. **Run `Validate` then `Normalise`
before generating anything.**

The server binds to `127.0.0.1` by default.

## Package layout

```
main.go                          flags, routes, ID minting, request decoding
internal/model/model.go          Session, Account, Asset, ExternalIP, Connection, Rule
internal/model/validate.go       Validate + Normalise — the untrusted-input boundary
internal/model/seed.go           the demo chart, shared by `-seed` and tests
internal/catalog/catalog.go      Provider/ResourceType/ParamField + registry
internal/catalog/{aws,azure,gcp,digitalocean}.go   one file per provider
internal/tofu/                   HCL generation (phase 6)
internal/ui/page.templ           document shell, topbar, static panels, type menus
internal/ui/node.templ           account card, asset tile, internet node, external IP node
internal/ui/drawer.templ         connection rules, account settings, asset params, external IP panel
internal/ui/drawer.go            drawer helper functions (exposure classification, formatting)
internal/ui/style.go             per-provider CSS custom properties, built from the catalog
web/static/app.js                drag, connectors, line drawing, selection, state
web/static/app.css               ported from the prototype
```

## Running it

```
go tool templ generate && go run . -seed      # http://127.0.0.1:8080
```

`templ` is pinned as a module tool in `go.mod` (`go get -tool github.com/a-h/templ/cmd/templ`),
so `go tool templ` resolves it from the module cache. Nothing needs installing globally, and
`~/go/bin` does not need to be on your PATH.

`-seed` loads the demonstration chart from `model.Seed()` — a public CDN in front of a load
balancer and web tier, a private database, a second account doing image processing, and an
office jump host with SSH access. It exercises every node type and every line colour, which
makes it the fastest way to check a change did not break rendering.

`go tool templ generate` must be re-run after editing any `.templ` file. The `_templ.go` files
it produces are build artifacts, and are committed so a clean checkout builds without a
generate step.
