package tofu

import (
	"strings"

	"infrachart/internal/catalog"
)

// Placeholders a catalog expression may use. Fixed values and companion
// references need to name account-scoped resources, but the catalog is data and
// cannot know an account ID — so it writes these and the emitter substitutes.
//
// `{{ }}` rather than `${ }` because the latter is HCL's own interpolation and
// the two would be indistinguishable in the output.
const (
	phAccount = "{{account}}"
	phAsset   = "{{asset}}"
	phRegion  = "{{region}}"
	// Dashed forms, for names that reject underscores: GCP resource names,
	// DigitalOcean names, S3 bucket prefixes.
	phAccountDashed = "{{account-dashed}}"
	phAssetDashed   = "{{asset-dashed}}"
)

// expand substitutes the placeholders in a catalog expression.
func expand(expr string, accountID, assetID string) string {
	return strings.NewReplacer(
		phAccountDashed, dashed(accountID),
		phAssetDashed, dashed(assetID),
		phAccount, accountID,
		phAsset, assetID,
		phRegion, "var."+accountID+"_region",
	).Replace(expr)
}

// blockNode collects arguments by their block path so nested blocks can be
// emitted in one pass. Order is preserved: arguments in declaration order, then
// child blocks in the order they were first seen, which keeps output stable.
type blockNode struct {
	args     [][2]string
	order    []string
	children map[string]*blockNode
}

// add places an argument at a dot path. An empty path means top level.
func (n *blockNode) add(path, key, expr string) {
	if path == "" {
		n.args = append(n.args, [2]string{key, expr})
		return
	}
	head, rest, _ := strings.Cut(path, ".")
	if n.children == nil {
		n.children = map[string]*blockNode{}
	}
	child, ok := n.children[head]
	if !ok {
		child = &blockNode{}
		n.children[head] = child
		n.order = append(n.order, head)
	}
	child.add(rest, key, expr)
}

// ensure declares a block with no arguments, which some providers require —
// azurerm's `features {}` is the reason this exists.
func (n *blockNode) ensure(path string) {
	if path == "" {
		return
	}
	head, rest, _ := strings.Cut(path, ".")
	if n.children == nil {
		n.children = map[string]*blockNode{}
	}
	child, ok := n.children[head]
	if !ok {
		child = &blockNode{}
		n.children[head] = child
		n.order = append(n.order, head)
	}
	child.ensure(rest)
}

// render writes this node's arguments, then its child blocks. Arguments come
// first so a block never separates two arguments that should align together.
func (n *blockNode) render(w *writer) {
	for _, a := range n.args {
		w.arg(a[0], a[1])
	}
	for _, name := range n.order {
		child := n.children[name]
		w.block(name, func() { child.render(w) })
	}
}

// varSet collects the variable declarations a chart needs, deduplicated by name
// and kept in first-seen order so output stays deterministic.
type varSet struct {
	order []string
	seen  map[string]struct{}
	decls map[string]varDecl
}

type varDecl struct {
	Name        string
	Description string
	Type        string
	Default     string // rendered HCL; never set on a sensitive variable
	Sensitive   bool
}

func newVarSet() *varSet {
	return &varSet{seen: map[string]struct{}{}, decls: map[string]varDecl{}}
}

func (v *varSet) add(d varDecl) {
	if _, ok := v.seen[d.Name]; ok {
		return
	}
	v.seen[d.Name] = struct{}{}
	v.order = append(v.order, d.Name)
	v.decls[d.Name] = d
}

// render writes the variable blocks. Sensitive ones carry no default, so
// `tofu plan` prompts rather than a secret living in the configuration.
func (v *varSet) render(w *writer) {
	if len(v.order) == 0 {
		return
	}
	w.blank()
	w.line("# Values the chart cannot supply. Sensitive ones have no default, so")
	w.line("# `tofu plan` prompts for them rather than storing a secret here.")
	for _, name := range v.order {
		d := v.decls[name]
		w.blank()
		w.block("variable "+quote(d.Name), func() {
			if d.Description != "" {
				w.arg("description", quote(d.Description))
			}
			w.arg("type", orDefault(d.Type, "string"))
			// A sensitive variable must have no default, or the prompt never
			// happens and whatever was defaulted is what gets used.
			if d.Default != "" && !d.Sensitive {
				w.arg("default", d.Default)
			}
			if d.Sensitive {
				w.arg("sensitive", "true")
			}
		})
	}
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// addFixed places one catalog Fixed value onto a node. A Fixed with no Key
// declares an empty block and nothing else — azurerm's `features {}` and a
// function app's `site_config {}` are both required and both empty.
func addFixed(n *blockNode, fx catalog.Fixed, accountID, assetID string) {
	if fx.Key == "" {
		n.ensure(fx.Block)
		return
	}
	n.add(fx.Block, fx.Key, expand(fx.Expr, accountID, assetID))
}
