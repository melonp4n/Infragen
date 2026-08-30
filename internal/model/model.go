// Package model holds the session graph: the accounts, assets, external IPs and
// connections that make up a chart. This is the canonical schema — exported JSON
// is exactly these structs, and the browser works with the same shape.
package model

// SchemaVersion is stamped into every exported session so future readers can
// detect and migrate older files.
const SchemaVersion = 1

// Point is a position on the chart canvas, in canvas pixels.
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Session is a whole chart. It is the unit of export and import.
type Session struct {
	Version     int          `json:"version"`
	Accounts    []Account    `json:"accounts"`
	Internet    Point        `json:"internet"`
	ExternalIPs []ExternalIP `json:"externalIps"`
	Connections []Connection `json:"connections"`
}

// Account is a cloud account container. Assets live inside it and move with it.
type Account struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Provider string  `json:"provider"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Assets   []Asset `json:"assets"`
}

// Asset is one deployable resource. Code keys into the provider's catalog, and
// Params is keyed by that resource type's ParamField.Key.
//
// Position inside the account is the slice index — tiles flow in a wrapping row,
// so a free x/y here would fight the layout.
type Asset struct {
	ID     string         `json:"id"`
	Code   string         `json:"code"`
	Name   string         `json:"name"`
	Params map[string]any `json:"params"`
}

// ExternalIP is a hardcoded address such as a jump host. Never deployed —
// it only supplies a CIDR to the rules that reference it.
type ExternalIP struct {
	ID    string  `json:"id"`
	Label string  `json:"label"`
	IP    string  `json:"ip"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
}

// Node type discriminators for NodeRef.Type.
const (
	NodeInternet = "internet"
	NodeExtIP    = "extip"
	NodeAsset    = "asset"
)

// NodeRef points at one end of a connection. Which fields are set depends on Type.
type NodeRef struct {
	Type      string `json:"type"`
	ID        string `json:"id,omitempty"`
	AccountID string `json:"accountId,omitempty"`
	AssetID   string `json:"assetId,omitempty"`
}

// Rule is one allowed traffic flow in a single direction.
type Rule struct {
	Protocol string `json:"protocol"`
	Port     string `json:"port"`
	Detail   string `json:"detail"`
}

// Connection joins two nodes. Direction matters: AToB is traffic B accepts when
// A initiates it. An empty rule list means that direction is blocked.
type Connection struct {
	ID   string  `json:"id"`
	A    NodeRef `json:"a"`
	B    NodeRef `json:"b"`
	AToB []Rule  `json:"aToB"`
	BToA []Rule  `json:"bToA"`
}

// Key returns a stable string identity for a node, used to match a NodeRef to
// its rendered DOM element and to detect duplicate connections.
func (n NodeRef) Key() string {
	switch n.Type {
	case NodeInternet:
		return NodeInternet
	case NodeExtIP:
		return NodeExtIP + ":" + n.ID
	default:
		return NodeAsset + ":" + n.AccountID + ":" + n.AssetID
	}
}

// Account finds an account by ID.
func (s *Session) Account(id string) *Account {
	for i := range s.Accounts {
		if s.Accounts[i].ID == id {
			return &s.Accounts[i]
		}
	}
	return nil
}

// Asset finds an asset within an account by ID.
func (a *Account) Asset(id string) *Asset {
	for i := range a.Assets {
		if a.Assets[i].ID == id {
			return &a.Assets[i]
		}
	}
	return nil
}

// ExternalIP finds a hardcoded external IP by ID.
func (s *Session) ExternalIP(id string) *ExternalIP {
	for i := range s.ExternalIPs {
		if s.ExternalIPs[i].ID == id {
			return &s.ExternalIPs[i]
		}
	}
	return nil
}

// Label renders a human-readable name for a node, used in the drawer, in
// generated comments and in resource names.
func (s *Session) Label(n NodeRef) string {
	switch n.Type {
	case NodeInternet:
		return "Internet"
	case NodeExtIP:
		if e := s.ExternalIP(n.ID); e != nil {
			return e.Label + " (" + e.IP + ")"
		}
		return "(removed)"
	default:
		acc := s.Account(n.AccountID)
		if acc == nil {
			return "(removed)"
		}
		as := acc.Asset(n.AssetID)
		if as == nil {
			return acc.Name + " / (removed)"
		}
		return acc.Name + " → " + as.Name
	}
}

// InternetTraffic splits a connection's rules into traffic arriving from the
// public internet and traffic leaving for it. isInternet is false when the
// connection does not touch the internet node at all.
//
// This lives here rather than in the UI because generation needs exactly the same
// split, and two implementations of "is this publicly reachable" would drift.
func (c Connection) InternetTraffic() (inbound, outbound []Rule, isInternet bool) {
	switch {
	case c.A.Type == NodeInternet:
		return c.AToB, c.BToA, true
	case c.B.Type == NodeInternet:
		return c.BToA, c.AToB, true
	default:
		return nil, nil, false
	}
}
