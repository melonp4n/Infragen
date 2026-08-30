# Data model

The Go structs in `internal/model/model.go` are the canonical schema. Exported JSON is exactly
those structs, and the browser's in-memory graph has the same shape and the same field names.
There is no separate wire format to keep in sync.

## Session

```jsonc
{
  "version": 1,
  "internet": { "x": 1020, "y": 40 },
  "accounts": [ ... ],
  "externalIps": [ ... ],
  "connections": [ ... ]
}
```

`version` is `model.SchemaVersion`. `Validate` accepts `0` (an older file written before the
field existed) or the current version, and rejects anything else rather than guessing.

The internet node always exists and is never deleted — only moved — so it is stored as a bare
position rather than as an entry in a list.

## Account

```jsonc
{
  "id": "acc_9f3c1a2b7e4d5061",
  "name": "AWS Account 1",
  "provider": "aws",
  "x": 60,
  "y": 120,
  "assets": [ ... ]
}
```

`provider` must be a key registered in the catalog: `aws`, `azure`, `gcp`, `digitalocean`.
`x`/`y` are canvas pixels — the position of the account card on the chart. Assets move with it.

## Asset

```jsonc
{
  "id": "asset_2c8b0d1e6a7f3924",
  "code": "EC2",
  "name": "Web tier EC2",
  "params": {
    "instance_type": "t3.micro",
    "ami": "ami-0c55b159cbfafe1f0",
    "key_name": "",
    "root_volume_gb": 20,
    "associate_public_ip_address": false
  }
}
```

`code` keys into the owning account's provider in the catalog. It is the badge shown on the
tile *and* the stored identity, so **a code must never change once shipped** — saved sessions
reference it. (Lambda's code is the literal glyph `λ`, inherited from the prototype. It is
valid JSON and renders the intended badge.)

`params` is keyed by `catalog.ParamField.Key` for that resource type. After `Normalise` it
contains exactly the declared keys, with values coerced to the declared types — nothing more.

**An asset has no x/y.** Position inside an account is its index in the `assets` array. Tiles
flow in a wrapping row inside the card, so a free position would fight the layout. Reordering,
if it is ever wanted, is a slice reorder.

## External IP

```jsonc
{
  "id": "extip_5d2e9c04a1b8f637",
  "label": "Office jumphost",
  "ip": "203.0.113.10/32",
  "x": 1180,
  "y": 40
}
```

A hardcoded address — a jump host, an allowlisted partner. **Nothing is ever deployed for it.**
Its only job is to supply a CIDR to the rules that reference it.

`ip` accepts a bare address or a prefix on input; `Normalise` rewrites it to masked prefix
form, so a bare `203.0.113.10` is stored as `203.0.113.10/32`. Generated rules therefore always
carry an explicit mask.

## Connection

```jsonc
{
  "id": "conn_a41f2b93c7d05e86",
  "a": { "type": "asset", "accountId": "acc_...", "assetId": "asset_..." },
  "b": { "type": "internet" },
  "aToB": [ { "protocol": "TCP", "port": "443", "detail": "OS + package updates" } ],
  "bToA": []
}
```

### Direction is the whole point

`aToB` is traffic **B will accept when A initiates it**. `bToA` is the reverse. An empty list
means that direction is blocked — which is the default, and is what makes the model useful:
you have to say what is allowed.

This is why the example above is safe. The web tier can reach the internet on 443 for package
updates, and `bToA` is empty, so the internet cannot initiate anything back. In the UI that
line is orange and dashed. The moment someone adds a rule to `bToA`, the line turns solid red,
because the public internet can now start a connection inwards.

### Not every connection becomes a firewall rule

A connection records intent. What it *generates* depends on the network kind of the resource at
each end, which the catalog declares:

- firewalled to firewalled — a real security-group, NSG, VPC-firewall or DO-firewall rule
- anything touching a service endpoint (S3, Lambda, Blob, Spaces, App Platform) — an IAM concern,
  not a firewall one. The port is irrelevant. `Lambda → S3 on 443` in the seed chart is this case
- a CDN to its origin — origin configuration plus a native prefix list or service tag, and across
  clouds no IP-based control exists at all

So `aToB` being non-empty does not imply a firewall rule will be produced. See
`TOFU-MAPPING.md`.

### NodeRef

An endpoint, discriminated by `type`:

| `type` | fields used | meaning |
|---|---|---|
| `internet` | — | the public network |
| `extip` | `id` | a hardcoded external address |
| `asset` | `accountId`, `assetId` | a deployable resource |

`NodeRef.Key()` produces a stable string (`internet`, `extip:<id>`, `asset:<acc>:<asset>`) used
to match a reference to its rendered DOM element and to detect duplicate connections. The
browser has an identical `nodeKey()` — the two must stay in step.

### Rule

```jsonc
{ "protocol": "TCP", "port": "443", "detail": "origin fetch" }
```

- `protocol` — one of `TCP`, `UDP`, `ICMP`, `ALL`.
- `port` — a port, an inclusive range like `8000-8080`, or a wildcard. Validated to 1–65535 with
  `lo <= hi`.

  **All ports** must be typed: `*`, `any` or `all`, normalised to `*`. **Blank is not a wildcard —
  it means unspecified.** A blank port is accepted while editing, so a half-written rule does not
  stop the drawer rendering, but it generates no rule and is reported in the warnings.

  That distinction is the whole point. Blank meaning "all ports" is fail-open: a rule someone
  abandoned mid-edit, or one where the field was cleared to retype, would silently open every port
  on the asset. Allowing everything has to be a deliberate keystroke.

  Port `0` is **not** a wildcard either — it is a real value in some protocols, so treating a typo
  as "everything" would be dangerous. Both rejection messages name `*` so the reader knows what to
  type.

  `parsePort` in `internal/model/validate.go` is the single definition of the format. Validation and
  generation both go through it: when each had its own copy they disagreed, and a value one rejected
  the other silently read as "all ports".
- `detail` — free text. **A value containing `/` is treated as a CIDR override** and replaces
  the address that would otherwise be derived from the peer node. Otherwise it is a note and is
  emitted as a comment. This overload comes from the prototype; if it ever causes confusion,
  split it into two fields rather than adding parsing rules.

## Validation and normalisation

`Validate` returns the first problem it finds; callers treat any error as a 400.

It rejects: unsupported version, unknown provider, unknown resource code, empty or duplicate
IDs, a `NodeRef` pointing at something that does not exist, a connection joining a node to
itself, unknown protocols, malformed or out-of-range ports, malformed addresses, control
characters or over-long text in any name, and counts beyond the sanity caps (200 accounts,
500 assets per account, 200 rules per connection).

`Normalise` then stamps the current version, coerces every param against its catalog field,
and rewrites external IPs to prefix form.

**Always call both, in that order, before generating configuration.** Params reach HCL; the
coercion in `Normalise` is what keeps arbitrary JSON out of it.

## Worked example

`model.Seed()` in `internal/model/seed.go` is a complete, valid session covering every node
type and every line colour. It is the fixture to reach for in tests and the fastest way to see
the shape of real data:

```
Internet ──443,80──▶ Edge CDN            red    (public ingress — the real attack surface)
Edge CDN ──443────▶ Public ALB           teal   (internal)
Public ALB ──8080─▶ Web tier EC2         teal
Web tier EC2 ─5432▶ Orders DB            teal
Web tier EC2 ─443─▶ Internet             orange (egress only — nothing can come back)
Image resize fn ─443▶ Uploads bucket     teal
Office jumphost ─22▶ Web tier EC2        purple (hardcoded IP, never deployed)
Office jumphost ─22▶ App droplet         purple
```
