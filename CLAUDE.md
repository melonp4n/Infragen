# infrachart

A localhost Go web app for drawing short-to-medium-lived infrastructure as a chart, and
generating OpenTofu from it. Cloud accounts are containers, assets are nodes inside them, and
the lines between nodes are the allowed traffic — which become security group rules.

## Read first

| File | What it covers |
|---|---|
| `documentation/STATUS.md` | **what is built, what is not, what to do next** |
| `documentation/ARCHITECTURE.md` | the stateless-server / client-state / server-rendered-markup split, and the routes |
| `documentation/DATA-MODEL.md` | the session schema, and why connection direction matters |
| `documentation/CATALOG.md` | how to add a provider or a resource type |
| `documentation/UI.md` | colour tokens, line semantics, interaction, the class contract |
| `documentation/TOFU-MAPPING.md` | resource mappings, companion resources, the four firewall models, credentials, secure defaults |
| `documentation/INVALID-PARAMS.md` | rollback record for the parameter rewrite, and its progress marker |

`documentation/example/iac-generator-ui.html` is the original single-file prototype. It works,
and it remains the reference for look and behaviour.

## Running

```
go tool templ generate && go run . -seed       # http://127.0.0.1:8080
```

`templ` is pinned as a module tool in `go.mod`, so `go tool templ` works without installing
anything or putting `~/go/bin` on your PATH. Re-run it after editing any `.templ` file — the
`_templ.go` files it produces are build artifacts, and are committed so that `go run .` works
on a clean checkout without generating first.

## Code style

- Two-space indentation, camelCase identifiers, opening brace on the same line.
- Comments succinct but clear — explain why, not what the line already says.
- `.go` files follow `gofmt` instead: tabs, and PascalCase for exported identifiers, because
  fighting `gofmt` means fighting every Go tool. `gofmt` already puts the brace on the same
  line. `.js`, `.css` and `.templ` markup follow the style above exactly.
- JSON field names are lowercase camelCase, set by struct tags.

## Verifying generated OpenTofu

```
go test -tags integration ./internal/tofu     # terraform init && validate, ~35s
```

This is the only check that proves an argument exists. `terraform fmt` proves the syntax parses and
nothing more, which is how fifteen invented parameters survived until someone actually ran validate.

## Things that will bite you

- **Never build HCL with templ.** It escapes for HTML, so quotes become `&quot;`. Generation
  belongs in `internal/tofu` using `text/template` or plain string building.
- **`ResourceType.Code` is permanent.** Saved sessions store it. Renaming one makes old files
  fail validation.
- **Call `model.Validate` then `model.Normalise` before generating anything.** Asset params
  reach HCL; normalisation is what keeps arbitrary JSON out of it.
- **Never build node markup in JavaScript.** Ask the server for the fragment. Two renderers for
  one component is exactly the bug farm this design avoids.
- **Red versus orange on a line is the most important thing the UI says.** Red means the public
  internet can initiate inwards; orange means egress only. Do not let a change blur them.
- **Never emit a firewall rule for a `NetServiceEndpoint` asset.** S3, Lambda, Blob, Spaces and
  App Platform have no firewall — access is IAM-governed. A rule for them applies cleanly and
  controls nothing, which is worse than no output because it looks like the tool worked.
- **A blank port means unspecified, never "all ports".** Allowing everything is typed as `*`.
  Blank is fail-open: an abandoned half-written rule would open every port on the asset. It is
  accepted while editing so the drawer still renders, then refused at generation with a warning.
- **Never emit a rule against an address that is not stable.** `aws_instance.public_ip` changes on
  stop/start, so the rule breaks silently later. Refuse it and tell the user to enable a static
  address.
- **Resource addresses derive from `Asset.ID`, never from a display name.** A name-derived address
  means renaming an asset destroys and recreates the resource on the next apply.
- **One classification of internet exposure, shared by `ui` and `tofu`.** Never two. Two copies of
  "is this publicly reachable" drifting apart is the worst bug this tool could have.
- **Check a provider schema before writing a catalog parameter.** Fifteen of the original ones were
  invented — plausible names for arguments that do not exist. `terraform fmt` passing proves the
  syntax is well formed and nothing about whether an argument is real; only `tofu validate` does.
- **No `switch` on a specific `TofuType` in `internal/tofu`** outside `firewall.go` and `attach()`.
  If a type needs special-casing, extend the catalog model instead.
- **Nil slices marshal to JSON `null`, and the browser expects arrays.** Anything that hands a
  session to the browser goes through `model.Normalise` first.
