# UI conventions

The visual language comes from the prototype at `documentation/example/iac-generator-ui.html`.
That file is a complete, working, single-file version of the app and remains the reference for
look and behaviour. `web/static/app.css` is its stylesheet, ported with two changes: the
Google Fonts link was dropped (the font stack already had local fallbacks, and a localhost tool
should not need the network), and hardcoded per-provider colours were replaced by custom
properties.

## Colour tokens

Defined on `:root` in `app.css`:

| Token | Value | Used for |
|---|---|---|
| `--bg` | `#0A0D12` | canvas background |
| `--panel` | `#12161D` | account cards, top bar, drawer |
| `--panel-2` | `#171C24` | asset tiles, inputs, menus |
| `--border` | `#232B36` | standard borders |
| `--text` / `--text-dim` / `--text-faint` | | three levels of emphasis |
| `--accent` | `#59D9C7` | teal — internal connections, primary buttons, selection |
| `--danger` | `#E8574B` | red — internet ingress, delete actions |
| `--warning` | `#E3A73D` | orange — internet egress only |
| `--extip` | `#9C86E0` | purple — hardcoded external addresses |

Provider colours are **not** in the stylesheet. They arrive as `--p` and `--p-dim`, set per
provider by a `<style>` block that `internal/ui/style.go` generates from the catalog. Anything
provider-tinted reads those two properties. See `CATALOG.md`.

## Line semantics

This is the part that carries the actual meaning of the chart. Implemented in `lineStyle()` in
`app.js`:

| Appearance | Meaning |
|---|---|
| teal, solid, 1.6px | internal connection |
| **red, solid, 2.2px** | **internet ingress — the public internet can initiate inwards. The real attack surface.** |
| orange, dashed, 1.6px | internet egress only — the asset can reach out, nothing can come back |
| purple, dashed, 1.8px | involves a hardcoded external IP, which is never deployed |
| grey, dotted, 1.4px | connection drawn but no rules defined either way |

The distinction between red and orange is the single most important thing the UI communicates.
A connection to the internet node with rules only in the outbound direction is ordinary and
safe; the same line with an inbound rule means something is publicly reachable. Do not
collapse these into one colour, and do not let a change make a red line look incidental.

Each line carries a small chip at its midpoint showing direction (`→`, `←`, `↔`, or `–` for
none) and the total rule count, so an unconfigured connection is obvious without opening it.

## Zoom

`--` / `+` in the top bar, Ctrl+scroll (or a trackpad pinch) over the canvas, and clicking the
percentage resets to 100%. Range is 25%–150%.

This uses the CSS `zoom` property rather than `transform: scale()`. `zoom` affects layout, so
the scroll area shrinks with the chart; a transform would leave the scrollbars sized for a
2200×1500 canvas that is no longer that big on screen.

The cost is that three places have to convert between visual pixels and canvas pixels, because
`getBoundingClientRect()` and `event.clientX` report visual pixels while the SVG draws in canvas
pixels. All three divide by `zoom`: `pointFor()`, the drag deltas in `startDrag()`, and
`openTypeMenu()`. **Anything new that mixes client coordinates with canvas coordinates has to do
the same.**

## Positioning

Node positions travel as `data-x` / `data-y` attributes, not inline `style`. `applyPositions()`
in `app.js` converts them to `left`/`top` on insert, and `moveNode()` keeps both the attribute
and the style in step during a drag.

This exists so the server never has to emit CSS — templ sanitises style attributes, and
fighting that for two numbers is not worth it — and so one code path owns positioning.

## Interaction

- **Drag** an account by its header; drag the internet node or an external IP anywhere on its
  body. Drag is entirely local: no request is made, and lines redraw on each mousemove.
- **Connect** by clicking one connector dot and then another. Click-to-click rather than
  drag-to-drop, because connectors are small and a drag gesture would fight the drag handler
  on the node underneath. Clicking the same dot twice cancels. Connecting two nodes that are
  already connected selects the existing connection instead of creating a duplicate.

  Connector handling lives in the **click** handler, not `mousedown`. Doing it on `mousedown`
  looks equivalent but is not: the click that follows falls through to the node underneath and
  replaces the newly created connection in the selection, so the connection appears not to have
  been made at all. `onCanvasMouseDown` returns early for connectors purely to stop a drag
  starting.
- **Rename an account** by clicking its name in the card header and typing. It is a native
  `contenteditable="plaintext-only"` span, so there is no modal and no endpoint. Enter commits
  and blurs; the text is read back with newlines stripped and clamped to 200 characters, which
  is what the server enforces anyway. `.account-name` is excluded from the drag handler.
- **Pan** by dragging empty canvas. This scrolls the wrapper rather than moving anything, so
  nothing in the session changes and the delta needs no zoom conversion — scroll offsets are
  already in visual pixels. `startPan()` bails out on any `.account`, `.internet-node`,
  `.extip-node` or `.type-menu` ancestor, which covers cases no earlier branch claimed, such as
  the empty area inside an account card.
- **Select** by clicking an asset tile, an external IP, or a connection line. Selection opens
  the drawer. A drag of under 4px counts as a click.
- **Edit an account** with the gear button in the card header, which opens the account panel in
  the drawer: region, and the default SSH key for every host in the account. It is an explicit
  button rather than a click on the header, because the header is the drag handle — telling a
  click from the start of a drag needs a threshold, and a button needs none. `.account-settings`
  is excluded from the drag handler for the same reason as `.account-name`.
- **Delete** via the ✕ on a node, or the Delete button in the drawer. Deleting a node also
  drops any connection that referenced it — a dangling `NodeRef` would fail import validation
  later.

Events are delegated from `document` and `#canvas`, so fragments inserted from the server need
no wiring of their own. When adding new interactive markup, extend the delegated handlers
rather than binding to the element directly.

## Class contract

The server renders markup and the browser finds things in it by class and data attribute.
These names are the interface between the two — changing one means changing both.

| Selector | Meaning |
|---|---|
| `.account[data-account-id][data-provider]` | account card |
| `.asset[data-account-id][data-asset-id]` | asset tile |
| `.extip-node[data-extip-id]` | hardcoded address node |
| `.internet-node` | the public network node |
| `.connector[data-node]` | connection handle; `data-node` is the `NodeRef.Key()` string |
| `.account-settings` | the gear in the card header; opens the account panel |
| `.add-tile` | the ✛ tile that opens the type menu |
| `.type-menu[data-type-menu]` | pre-rendered asset picker, one per provider |
| `[data-conn-id]` | on SVG paths and chips; identifies the connection |
| `.rule-row[data-dir][data-index]` | one rule; `data-dir` is `aToB` or `bToA` |
| `[data-param]` | a parameter input in the drawer; the attribute is the catalog `ParamKey()` |

## Advanced options disclosure

A resource type's fields split into two groups. The few things someone picks when creating an asset
render immediately; everything marked `Advanced` in the catalog goes inside a native `<details>`
element with the count in its summary — "Advanced options (9)".

Native `<details>` rather than a tab or a JavaScript toggle: no state to keep in sync with a
server-rendered panel, and no new component. The arrow is a CSS `::before` on the summary, since the
default marker cannot be styled consistently.

**A field gated by a directive is not shown while the gate is off.** `RequiresParam` on a
`ParamField` means generation will drop the value, so offering the field would invite input that
goes nowhere. Toggling such a gate is the one param edit that re-renders the panel, since it changes
which fields exist — see `gatesOtherParams()` in `app.js`.

**Directives are never hidden.** `splitParams()` in `internal/ui/drawer.go` keeps any field with
`Directive: true` in the visible group even when it is marked `Advanced`. A directive changes what
gets generated — "Static public IP" is the current example, and it is what makes a cross-account
rule possible — so burying it would hide a real decision behind a disclosure.

Order within each group follows the catalog, which declares fields in the order they should be read.

## Validating from the modal

The generate modal carries a **Validate** button that runs the real tool server-side and reports the
verdict in a strip above the configuration: green for valid, red for anything else.

Two things about it are deliberate:

- **The configuration is regenerated on the server**, not taken from what is on screen. Validation
  executes provider plugins, so what runs must be something the process produced rather than
  whatever the browser posted.
- **The verdict is cleared when the modal reopens.** A green strip left over from a previous
  generation would be a verdict about different configuration.

The first run takes around 18 seconds because providers are downloaded; later runs are about 7,
because the working directory is reused rather than recreated. Runs are serialised by a mutex, since
they share that directory.

If neither `tofu` nor `terraform` is installed, the strip is red and says which to install — an
empty red panel would look like a validation failure rather than a missing tool.

### Copying the configuration

The **Copy** button beside it takes the text straight out of `#generate-output`, so it copies what
the user is looking at rather than asking the server to generate the configuration a second time —
the two could differ if the chart were edited in between.

Its label lives in a `[data-copy-label]` span so the confirmation swaps only the word, leaving the
icon in place. Clipboard access can be refused outright, and a silent no-op reads as a copy that
worked, so the failure path selects the whole `<pre>` and says `Press Ctrl+C` instead.

## Script fields

A `FieldScript` param renders as a monospace `<textarea>` rather than an input. Startup scripts are
the only multi-line values in the model, and a single-line input would silently drop everything after
the first newline when the browser posted it back.

## Pre-rendered chrome

The add-account panel, the add-IP panel, the asset type menus (one per provider) and the
generate modal are all rendered once into the page and toggled with a `hidden` class. They
carry no per-session data, so there is nothing to fetch and no endpoint for them. The type
menus in particular could have been an endpoint, but four providers of roughly seven types
each is small enough that pre-rendering all of them is cheaper than the request.
