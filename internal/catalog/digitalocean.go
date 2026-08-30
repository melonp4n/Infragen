package catalog

// Checked against the digitalocean provider schema. Several types were missing
// required arguments entirely — a database cluster has no `version`, a Kubernetes
// cluster has neither `region` nor a node pool — so none of them could validate.
func init() {
	Register(Provider{
		Key:           "digitalocean",
		Label:         "DigitalOcean",
		Color:         "#4361EE",
		Dim:           "#26305C",
		TofuLocalName: "digitalocean",
		TofuSource:    "digitalocean/digitalocean",
		NameArg:       "name",
		// The DigitalOcean provider takes no region; resources carry their own.
		Types: []ResourceType{
			{
				Code: "DRP", Name: "Droplet", TofuType: "digitalocean_droplet",
				Network: NetFirewalled, AddressAttr: "ipv4_address", AddressKind: AddrStaticIP,
				Fixed: []Fixed{
					// Key auth rather than a root password emailed in plain text.
					{Key: "ssh_keys", Expr: "var.{{account}}_ssh_key_ids"},
					{Key: "vpc_uuid", Expr: "digitalocean_vpc.{{account}}.id"},
				},
				Variables: []Variable{{
					Name: "{{account}}_ssh_key_ids", Description: "DigitalOcean SSH key IDs or fingerprints",
					Type: "list(string)", Default: "[]",
				}},
				Params: []ParamField{
					{Key: "size", Label: "Droplet size", Type: FieldSelect, Options: []string{"s-1vcpu-1gb", "s-2vcpu-2gb", "s-4vcpu-8gb", "c-4"}, Default: "s-1vcpu-1gb"},
					{Key: "image", Label: "Image", Type: FieldText, Default: "ubuntu-24-04-x64"},
					{Key: "region", Label: "Region", Type: FieldSelect, Options: []string{"nyc1", "nyc3", "sfo3", "ams3", "lon1", "sgp1"}, Default: "lon1"},
					{Key: "monitoring", Label: "Monitoring", Type: FieldBoolean, Default: true, Advanced: true},
					{Key: "backups", Label: "Backups", Type: FieldBoolean, Default: false, Advanced: true},
				},
			},
			{
				Code: "APP", Name: "App platform", TofuType: "digitalocean_app",
				// An app names itself inside its spec block.
				Network: NetServiceEndpoint, OmitName: true,
				Fixed: []Fixed{
					{Block: "spec", Key: "name", Expr: `"{{asset-dashed}}"`},
					{Block: "spec.service", Key: "name", Expr: `"web"`},
					{Block: "spec.service.image", Key: "registry_type", Expr: `"DOCKER_HUB"`},
					{Block: "spec.service.image", Key: "repository", Expr: `"nginx"`},
					{Block: "spec.service.image", Key: "tag", Expr: `"latest"`},
				},
				Params: []ParamField{
					{Key: "region", Block: "spec", Label: "Region", Type: FieldSelect, Options: []string{"nyc", "ams", "fra", "lon", "sgp"}, Default: "lon"},
					{Key: "instance_size_slug", Block: "spec.service", Label: "Instance size", Type: FieldSelect, Options: []string{"basic-xxs", "basic-xs", "professional-xs"}, Default: "basic-xxs"},
					{Key: "instance_count", Block: "spec.service", Label: "Instance count", Type: FieldNumber, Default: 1},
				},
			},
			{
				Code: "SPC", Name: "Spaces bucket", TofuType: "digitalocean_spaces_bucket",
				Network: NetServiceEndpoint,
				Params: []ParamField{
					{Key: "region", Label: "Region", Type: FieldSelect, Options: []string{"nyc3", "ams3", "sgp1", "fra1"}, Default: "ams3"},
					{Key: "acl", Label: "ACL", Type: FieldSelect, Options: []string{"private", "public-read"}, Default: "private", Advanced: true},
					{Key: "force_destroy", Label: "Destroy with contents", Type: FieldBoolean, Default: true, Advanced: true},
				},
			},
			{
				Code: "DB", Name: "Managed DB", TofuType: "digitalocean_database_cluster",
				Network: NetFirewalled, AddressAttr: "host", AddressKind: AddrHostname,
				Fixed: []Fixed{
					{Key: "private_network_uuid", Expr: "digitalocean_vpc.{{account}}.id"},
				},
				Params: []ParamField{
					{Key: "engine", Label: "Engine", Type: FieldSelect, Options: []string{"pg", "mysql", "redis", "mongodb"}, Default: "pg"},
					// Required, and absent from the previous table entirely.
					{Key: "version", Label: "Engine version", Type: FieldText, Default: "16"},
					{Key: "size", Label: "Node size", Type: FieldSelect, Options: []string{"db-s-1vcpu-1gb", "db-s-2vcpu-4gb"}, Default: "db-s-1vcpu-1gb"},
					{Key: "node_count", Label: "Node count", Type: FieldNumber, Default: 1},
					{Key: "region", Label: "Region", Type: FieldSelect, Options: []string{"nyc3", "sfo3", "fra1", "lon1"}, Default: "lon1"},
				},
			},
			{
				Code: "K8S", Name: "Kubernetes", TofuType: "digitalocean_kubernetes_cluster",
				Network: NetFirewalled, AddressAttr: "endpoint", AddressKind: AddrHostname,
				Fixed: []Fixed{
					{Key: "vpc_uuid", Expr: "digitalocean_vpc.{{account}}.id"},
					{Block: "node_pool", Key: "name", Expr: `"default"`},
				},
				Params: []ParamField{
					{Key: "region", Label: "Region", Type: FieldSelect, Options: []string{"nyc1", "nyc3", "ams3", "lon1", "fra1"}, Default: "lon1"},
					{Key: "version", Label: "K8s version", Type: FieldText, Default: "1.31.1-do.4"},
					{Key: "size", Block: "node_pool", Label: "Node size", Type: FieldSelect, Options: []string{"s-2vcpu-4gb", "s-4vcpu-8gb"}, Default: "s-2vcpu-4gb"},
					{Key: "node_count", Block: "node_pool", Label: "Node count", Type: FieldNumber, Default: 3},
				},
			},
			{
				Code: "LB", Name: "Load balancer", TofuType: "digitalocean_loadbalancer",
				Network: NetFirewalled, AddressAttr: "ip", AddressKind: AddrStaticIP,
				Fixed: []Fixed{
					{Key: "vpc_uuid", Expr: "digitalocean_vpc.{{account}}.id"},
					{Block: "forwarding_rule", Key: "entry_port", Expr: "80"},
					{Block: "forwarding_rule", Key: "entry_protocol", Expr: `"http"`},
					{Block: "forwarding_rule", Key: "target_port", Expr: "80"},
					{Block: "forwarding_rule", Key: "target_protocol", Expr: `"http"`},
				},
				Params: []ParamField{
					{Key: "region", Label: "Region", Type: FieldSelect, Options: []string{"nyc1", "nyc3", "ams3", "lon1", "fra1"}, Default: "lon1"},
					{Key: "size", Label: "Size", Type: FieldSelect, Options: []string{"lb-small", "lb-medium", "lb-large"}, Default: "lb-small"},
					{Key: "redirect_http_to_https", Label: "Redirect HTTP to HTTPS", Type: FieldBoolean, Default: true, Advanced: true},
				},
			},
			{
				Code: "CDN", Name: "CDN endpoint", TofuType: "digitalocean_cdn",
				// Fronts Spaces only; an external origin is not a supported setup,
				// which is why the bucket comes with it.
				Network: NetEdge, AddressAttr: "endpoint", AddressKind: AddrHostname,
				// A CDN endpoint has no name argument; it is identified by origin.
				OmitName: true,
				Companions: []Companion{{
					TofuType: "digitalocean_spaces_bucket", Suffix: "origin",
					Fixed: []Fixed{
						{Key: "name", Expr: `"{{asset-dashed}}-origin"`},
						{Key: "region", Expr: `"ams3"`},
						{Key: "acl", Expr: `"public-read"`},
						{Key: "force_destroy", Expr: "true"},
					},
					ParentRef:  "origin",
					ParentExpr: "digitalocean_spaces_bucket.{{asset}}_origin.bucket_domain_name",
				}},
				Params: []ParamField{
					{Key: "ttl", Label: "TTL (s)", Type: FieldNumber, Default: 3600},
					{Key: "custom_domain", Label: "Custom domain", Type: FieldText, Default: "", Advanced: true},
				},
			},
		},
	})
}
