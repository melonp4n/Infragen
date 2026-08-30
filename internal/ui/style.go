package ui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/a-h/templ"

	"infrachart/internal/catalog"
)

// Provider colours live in the catalog so that adding a provider means editing
// one Go file. They reach the page as CSS custom properties on a per-provider
// selector, which keeps every node component free of inline colour styles.
var hexColor = regexp.MustCompile(`^#[0-9A-Fa-f]{3,8}$`)

// providerCSS builds the `[data-provider="…"]` rules for every registered
// provider. Colours are hex-checked before they reach the stylesheet: they are
// our own constants today, but this is still a value going into CSS.
func providerCSS() templ.Component {
	var b strings.Builder
	for _, p := range catalog.All() {
		color, dim := p.Color, p.Dim
		if !hexColor.MatchString(color) {
			color = "var(--text-dim)"
		}
		if !hexColor.MatchString(dim) {
			dim = "var(--panel-2)"
		}
		fmt.Fprintf(&b, "[data-provider=%q]{--p:%s;--p-dim:%s;}\n", p.Key, color, dim)
	}
	return templ.Raw(b.String())
}
