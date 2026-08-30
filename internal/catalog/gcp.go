package catalog

// Checked against the google provider schema. GCP keeps a name argument on every
// resource in the same dashed form, so these take the shared display name rather
// than declaring their own.
func init() {
	Register(Provider{
		Key:           "gcp",
		Label:         "GCP",
		Color:         "#4EA699",
		Dim:           "#274641",
		TofuLocalName: "google",
		TofuSource:    "hashicorp/google",
		// No labels: several google resources spell them differently or
		// do not take them at all.
		NameArg: "name",
		Config: []Fixed{
			{Key: "region", Expr: "{{region}}"},
			{Key: "project", Expr: "var.{{account}}_project"},
		},
		Variables: []Variable{{
			Name: "{{account}}_project", Description: "GCP project ID", Default: `"my-project"`,
		}},
		Types: []ResourceType{
			{
				Code: "GCE", Name: "Compute engine", TofuType: "google_compute_instance",
				Network: NetFirewalled, AddressKind: AddrEphemeralIP,
				AddressAttr: "network_interface[0].access_config[0].nat_ip",
				StaticAddr:  &StaticAddress{TofuType: "google_compute_address", Attr: "address"},
				Fixed: []Fixed{
					{Block: "network_interface", Key: "subnetwork", Expr: "google_compute_subnetwork.{{account}}.id"},
					{Key: "metadata",
						Expr: `{{sshkey}} != "" ? { ssh-keys = "{{sshuser}}:${{{sshkey}}}" } : {}`},
				},
				Params: append([]ParamField{
					StaticAddressToggle(),
					{Key: "machine_type", Label: "Machine type", Type: FieldSelect, Options: []string{"e2-micro", "e2-medium", "n2-standard-2", "c2-standard-4"}, Default: "e2-medium"},
					{Key: "zone", Label: "Zone", Type: FieldText, Default: "europe-west2-a"},
					{Key: "image", Block: "boot_disk.initialize_params", Label: "Boot image", Type: FieldText, Default: "debian-cloud/debian-12"},
					// Google spells this as a top-level argument rather than inside the
					// metadata map, which is the one that would need special handling.
					{Key: "metadata_startup_script", Label: "Startup script", Type: FieldScript, Default: "", Advanced: true},
					{Key: "size", Block: "boot_disk.initialize_params", Label: "Boot disk (GB)", Type: FieldNumber, Default: 10, Advanced: true},
					// Shielded VM: measured boot and rootkit protection.
					{Key: "enable_secure_boot", Block: "shielded_instance_config", Label: "Secure boot", Type: FieldBoolean, Default: true, Advanced: true},
					{Key: "enable_vtpm", Block: "shielded_instance_config", Label: "Virtual TPM", Type: FieldBoolean, Default: true, Advanced: true},
				}, sshAndAnsible("ansible")...),
			},
			{
				Code: "GCF", Name: "Cloud function", TofuType: "google_cloudfunctions2_function",
				Network: NetServiceEndpoint,
				Fixed: []Fixed{
					{Key: "location", Expr: "{{region}}"},
					{Block: "build_config.source.storage_source", Key: "bucket", Expr: "google_storage_bucket.{{asset}}_src.name"},
					{Block: "build_config.source.storage_source", Key: "object", Expr: "google_storage_bucket_object.{{asset}}_srcobj.name"},
				},
				Companions: []Companion{
					{
						TofuType: "google_storage_bucket", Suffix: "src",
						Fixed: []Fixed{
							{Key: "name", Expr: `"{{asset-dashed}}-src"`},
							{Key: "location", Expr: `"EU"`},
							{Key: "uniform_bucket_level_access", Expr: "true"},
							{Key: "force_destroy", Expr: "true"},
						},
					},
					{
						TofuType: "google_storage_bucket_object", Suffix: "srcobj",
						Fixed: []Fixed{
							{Key: "name", Expr: `"source.zip"`},
							{Key: "bucket", Expr: "google_storage_bucket.{{asset}}_src.name"},
							{Key: "source", Expr: "var.{{asset}}_source"},
						},
					},
				},
				Variables: []Variable{{
					Name: "{{asset}}_source", Description: "Path to the function source zip",
					Default: `"function.zip"`,
				}},
				Params: []ParamField{
					{Key: "runtime", Block: "build_config", Label: "Runtime", Type: FieldSelect, Options: []string{"nodejs20", "python312", "go122"}, Default: "nodejs20"},
					{Key: "entry_point", Block: "build_config", Label: "Entry point", Type: FieldText, Default: "handler"},
					{Key: "available_memory", Block: "service_config", Label: "Memory", Type: FieldText, Default: "256M", Advanced: true},
					{Key: "ingress_settings", Block: "service_config", Label: "Ingress", Type: FieldSelect, Options: []string{"ALLOW_INTERNAL_ONLY", "ALLOW_INTERNAL_AND_GCLB", "ALLOW_ALL"}, Default: "ALLOW_INTERNAL_ONLY", Advanced: true},
				},
			},
			{
				Code: "GCS", Name: "Cloud storage", TofuType: "google_storage_bucket",
				Network: NetServiceEndpoint,
				Params: []ParamField{
					{Key: "location", Label: "Location", Type: FieldText, Default: "EU"},
					{Key: "storage_class", Label: "Storage class", Type: FieldSelect, Options: []string{"STANDARD", "NEARLINE", "COLDLINE", "ARCHIVE"}, Default: "STANDARD"},
					// Uniform access removes per-object ACLs, and enforced prevention
					// stops the bucket being made public at all.
					{Key: "uniform_bucket_level_access", Label: "Uniform access control", Type: FieldBoolean, Default: true, Advanced: true},
					{Key: "public_access_prevention", Label: "Public access", Type: FieldSelect, Options: []string{"enforced", "inherited"}, Default: "enforced", Advanced: true},
					{Key: "force_destroy", Label: "Destroy with contents", Type: FieldBoolean, Default: true, Advanced: true},
				},
			},
			{
				Code: "CSQL", Name: "Cloud SQL", TofuType: "google_sql_database_instance",
				Network: NetFirewalled, AddressAttr: "public_ip_address", AddressKind: AddrStaticIP,
				Fixed: []Fixed{
					{Key: "region", Expr: "{{region}}"},
				},
				Params: []ParamField{
					{Key: "database_version", Label: "DB version", Type: FieldSelect, Options: []string{"POSTGRES_16", "MYSQL_8_0"}, Default: "POSTGRES_16"},
					{Key: "tier", Block: "settings", Label: "Tier", Type: FieldSelect, Options: []string{"db-f1-micro", "db-custom-2-7680"}, Default: "db-f1-micro"},
					{Key: "availability_type", Block: "settings", Label: "Availability", Type: FieldSelect, Options: []string{"ZONAL", "REGIONAL"}, Default: "ZONAL", Advanced: true},
					// Encrypted-only rather than disabling the public IP: turning
					// ipv4 off needs a private network the chart does not describe.
					{Key: "ssl_mode", Block: "settings.ip_configuration", Label: "SSL mode", Type: FieldSelect, Options: []string{"ENCRYPTED_ONLY", "ALLOW_UNENCRYPTED_AND_ENCRYPTED"}, Default: "ENCRYPTED_ONLY", Advanced: true},
					{Key: "enabled", Block: "settings.backup_configuration", Label: "Backups", Type: FieldBoolean, Default: true, Advanced: true},
					{Key: "deletion_protection", Label: "Deletion protection", Type: FieldBoolean, Default: false, Advanced: true},
				},
			},
			{
				Code: "GKE", Name: "GKE cluster", TofuType: "google_container_cluster",
				// `endpoint` is the control plane, not the workload. GCP firewall rules
				// target nodes by network tag, so there is no address to reference here.
				Network: NetFirewalled,
				Fixed: []Fixed{
					{Key: "location", Expr: "{{region}}"},
					// The default pool cannot be configured, so it is replaced by an
					// explicit one — this is Google's own recommendation.
					{Key: "remove_default_node_pool", Expr: "true"},
					{Key: "initial_node_count", Expr: "1"},
					{Key: "network", Expr: "google_compute_network.{{account}}.id"},
					{Key: "subnetwork", Expr: "google_compute_subnetwork.{{account}}.id"},
					{Block: "workload_identity_config", Key: "workload_pool", Expr: `"${var.{{account}}_project}.svc.id.goog"`},
				},
				Companions: []Companion{{
					TofuType: "google_container_node_pool", Suffix: "nodes",
					Fixed: []Fixed{
						{Key: "name", Expr: `"{{asset-dashed}}-nodes"`},
						{Key: "cluster", Expr: "google_container_cluster.{{asset}}.id"},
						{Key: "node_count", Expr: "3"},
						{Block: "node_config", Key: "machine_type", Expr: `"e2-medium"`},
						{Block: "node_config.shielded_instance_config", Key: "enable_secure_boot", Expr: "true"},
					},
				}},
				Params: []ParamField{
					{Key: "deletion_protection", Label: "Deletion protection", Type: FieldBoolean, Default: false, Advanced: true},
				},
			},
			{
				Code: "CDN", Name: "Cloud CDN", TofuType: "google_compute_backend_bucket",
				// Origin fetches arrive from the load balancer and health-check ranges.
				Network: NetEdge,
				Companions: []Companion{{
					TofuType: "google_storage_bucket", Suffix: "bucket",
					Fixed: []Fixed{
						{Key: "name", Expr: `"{{asset-dashed}}-origin"`},
						{Key: "location", Expr: `"EU"`},
						{Key: "uniform_bucket_level_access", Expr: "true"},
						{Key: "force_destroy", Expr: "true"},
					},
					ParentRef:  "bucket_name",
					ParentExpr: "google_storage_bucket.{{asset}}_bucket.name",
				}},
				Params: []ParamField{
					{Key: "enable_cdn", Label: "Enable CDN", Type: FieldBoolean, Default: true},
					{Key: "cache_mode", Block: "cdn_policy", Label: "Cache mode", Type: FieldSelect, Options: []string{"CACHE_ALL_STATIC", "USE_ORIGIN_HEADERS", "FORCE_CACHE_ALL"}, Default: "CACHE_ALL_STATIC", Advanced: true},
				},
			},
		},
	})
}
