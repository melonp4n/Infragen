package model

import "infrachart/internal/catalog"

// Seed builds the demonstration chart from the original prototype: a public CDN
// in front of a load balancer and a web tier, a private database, a second
// account doing image processing, and an office jump host with SSH access.
//
// It exists for two reasons — `-seed` gives a populated chart to click around,
// and tests get a fixture that exercises every node and line type.
func Seed() Session {
	s := Session{Version: SchemaVersion, Internet: Point{X: 1020, Y: 40}}

	acc1 := account("acc_1", "AWS Account 1", "aws", 60, 120)
	cdn := asset(&acc1, "asset_1", "CDN", "Edge CDN")
	alb := asset(&acc1, "asset_2", "ALB", "Public ALB")
	ec2 := asset(&acc1, "asset_3", "EC2", "Web tier EC2")
	rds := asset(&acc1, "asset_4", "RDS", "Orders DB")

	acc2 := account("acc_2", "AWS Account 2", "aws", 420, 340)
	lambda := asset(&acc2, "asset_5", "λ", "Image resize fn")
	s3 := asset(&acc2, "asset_6", "S3", "Uploads bucket")

	acc3 := account("acc_3", "DigitalOcean", "digitalocean", 760, 120)
	droplet := asset(&acc3, "asset_7", "DRP", "App droplet")
	asset(&acc3, "asset_8", "DB", "Managed PG")

	acc4 := account("acc_4", "Azure", "azure", 760, 420)
	asset(&acc4, "asset_9", "VM", "Batch worker")

	s.Accounts = []Account{acc1, acc2, acc3, acc4}
	s.ExternalIPs = []ExternalIP{
		{ID: "extip_1", Label: "Office jumphost", IP: "203.0.113.10/32", X: 1180, Y: 40},
	}

	internet := NodeRef{Type: NodeInternet}
	jump := NodeRef{Type: NodeExtIP, ID: "extip_1"}

	s.Connections = []Connection{
		// The CDN is the only thing the public can reach — the real attack surface.
		conn("conn_1", internet, cdn, rules(rule("TCP", "443", ""), rule("TCP", "80", "")), nil),
		conn("conn_2", cdn, alb, rules(rule("TCP", "443", "origin fetch")), nil),
		conn("conn_3", alb, ec2, rules(rule("TCP", "8080", "health check + traffic")), nil),
		conn("conn_4", ec2, rds, rules(rule("TCP", "5432", "app queries")), nil),
		// Outbound only: the web tier can fetch updates but is not reachable from
		// the internet.
		conn("conn_5", ec2, internet, rules(rule("TCP", "443", "OS + package updates")), nil),
		conn("conn_6", lambda, s3, rules(rule("TCP", "443", "GetObject / PutObject")), nil),
		// The jump host is hardcoded — nothing is deployed for it.
		conn("conn_7", jump, ec2, rules(rule("TCP", "22", "admin SSH access")), nil),
		conn("conn_8", jump, droplet, rules(rule("TCP", "22", "admin SSH access")), nil),
	}
	return s
}

func account(id, name, provider string, x, y float64) Account {
	return Account{ID: id, Name: name, Provider: provider, X: x, Y: y}
}

// asset appends a new asset to an account and returns a reference to it, so the
// connection list below reads as a graph rather than a pile of string literals.
func asset(acc *Account, id, code, name string) NodeRef {
	acc.Assets = append(acc.Assets, Asset{
		ID: id, Code: code, Name: name,
		Params: catalog.Defaults(acc.Provider, code),
	})
	return NodeRef{Type: NodeAsset, AccountID: acc.ID, AssetID: id}
}

func conn(id string, a, b NodeRef, aToB, bToA []Rule) Connection {
	return Connection{ID: id, A: a, B: b, AToB: aToB, BToA: bToA}
}

func rule(protocol, port, detail string) Rule {
	return Rule{Protocol: protocol, Port: port, Detail: detail}
}

func rules(r ...Rule) []Rule { return r }
