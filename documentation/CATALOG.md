# The catalog

`internal/catalog` is the table of providers, the resource types each one offers, and the
parameters each type exposes. It drives three things at once:

1. the asset type menu on each account card,
2. the parameter form in the edit drawer,
3. the OpenTofu that gets generated.

Because all three read the same table, **adding a provider or a resource type means editing
one Go file and nothing else.** No templ component, no CSS, no JavaScript. If you find
yourself needing to touch the UI to add a resource type, the abstraction has sprung a leak —
fix the leak rather than working around it.

## Shape

```go
type ParamField struct {
    Key       string    // HCL argument name, and the key in Asset.Params
    Label     string    // shown in the drawer
    Type      string    // FieldText | FieldNumber | FieldSelect | FieldBoolean
    Options   []string  // FieldSelect only
    Default   any
    Directive bool      // steers generation; NOT an HCL argument
}

type ParamField struct {
    Key       string    // leaf argument name
    Block     string    // dot path to the containing block, empty for top level
                        // e.g. "boot_disk.initialize_params", "settings"
    Label     string
    Type      string
    Options   []string
    Wrap      string    // wraps the value, e.g. "base64encode(%s)"
    Default   any       // for a security-relevant field, this IS the secure value
    Advanced  bool      // sits behind the drawer's disclosure
    Directive bool      // steers generation, never emitted as an argument
    RequiresParam string // emit and show only when this boolean directive is on
}

// Fixed is a required argument with exactly one sensible answer and no user
// opinion. Never a security setting.
type Fixed struct {
    Block, Key, Expr string  // Expr is rendered HCL, not a Go value
}

// Companion is a resource emitted alongside an asset because the asset cannot
// exist without it, named <assetID>_<Suffix>.
type Companion struct {
    TofuType, Suffix string
    Fixed            []Fixed
    ParentRef        string  // argument on the parent that points at this companion
    ParentExpr       string
}

type ResourceType struct {
    Code     string        // badge text and stored identity, e.g. "EC2"
    Name     string        // menu text, e.g. "EC2 instance"
    TofuType string        // e.g. "aws_instance"
    Fixed      []Fixed
    Companions []Companion

    // How the resource is reached, and what governs access to it. This decides
    // whether a connection to it becomes a firewall rule at all.
    Network     string          // NetFirewalled | NetServiceEndpoint | NetEdge
    AddressAttr string          // attribute holding the address; empty when AddrNone
    AddressKind string          // AddrNone | AddrStaticIP | AddrEphemeralIP | AddrHostname
    StaticAddr  *StaticAddress  // how to give it a durable address, when it lacks one

    Params []ParamField
}

// StaticAddress names the resource that gives an asset a durable address. Only
// emitted when the user opts in via the static_public_ip param.
type StaticAddress struct {
    TofuType string  // aws_eip, azurerm_public_ip, google_compute_address
    Attr     string  // the attribute to reference instead
}

type Provider struct {
    Key   string   // "aws" — stored in Account.Provider
    Label string   // "AWS" — display name
    Color string   // hex; the provider's accent
    Dim   string   // hex; the muted background behind badges
    Types []ResourceType
}
```

### Functions

| Function | Use |
|---|---|
| `Register(p)` | called from each provider file's `init()` |
| `Get(key)` | one provider |
| `All()` | every provider, ordered by key so output is deterministic |
| `Type(provider, code)` | one resource type |
| `Defaults(provider, code)` | initial `Asset.Params` for a new asset |
| `f.ParamKey()` | how a field is keyed in `Asset.Params` — `Block + "." + Key`, or just `Key` at top level |
| `rt.Arguments()` | the params that are real HCL arguments — **emitters must use this**, not `Params` |
| `StaticAddressToggle()` | the ready-made `static_public_ip` param to attach alongside a `StaticAddr` |

## Optional blocks

`RequiresParam` on a `ParamField` names a boolean directive that must be on before the field is
emitted **or shown**. It exists for a block a resource may not accept at all, rather than one whose
values are merely a matter of taste.

EC2's root volume is the case. `root_block_device` is invalid on a container-backed or
instance-store AMI — Terraform rejects the block outright, so giving it sensible values does not
help; it has to be absent.

```go
{Key: ParamRootBlockDevice, Label: "Manage root volume", Type: FieldBoolean, Default: true, Directive: true, Advanced: true},
{Key: "volume_size", Block: "root_block_device", ..., RequiresParam: ParamRootBlockDevice},
{Key: "encrypted",   Block: "root_block_device", ..., RequiresParam: ParamRootBlockDevice},
```

Three things about this pattern:

- **The gate defaults on.** Most AMIs are EBS-backed, and `encrypted` defaults to `true` — a
  security default. A gate defaulting off would quietly remove root volume encryption from every
  new instance, so the minority case is the one that opts out.
- **The gated fields are hidden, not disabled**, by `splitParams` in `internal/ui/drawer.go`. A
  visible field whose value generation drops is worse than no field.
- **Flipping the gate re-renders the drawer**, because it changes which fields exist.
  `gatesOtherParams()` in `app.js` reads the catalog to decide, so a new gate needs no change
  there. Ordinary param edits deliberately do not re-render, but a gate is always a checkbox, so
  there is no keystroke to interrupt.

`Fixed` and `Companion` carry the same field, and every loop that walks them honours it.

## Account settings

A `Provider` carries `AccountParams` alongside its `Types`: the settings that belong to a whole
account rather than to one resource. They are ordinary `ParamField`s, so the drawer renders them
through the same `paramField` component an asset's params use, and adding one needs no new markup.

```go
AccountParams: AccountSettings([]string{"eu-west-2", "us-east-1", ...}, "eu-west-2"),
```

`AccountSettings` supplies the region plus the two default key fields. All three are `Directive`:
none is an argument on any resource. Passing `nil` regions omits the region field, for a provider
with no account-wide region to set — no provider currently does, because DigitalOcean's account
region is what its VPC is created in even though the provider block takes none.

Read a region with `Provider.Region(acc.Params)`, which falls back to the field's default. Never
read `params["region"]` directly: the generator always needs a value, because a provider block with
an empty region will not plan.

## Adding a resource type

Add one entry to the provider's `Types` slice:

```go
{
    Code: "EFS", Name: "Elastic file system", TofuType: "aws_efs_file_system",
    Params: []ParamField{
        {Key: "performance_mode", Label: "Performance mode", Type: FieldSelect,
            Options: []string{"generalPurpose", "maxIO"}, Default: "generalPurpose"},
        {Key: "encrypted", Label: "Encrypted", Type: FieldBoolean, Default: true},
    },
},
```

Rebuild and it appears in the AWS type menu with a working edit form.

Rules to follow:

- **`Code` is permanent.** It is stored in every saved session that uses the type. Renaming it
  makes old session files fail validation with "unknown resource type". If a badge must change
  its appearance, add a display field rather than changing `Code`.
- **Get `Network` right.** This is the field that decides whether connections to the resource
  become firewall rules, and getting it wrong is silent — the generator will happily emit a
  security-group rule for something that has no security group, producing HCL that applies
  cleanly and controls nothing. The three kinds:

  | Kind | Use for | Examples |
  |---|---|---|
  | `NetFirewalled` | has an IP in a VPC/VNet and a firewall to attach rules to | instances, VMs, droplets, managed databases, load balancers, clusters |
  | `NetServiceEndpoint` | reached at a public service endpoint, access governed by IAM or a resource policy, **no firewall** | object storage, functions, app platforms |
  | `NetEdge` | a CDN — no stable egress IP, so origin access is controlled by configuration and header auth | every `CDN` type |

- **`AddressAttr` must name a real attribute** of `TofuType`, and `AddressKind` must be honest
  about what that attribute actually is. There are four cases and they generate differently:

  | `AddressKind` | Meaning | Generator behaviour |
  |---|---|---|
  | `AddrNone` | no address attribute at all | cross-account rules cannot be generated |
  | `AddrStaticIP` | an IP that does not change | referenced directly |
  | `AddrEphemeralIP` | an IP that changes on restart | **rule refused** until the user enables a static address |
  | `AddrHostname` | a DNS name | reported — security groups cannot use hostnames |

  `aws_instance.public_ip` is `AddrEphemeralIP`: it changes on stop/start, so a rule against it
  would break silently on the next restart. `aws_lb.dns_name` and `aws_db_instance.address` are
  `AddrHostname`. `AddressAttr` and `AddressKind` must agree — either both set, or neither.
- **Set `StaticAddr` whenever `AddressKind` is `AddrEphemeralIP`**, and add
  `StaticAddressToggle()` to `Params`. The two are halves of one mechanism — a `StaticAddr` with no
  toggle can never be switched on, and a toggle with no `StaticAddr` does nothing. Without the
  pair, the generator's refusal is a dead end: the user is told no rule was generated and has no
  way to fix it. `TestEveryResourceTypeIsClassified` enforces the pairing in both directions.

  Do **not** set `StaticAddr` on a resource whose address is required scaffolding rather than a
  choice. Azure's App Gateway always needs an `azurerm_public_ip`, so generation creates one
  regardless and a toggle would be a control that does nothing.
- **Mark a param `Directive: true` when it is not an HCL argument.** `static_public_ip` is the
  current example: `aws_instance` has no argument by that name, so emitting it verbatim would
  produce invalid configuration. Directives still render in the drawer and still live in
  `Asset.Params` — they are simply filtered out by `rt.Arguments()`, which is the only thing
  emitters may iterate.
- **Put the argument's real name in `Key` and its real position in `Block`.** Check a provider
  schema; do not write it from memory. Fifteen parameters in the original tables were invented —
  plausible names for arguments that do not exist — and no chart containing those types could
  validate. `documentation/INVALID-PARAMS.md` records every one.
- **A `Fixed` key must never collide with a `ParamField` key in the same block.** The user would
  edit a field that the fixed value silently overrides — a control that does nothing. A catalog
  test enforces this.
- **`Fixed` is for structural arguments only**, never security. A security setting is an ordinary
  editable field whose `Default` is the secure value.
- **Use `FieldScript` for anything multi-line**, and only for that. It is the one field type
  permitted to contain newlines, renders as a textarea, and has a 16KB cap rather than the 200-char
  one names get. A startup script in a `FieldText` would be silently truncated at the first newline
  by `Normalise`.
- **Use `Wrap` when the provider wants the value transformed.** `"base64encode(%s)"` on Azure's
  `custom_data`, which takes base64 where the other clouds take a plain string. The alternative —
  special-casing it in the emitter — is the per-type branch the catalog exists to avoid.
- **Attach `SSHKeyParams` to every machine type, and `AnsibleToggles` only where Ansible applies.**
  They are separate because SSH access and Ansible management are separate wants. Bundling them
  once made it impossible to give a host a key without also declaring it Ansible-managed.
- **Use `Companion.Count` when the decision depends on a value only Terraform can see.** An SSH key
  may arrive as a variable, so whether to create a key pair is a plan-time question. Reference a
  counted companion with `one(...)`, which yields null at count zero.
- **Use `RequiresParam` for anything that only applies when a directive is on.** Both `Fixed` and
  `Companion` carry it. SSH key wiring uses it: a key pair on a host nobody manages with Ansible is
  meaningless. An empty `RequiresParam` always applies.
- **Mark a field `Advanced: true`** when it is not one of the three or four things someone picks
  when creating the asset. Everything else lands behind the drawer's disclosure.
- **Use `Companions` rather than special-casing the emitter.** `internal/tofu` must contain no
  `switch` on a specific `TofuType` outside `firewall.go` and `attach()`. If a type seems to need
  one, the catalog model is missing something — extend the model rather than adding a branch. A
  test enforces this too.
- **`Asset.Params` is keyed by `ParamKey()`, not `Key`.** Two fields may share a leaf name in
  different blocks — `name` appears in several — so the block path is part of the identity.
  Anything reading or writing a param must use `ParamKey()`: `Defaults`, `Normalise`, the drawer
  and the emitter all do.
- See `TOFU-MAPPING.md` for the attribute table, the companion list and the reasoning behind all
  of this.

## A note on moving a field into a block

Changing a field's `Block` changes its `ParamKey()`, so saved sessions carry the old key. Nothing
breaks: `Normalise` drops keys the catalog no longer declares and fills the new one from its
`Default`. The user loses that one value and keeps everything else.

This is why no schema version bump is needed for the parameter rewrite — the values being dropped
are the ones that were never valid arguments anyway.
- **`Key` is the HCL argument name.** Keep it exactly as OpenTofu spells it — the generator
  emits `key = value` directly.
- **Give every field a `Default`.** `Normalise` falls back to it whenever an incoming value is
  missing or the wrong type, so a nil default becomes a nil HCL value.
- `FieldSelect` values are checked against `Options` during normalisation. A value not in the
  list is replaced by the default, so the list is a whitelist, not a hint.

## Adding a provider

Create `internal/catalog/<name>.go`:

```go
package catalog

func init() {
    Register(Provider{
        Key:   "hetzner",
        Label: "Hetzner",
        Color: "#D50C2D",
        Dim:   "#4A1720",
        Types: []ResourceType{ /* ... */ },
    })
}
```

`init()` registration means importing the package is enough — there is no list of providers to
remember to update.

That is the whole job for the editor. Colours reach the UI automatically: `internal/ui/style.go`
walks `catalog.All()` and emits a `[data-provider="hetzner"]{--p:…;--p-dim:…}` rule into the
page. Every provider-tinted element (account dot, provider tag, asset badge, type menu code)
reads those two custom properties, so no CSS edit is needed. Colours are hex-checked before
they reach the stylesheet.

For generation, see `TOFU-MAPPING.md` — a new provider also needs its firewall model
implemented, and that is genuinely per-provider work.

## Why the catalog is Go code and not a config file

It changes rarely, it is read by the type system, and a YAML version would need a parsing and
validation layer for data that a compiler already checks. If the catalog ever needs to be
editable without a rebuild, `go:embed` a file at that point — not before.
