package ui

import (
	"fmt"
	"strings"

	"github.com/a-h/templ"

	"infrachart/internal/catalog"
)

// providerCSS builds the `[data-provider="…"]` rules for every registered
// provider. Provider colours live in the catalog so that adding a provider means
// editing one Go file, and they reach the page as CSS custom properties, which
// keeps every node component free of inline colour styles.
//
// The values are Go constants, never user input, so they are checked where they
// are written — TestProviderColoursAreHex in internal/catalog — rather than on
// every page render behind a fallback that can never fire.
func providerCSS() templ.Component {
	var b strings.Builder
	for _, p := range catalog.All() {
		fmt.Fprintf(&b, "[data-provider=%q]{--p:%s;--p-dim:%s;}\n", p.Key, p.Color, p.Dim)
	}
	return templ.Raw(b.String())
}
