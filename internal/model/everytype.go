package model

import "infrachart/internal/catalog"

// EveryType builds a session containing one asset of every registered resource
// type, one account per provider.
//
// It exists for the acceptance gate: `tofu validate` on this chart is what proves
// each type emits what its provider requires. A type missing from here is a type
// nothing checks.
func EveryType() Session {
	s := Session{Version: SchemaVersion, Internet: Point{X: 1020, Y: 40}}
	n := 0
	for _, p := range catalog.All() {
		acc := Account{
			ID:       "acc_" + p.Key,
			Name:     p.Label,
			Provider: p.Key,
			Params:   catalog.AccountDefaults(p.Key),
			X:        float64(60 + n*380),
			Y:        120,
		}
		for i, rt := range p.Types {
			acc.Assets = append(acc.Assets, Asset{
				ID:     "asset_" + p.Key + "_" + slugCode(rt.Code, i),
				Code:   rt.Code,
				Name:   rt.Name,
				Params: catalog.Defaults(p.Key, rt.Code),
			})
		}
		s.Accounts = append(s.Accounts, acc)
		n++
	}
	return s
}

// slugCode keeps resource names valid where a code is a glyph rather than a word —
// Lambda's code is "λ", which is not a legal HCL identifier.
func slugCode(code string, index int) string {
	out := make([]rune, 0, len(code))
	for _, r := range code {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out = append(out, r)
		case r >= 'A' && r <= 'Z':
			out = append(out, r+('a'-'A'))
		}
	}
	if len(out) == 0 {
		return string(rune('a'+index)) + "type"
	}
	return string(out)
}
