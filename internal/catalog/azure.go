package catalog

// Checked against the azurerm provider schema. Azure puts more in nested blocks
// than the other clouds and its name rules differ per resource, so every type
// declares its own name rather than taking a shared one.
func init() {
	Register(Provider{
		Key:           "azure",
		Label:         "Azure",
		Color:         "#3A8FD9",
		Dim:           "#233F52",
		TofuLocalName: "azurerm",
		TofuSource:    "hashicorp/azurerm",
		// Name is per type: Azure name rules differ per resource.
		LabelArg: "tags",
		// azurerm takes no region — location is per resource — but the empty
		// features block is mandatory.
		Config: []Fixed{{Block: "features"}},
		AccountParams: AccountSettings([]string{
			"uksouth", "ukwest", "westeurope", "northeurope", "eastus", "westus2", "southeastasia",
		}, "uksouth"),
		Types: []ResourceType{
			{
				Code: "VM", Name: "Virtual machine", TofuType: "azurerm_linux_virtual_machine",
				Network: NetFirewalled, AddressAttr: "public_ip_address", AddressKind: AddrEphemeralIP,
				StaticAddr: &StaticAddress{TofuType: "azurerm_public_ip", Attr: "ip_address"},
				// The interface below only gets a public address when the static
				// toggle is on, so public_ip_address is empty until then.
				AddressRequires: ParamStaticPublicIP,
				Fixed: []Fixed{
					{Key: "name", Expr: `"{{asset-dashed}}"`},
					{Key: "resource_group_name", Expr: "azurerm_resource_group.{{account}}.name"},
					{Key: "location", Expr: "azurerm_resource_group.{{account}}.location"},
					// The admin name is fixed because admin_ssh_key.username must
					// match it exactly; two editable fields that must agree is a
					// trap, not a choice.
					{Key: "admin_username", Expr: `"azureuser"`},
					{Block: "admin_ssh_key", Key: "username", Expr: `"azureuser"`},
					{Block: "admin_ssh_key", Key: "public_key", Expr: "{{sshkey}}"},
				},
				Companions: []Companion{{
					TofuType: "azurerm_network_interface", Suffix: "nic",
					Fixed: []Fixed{
						{Key: "name", Expr: `"{{asset-dashed}}-nic"`},
						{Key: "resource_group_name", Expr: "azurerm_resource_group.{{account}}.name"},
						{Key: "location", Expr: "azurerm_resource_group.{{account}}.location"},
						{Block: "ip_configuration", Key: "name", Expr: `"internal"`},
						{Block: "ip_configuration", Key: "subnet_id", Expr: "azurerm_subnet.{{account}}.id"},
						{Block: "ip_configuration", Key: "private_ip_address_allocation", Expr: `"Dynamic"`},
						// Attaching the address is what makes it reachable. Emitting the
						// public IP without this left it allocated and unused.
						{Block: "ip_configuration", Key: "public_ip_address_id",
							Expr: "azurerm_public_ip.{{asset}}.id", RequiresParam: ParamStaticPublicIP},
					},
					ParentRef:  "network_interface_ids",
					ParentExpr: "[azurerm_network_interface.{{asset}}_nic.id]",
				}},
				Params: append([]ParamField{
					StaticAddressToggle(),
					{Key: "size", Label: "VM size", Type: FieldSelect, Options: []string{"Standard_B1s", "Standard_B2s", "Standard_D2s_v5", "Standard_E4s_v5"}, Default: "Standard_B2s"},
					// Password auth off by default: key auth is the Azure recommendation.
					{Key: "disable_password_authentication", Label: "SSH key auth only", Type: FieldBoolean, Default: true, Advanced: true},
					// Azure takes base64 here where every other cloud takes a plain string.
					{Key: "custom_data", Label: "Startup script", Type: FieldScript, Default: "", Advanced: true, Wrap: "base64encode(%s)"},
					{Key: "caching", Block: "os_disk", Label: "OS disk caching", Type: FieldSelect, Options: []string{"ReadWrite", "ReadOnly", "None"}, Default: "ReadWrite", Advanced: true},
					{Key: "storage_account_type", Block: "os_disk", Label: "OS disk type", Type: FieldSelect, Options: []string{"Standard_LRS", "StandardSSD_LRS", "Premium_LRS"}, Default: "StandardSSD_LRS", Advanced: true},
					{Key: "publisher", Block: "source_image_reference", Label: "Image publisher", Type: FieldText, Default: "Canonical", Advanced: true},
					{Key: "offer", Block: "source_image_reference", Label: "Image offer", Type: FieldText, Default: "ubuntu-24_04-lts", Advanced: true},
					{Key: "sku", Block: "source_image_reference", Label: "Image SKU", Type: FieldText, Default: "server", Advanced: true},
					{Key: "version", Block: "source_image_reference", Label: "Image version", Type: FieldText, Default: "latest", Advanced: true},
				}, sshAndAnsible("azureuser")...),
			},
			{
				Code: "FN", Name: "Function app", TofuType: "azurerm_linux_function_app",
				Network: NetServiceEndpoint,
				Fixed: []Fixed{
					{Key: "name", Expr: `"{{asset-dashed}}"`},
					{Key: "resource_group_name", Expr: "azurerm_resource_group.{{account}}.name"},
					{Key: "location", Expr: "azurerm_resource_group.{{account}}.location"},
					{Key: "storage_account_name", Expr: "azurerm_storage_account.{{asset}}_storage.name"},
					{Key: "storage_account_access_key", Expr: "azurerm_storage_account.{{asset}}_storage.primary_access_key"},
					// Required, and empty is valid.
					{Block: "site_config"},
				},
				Companions: []Companion{
					{
						TofuType: "azurerm_service_plan", Suffix: "plan",
						Fixed: []Fixed{
							{Key: "name", Expr: `"{{asset-dashed}}-plan"`},
							{Key: "resource_group_name", Expr: "azurerm_resource_group.{{account}}.name"},
							{Key: "location", Expr: "azurerm_resource_group.{{account}}.location"},
							{Key: "os_type", Expr: `"Linux"`},
							{Key: "sku_name", Expr: `"Y1"`},
						},
						ParentRef:  "service_plan_id",
						ParentExpr: "azurerm_service_plan.{{asset}}_plan.id",
					},
					{
						// A storage account name takes no hyphens and is capped at
						// 24 characters, so it is derived rather than dashed.
						TofuType: "azurerm_storage_account", Suffix: "storage",
						Fixed: []Fixed{
							{Key: "name", Expr: `substr(replace("{{asset}}", "_", ""), 0, 24)`},
							{Key: "resource_group_name", Expr: "azurerm_resource_group.{{account}}.name"},
							{Key: "location", Expr: "azurerm_resource_group.{{account}}.location"},
							{Key: "account_tier", Expr: `"Standard"`},
							{Key: "account_replication_type", Expr: `"LRS"`},
							{Key: "min_tls_version", Expr: `"TLS1_2"`},
						},
					},
				},
				Params: []ParamField{
					{Key: "https_only", Label: "HTTPS only", Type: FieldBoolean, Default: true, Advanced: true},
				},
			},
			{
				Code: "BLB", Name: "Blob storage", TofuType: "azurerm_storage_account",
				Network: NetServiceEndpoint,
				Fixed: []Fixed{
					{Key: "name", Expr: `substr(replace("{{asset}}", "_", ""), 0, 24)`},
					{Key: "resource_group_name", Expr: "azurerm_resource_group.{{account}}.name"},
					{Key: "location", Expr: "azurerm_resource_group.{{account}}.location"},
				},
				Params: []ParamField{
					{Key: "account_tier", Label: "Account tier", Type: FieldSelect, Options: []string{"Standard", "Premium"}, Default: "Standard"},
					{Key: "account_replication_type", Label: "Replication", Type: FieldSelect, Options: []string{"LRS", "GRS", "ZRS", "RAGRS"}, Default: "LRS"},
					{Key: "https_traffic_only_enabled", Label: "HTTPS only", Type: FieldBoolean, Default: true, Advanced: true},
					{Key: "min_tls_version", Label: "Minimum TLS", Type: FieldSelect, Options: []string{"TLS1_2", "TLS1_1", "TLS1_0"}, Default: "TLS1_2", Advanced: true},
					{Key: "allow_nested_items_to_be_public", Label: "Allow public blobs", Type: FieldBoolean, Default: false, Advanced: true},
					{Key: "public_network_access_enabled", Label: "Public network access", Type: FieldBoolean, Default: false, Advanced: true},
				},
			},
			{
				Code: "SQL", Name: "SQL database", TofuType: "azurerm_mssql_database",
				// The FQDN belongs to the server, not the database, so there is no
				// address on this resource to reference.
				Network: NetFirewalled,
				Fixed: []Fixed{
					{Key: "name", Expr: `"{{asset-dashed}}"`},
				},
				Companions: []Companion{{
					TofuType: "azurerm_mssql_server", Suffix: "server",
					Fixed: []Fixed{
						{Key: "name", Expr: `"{{asset-dashed}}-sql"`},
						{Key: "resource_group_name", Expr: "azurerm_resource_group.{{account}}.name"},
						{Key: "location", Expr: "azurerm_resource_group.{{account}}.location"},
						{Key: "version", Expr: `"12.0"`},
						{Key: "administrator_login", Expr: `"sqladmin"`},
						{Key: "administrator_login_password", Expr: "var.{{asset}}_sql_password"},
						{Key: "minimum_tls_version", Expr: `"1.2"`},
						{Key: "public_network_access_enabled", Expr: "false"},
					},
					ParentRef:  "server_id",
					ParentExpr: "azurerm_mssql_server.{{asset}}_server.id",
				}},
				Variables: []Variable{{
					Name: "{{asset}}_sql_password", Description: "SQL server administrator password",
					Sensitive: true,
				}},
				Params: []ParamField{
					{Key: "sku_name", Label: "SKU", Type: FieldSelect, Options: []string{"Basic", "S0", "S1", "GP_Gen5_2"}, Default: "S0"},
					{Key: "max_size_gb", Label: "Max size (GB)", Type: FieldNumber, Default: 32, Advanced: true},
					{Key: "zone_redundant", Label: "Zone redundant", Type: FieldBoolean, Default: false, Advanced: true},
				},
			},
			{
				Code: "AKS", Name: "AKS cluster", TofuType: "azurerm_kubernetes_cluster",
				Network: NetFirewalled,
				Fixed: []Fixed{
					{Key: "name", Expr: `"{{asset-dashed}}"`},
					{Key: "resource_group_name", Expr: "azurerm_resource_group.{{account}}.name"},
					{Key: "location", Expr: "azurerm_resource_group.{{account}}.location"},
					{Key: "dns_prefix", Expr: `"{{asset-dashed}}"`},
					{Block: "default_node_pool", Key: "name", Expr: `"default"`},
					// A managed identity rather than a service principal: no
					// credential to store or rotate.
					{Block: "identity", Key: "type", Expr: `"SystemAssigned"`},
					{Block: "node_provisioning_profile", Key: "mode", Expr: `"Manual"`},
				},
				Params: []ParamField{
					{Key: "node_count", Block: "default_node_pool", Label: "Node count", Type: FieldNumber, Default: 3},
					{Key: "vm_size", Block: "default_node_pool", Label: "Node VM size", Type: FieldSelect, Options: []string{"Standard_DS2_v2", "Standard_D4s_v5"}, Default: "Standard_DS2_v2"},
					{Key: "kubernetes_version", Label: "K8s version", Type: FieldText, Default: "1.30", Advanced: true},
				},
			},
			{
				Code: "APG", Name: "App gateway", TofuType: "azurerm_application_gateway",
				// An application gateway needs a subnet of its own, which is why it
				// carries one as a companion rather than sharing the account's.
				Network: NetFirewalled,
				Fixed: []Fixed{
					{Key: "name", Expr: `"{{asset-dashed}}"`},
					{Key: "resource_group_name", Expr: "azurerm_resource_group.{{account}}.name"},
					{Key: "location", Expr: "azurerm_resource_group.{{account}}.location"},
					{Block: "sku", Key: "name", Expr: `"Standard_v2"`},
					{Block: "sku", Key: "tier", Expr: `"Standard_v2"`},
					{Block: "gateway_ip_configuration", Key: "name", Expr: `"gateway-ip"`},
					{Block: "gateway_ip_configuration", Key: "subnet_id", Expr: "azurerm_subnet.{{asset}}_subnet.id"},
					{Block: "frontend_port", Key: "name", Expr: `"http"`},
					{Block: "frontend_port", Key: "port", Expr: "80"},
					{Block: "frontend_ip_configuration", Key: "name", Expr: `"frontend"`},
					{Block: "frontend_ip_configuration", Key: "public_ip_address_id", Expr: "azurerm_public_ip.{{asset}}_pip.id"},
					{Block: "backend_address_pool", Key: "name", Expr: `"backend"`},
					{Block: "backend_http_settings", Key: "name", Expr: `"settings"`},
					{Block: "backend_http_settings", Key: "cookie_based_affinity", Expr: `"Disabled"`},
					{Block: "backend_http_settings", Key: "port", Expr: "80"},
					{Block: "backend_http_settings", Key: "protocol", Expr: `"Http"`},
					{Block: "backend_http_settings", Key: "request_timeout", Expr: "60"},
					{Block: "http_listener", Key: "name", Expr: `"listener"`},
					{Block: "http_listener", Key: "frontend_ip_configuration_name", Expr: `"frontend"`},
					{Block: "http_listener", Key: "frontend_port_name", Expr: `"http"`},
					{Block: "http_listener", Key: "protocol", Expr: `"Http"`},
					{Block: "request_routing_rule", Key: "name", Expr: `"rule"`},
					{Block: "request_routing_rule", Key: "priority", Expr: "1"},
					{Block: "request_routing_rule", Key: "rule_type", Expr: `"Basic"`},
					{Block: "request_routing_rule", Key: "http_listener_name", Expr: `"listener"`},
					{Block: "request_routing_rule", Key: "backend_address_pool_name", Expr: `"backend"`},
					{Block: "request_routing_rule", Key: "backend_http_settings_name", Expr: `"settings"`},
				},
				Companions: []Companion{
					{
						TofuType: "azurerm_public_ip", Suffix: "pip",
						Fixed: []Fixed{
							{Key: "name", Expr: `"{{asset-dashed}}-pip"`},
							{Key: "resource_group_name", Expr: "azurerm_resource_group.{{account}}.name"},
							{Key: "location", Expr: "azurerm_resource_group.{{account}}.location"},
							{Key: "allocation_method", Expr: `"Static"`},
							{Key: "sku", Expr: `"Standard"`},
						},
					},
					{
						TofuType: "azurerm_subnet", Suffix: "subnet",
						Fixed: []Fixed{
							{Key: "name", Expr: `"{{asset-dashed}}-subnet"`},
							{Key: "resource_group_name", Expr: "azurerm_resource_group.{{account}}.name"},
							{Key: "virtual_network_name", Expr: "azurerm_virtual_network.{{account}}.name"},
							{Key: "address_prefixes", Expr: `["10.0.3.0/24"]`},
						},
					},
				},
				Params: []ParamField{
					{Key: "capacity", Block: "sku", Label: "Capacity units", Type: FieldNumber, Default: 2},
				},
			},
			{
				Code: "CDN", Name: "Azure CDN", TofuType: "azurerm_cdn_endpoint",
				// AzureFrontDoor.Backend is an NSG service tag, so it only protects an
				// Azure origin. A non-Azure origin has no IP-based control at all.
				Network: NetEdge, AddressAttr: "fqdn", AddressKind: AddrHostname,
				Fixed: []Fixed{
					{Key: "name", Expr: `"{{asset-dashed}}"`},
					{Key: "resource_group_name", Expr: "azurerm_resource_group.{{account}}.name"},
					{Key: "location", Expr: `"global"`},
					{Block: "origin", Key: "name", Expr: `"origin"`},
					{Block: "origin", Key: "host_name", Expr: "var.{{asset}}_origin_host"},
				},
				Companions: []Companion{{
					TofuType: "azurerm_cdn_profile", Suffix: "profile",
					Fixed: []Fixed{
						{Key: "name", Expr: `"{{asset-dashed}}-profile"`},
						{Key: "resource_group_name", Expr: "azurerm_resource_group.{{account}}.name"},
						{Key: "location", Expr: `"global"`},
						{Key: "sku", Expr: `"Standard_Microsoft"`},
					},
					ParentRef:  "profile_name",
					ParentExpr: "azurerm_cdn_profile.{{asset}}_profile.name",
				}},
				Variables: []Variable{{
					Name: "{{asset}}_origin_host", Description: "Origin host the CDN fetches from",
					Default: `"origin.example.com"`,
				}},
				Params: []ParamField{
					{Key: "is_https_allowed", Label: "Allow HTTPS", Type: FieldBoolean, Default: true, Advanced: true},
					{Key: "is_http_allowed", Label: "Allow plain HTTP", Type: FieldBoolean, Default: false, Advanced: true},
				},
			},
		},
	})
}
