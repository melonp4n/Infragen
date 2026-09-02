# OpenTofu mapping

**Status: complete.** `terraform init && terraform validate` passes on a chart containing every one
of the 26 resource types, checked against real provider schemas. `INVALID-PARAMS.md` records what
the parameter tables used to get wrong.

This file records the decisions and why, so they do not have to be rediscovered.

## Decisions

| Question | Decision |
|---|---|
| State layout | **One configuration, one state, provider aliases per account.** |
| Cross-account rules | Reference the peer resource's address attribute directly. |
| Same-account rules | Prefer a security-group reference over a CIDR. |
| Non-firewalled assets | Classified in the catalog. Emit IAM or a comment — **never a firewall rule.** |
| CDN edges | Native prefix list or service tag where one exists; otherwise origin config plus an explicit warning. |
| Ephemeral addresses | **Refuse the rule and warn.** The user opts into a stable address via a toggle. |
| Resource addresses | Derived from `Asset.ID`, **never from display names.** |
| Companion resources | **Declared in the catalog**, emitted automatically, named `<assetID>_<suffix>`. |
| Credentials | Provider-managed where it exists, sensitive variables elsewhere. **No secret in the file or in state.** |
| Security posture | Expressed as the `Default` on ordinary editable fields. No warning machinery. |

### On security defaults

Every field is editable. Security is expressed entirely through which value sits in `Default` —
encryption on, TLS 1.2, IMDSv2 required, public access blocked. Nothing marks a field as
security-relevant at runtime and nothing checks whether the user changed it.

A deviation-warning mechanism was designed and cut: a `SecureValue` field, a per-field risk string,
drawer warnings, generation warnings and a forced-open disclosure — all to tell the user something
they had chosen to do deliberately. The default already puts them in the right place, and the risk
strings would have been the first thing to go stale as platforms changed.

Planned but out of scope: **DNS management.** A managed name would replace IP references and change
the ephemeral-address problem materially. Keep address resolution behind one function.

## Resource types

Each `ResourceType` carries the OpenTofu type it becomes.

### AWS
| Code | Resource |
|---|---|
| `EC2` | `aws_instance` |
| `λ` | `aws_lambda_function` |
| `RDS` | `aws_db_instance` |
| `S3` | `aws_s3_bucket` |
| `ALB` | `aws_lb` |
| `ECS` | `aws_ecs_service` |
| `CDN` | `aws_cloudfront_distribution` |

### Azure
| Code | Resource |
|---|---|
| `VM` | `azurerm_linux_virtual_machine` |
| `FN` | `azurerm_linux_function_app` |
| `BLB` | `azurerm_storage_account` |
| `SQL` | `azurerm_mssql_database` |
| `AKS` | `azurerm_kubernetes_cluster` |
| `APG` | `azurerm_application_gateway` |
| `CDN` | `azurerm_cdn_endpoint` |

### GCP
| Code | Resource |
|---|---|
| `GCE` | `google_compute_instance` |
| `GCF` | `google_cloudfunctions2_function` |
| `GCS` | `google_storage_bucket` |
| `CSQL` | `google_sql_database_instance` |
| `GKE` | `google_container_cluster` |
| `CDN` | `google_compute_backend_bucket` |

### DigitalOcean
| Code | Resource |
|---|---|
| `DRP` | `digitalocean_droplet` |
| `APP` | `digitalocean_app` |
| `SPC` | `digitalocean_spaces_bucket` |
| `DB` | `digitalocean_database_cluster` |
| `K8S` | `digitalocean_kubernetes_cluster` |
| `LB` | `digitalocean_loadbalancer` |
| `CDN` | `digitalocean_cdn` |

## Companion resources

13 of the 26 types cannot validate without another resource being created alongside them. These are
declared in the catalog as `ResourceType.Companions` and emitted automatically, named
`<assetID>_<suffix>` so they inherit the rename-stability guarantee.

### AWS

| Type | Companions | Why |
|---|---|---|
| `aws_lambda_function` | `aws_iam_role`, `aws_iam_role_policy_attachment`, a deployment package | `role` must be an ARN; one of `filename` / `s3_*` / `image_uri` is required |
| `aws_db_instance` | `aws_db_subnet_group`, **a second `aws_subnet` in another AZ** | a subnet group needs ≥2 availability zones |
| `aws_s3_bucket` | `aws_s3_bucket_public_access_block`, `aws_s3_bucket_server_side_encryption_configuration`, `aws_s3_bucket_versioning` | not needed to validate, needed for the secure default; provider v4 split these out |
| `aws_lb` | **a second `aws_subnet` in another AZ** | an ALB requires subnets in ≥2 AZs |
| `aws_ecs_service` | `aws_ecs_cluster`, `aws_ecs_task_definition` | `task_definition` is required; the task definition needs `family` and `container_definitions` |

### Azure

| Type | Companions | Why |
|---|---|---|
| `azurerm_linux_virtual_machine` | `azurerm_network_interface` | `network_interface_ids` is required. This companion is also what unblocks NSG attachment |
| `azurerm_linux_function_app` | `azurerm_service_plan`, `azurerm_storage_account` | `service_plan_id` and storage credentials are required |
| `azurerm_mssql_database` | `azurerm_mssql_server` | `server_id` is required |
| `azurerm_application_gateway` | `azurerm_public_ip`, **a dedicated `azurerm_subnet`** | a gateway must sit in its own subnet; eight nested blocks are required |
| `azurerm_cdn_endpoint` | `azurerm_cdn_profile` | `profile_name` is required. Classic CDN is retiring — `azurerm_cdn_frontdoor_*` is the current path |

### GCP

| Type | Companions | Why |
|---|---|---|
| `google_cloudfunctions2_function` | `google_storage_bucket`, `google_storage_bucket_object` | `build_config.source` must point at an object in a bucket |
| `google_container_cluster` | `google_container_node_pool` with `remove_default_node_pool = true` | machine type and node count are node-pool concerns |
| `google_compute_backend_bucket` | `google_storage_bucket` | `bucket_name` is required |

### DigitalOcean

| Type | Companions | Why |
|---|---|---|
| `digitalocean_cdn` | `digitalocean_spaces_bucket` | `origin` must be a Spaces endpoint |
| `digitalocean_droplet` | `digitalocean_ssh_key` (optional) | the secure default: key auth instead of an emailed root password |

The heaviest are `azurerm_application_gateway` (2 companions, 8 blocks), `aws_ecs_service` and
`azurerm_linux_function_app` (2 companions each).

## Nested blocks

Most required arguments are not top-level. `ParamField.Block` carries a dot path to the containing
block — `boot_disk.initialize_params`, `settings`, `default_node_pool` — and the writer groups
arguments by that path when emitting. The drawer stays a flat list of rows; only the emitter cares
about nesting.

`ResourceType.Fixed` holds required arguments with exactly one sensible answer and no user opinion:
a CloudFront `origin_id`, a task definition `family`. **Never a security setting** — those are
ordinary editable fields whose `Default` is the secure value.

Arguments are collected into a `blockNode` tree and rendered in one pass, so **arguments always
precede nested blocks** regardless of declaration order. That is not cosmetic: a block appearing
between two arguments would split them into separate alignment runs and change the formatting.

### Placeholders in catalog expressions

`Fixed.Expr`, `Companion.ParentExpr` and `Variable.Name` are static strings in the catalog, but they
often need to name account-scoped resources. Three placeholders are substituted at emit time:

| Placeholder | Becomes |
|---|---|
| `{{account}}` | the account ID, e.g. `acc_1` |
| `{{asset}}` | the asset ID, e.g. `asset_3` |
| `{{region}}` | `var.<accountID>_region` |

`{{ }}` rather than `${ }` because the latter is HCL's own interpolation and the two would be
indistinguishable in the output. An expression may contain both — `"${aws_eip.{{asset}}.public_ip}/32"`
expands the placeholder and leaves the HCL reference intact.

### Companions

`Companion.Suffix` names the resource `<assetID>_<suffix>`, so companions inherit the guarantee that
renaming an asset never moves a resource address. `ParentRef` and `ParentExpr` are the argument on
the parent that points at the companion, and must be set together — a companion nothing references
is dead HCL, and `TestEveryCompanionIsReferenced` fails on it.

### Provider blocks and names

`Provider.Config` is what goes inside the provider block, because the clouds disagree: AWS takes a
`region`, azurerm requires an empty `features {}` and no region at all, Google needs a `project`.
`Provider.NameArg` and `LabelArg` carry the generated name and the display label — AWS and Azure
take `tags`, GCP and DigitalOcean take a `name`, and `ResourceType.OmitName` suppresses it for the
few resources with no name argument at all (a DigitalOcean app names itself inside its spec block).

Azure declares its name per type rather than sharing one: a storage account name takes no hyphens
and is capped at 24 characters, while a VM name does take them.

### Variables

`ResourceType.Variables` declares values the chart cannot supply. Sensitive ones are emitted with
**no default**, so `tofu plan` prompts rather than a secret living in the configuration. Names are
deduplicated after placeholder expansion, so one variable per account rather than one per asset.

## Credentials

No secret reaches the configuration or the state file.

| Provider | Approach |
|---|---|
| AWS RDS | `manage_master_user_password = true`. AWS creates and rotates it in Secrets Manager; no `password` argument is emitted at all. This is AWS's own recommendation |
| Azure SQL server, Azure VM | `variable` marked `sensitive = true` with no default, so `tofu plan` prompts. VMs also get `disable_password_authentication = true` and an `admin_ssh_key` block |
| DigitalOcean droplets | `ssh_keys` from a variable, never a password |

`random_password` was rejected: it applies without prompting, but the secret then lives in state.

## Secure defaults

Applied as the `Default` on ordinary editable fields.

| Area | Setting |
|---|---|
| AWS EC2 | `metadata_options { http_tokens = "required" }` (IMDSv2), `root_block_device { encrypted = true }` with the block managed by default, no public IP |
| AWS S3 | public access block all four flags, SSE encryption, versioning enabled |
| AWS RDS | `storage_encrypted = true`, `publicly_accessible = false`, `manage_master_user_password = true`, `auto_minor_version_upgrade = true` |
| AWS ALB | `drop_invalid_header_fields = true` |
| Azure storage | `min_tls_version = "TLS1_2"`, `https_traffic_only_enabled = true`, `allow_nested_items_to_be_public = false`, `public_network_access_enabled = false` |
| Azure VM | `disable_password_authentication = true` with `admin_ssh_key` |
| Azure SQL | `minimum_tls_version = "1.2"`, `public_network_access_enabled = false` on the server |
| GCP storage | `uniform_bucket_level_access = true`, `public_access_prevention = "enforced"` |
| GCP Cloud SQL | `settings.ip_configuration.ipv4_enabled = false`, backups enabled, SSL required |
| GCP GKE | `remove_default_node_pool = true`, workload identity, shielded nodes |
| GCE | no external IP unless the static-address toggle is on, shielded VM config |
| DigitalOcean | droplet `ssh_keys` not password, `monitoring = true`, Spaces `acl = "private"` |

### `root_block_device` is not always valid

A container-backed or instance-store AMI has no root EBS volume, and Terraform **rejects
`root_block_device` on one** rather than ignoring it. So the two fields inside it are gated on a
`Manage root volume` directive (`ParamRootBlockDevice`), and the whole block is omitted when it is
off.

The gate defaults **on**, which matters here: `encrypted = true` is the secure default in the table
above, and a gate defaulting off would quietly drop root volume encryption from every new instance.
The AMI that cannot take the block is the minority case, so it is the one that opts out.

`ParamField.RequiresParam` is the mechanism, and it hides the fields in the drawer as well as
omitting the arguments — see `CATALOG.md`.

### Destruction controls

Defaulted for teardown, since that is what this tool is for. Editable, under Advanced.

| Field | Default | Note |
|---|---|---|
| `force_destroy` (S3, GCS, Spaces) | `true` | a bucket with objects blocks `destroy` otherwise |
| `skip_final_snapshot` (RDS) | `true` | a snapshot on every teardown accumulates cost |
| `deletion_protection` (RDS, Cloud SQL, GKE) | `false` | on means teardown needs a console visit |
| `backup_retention_period` (RDS) | `0` | non-zero blocks `skip_final_snapshot` |

## Not every asset is firewallable

This is the most important thing on this page, because getting it wrong produces output that
looks correct and controls nothing.

Three distinct kinds of resource are easy to conflate:

| Kind | Reached via | Access governed by | Types |
|---|---|---|---|
| `NetFirewalled` | an IP inside a VPC or VNet | security group, NSG, VPC firewall, DO firewall | `EC2` `RDS` `ALB` `ECS` `VM` `SQL` `APG` `AKS` `GCE` `CSQL` `GKE` `DRP` `DB` `K8S` `LB` |
| `NetServiceEndpoint` | a public service endpoint | IAM or resource policy — **there is no firewall** | `S3` `λ` `BLB` `FN` `GCS` `GCF` `SPC` `APP` |
| `NetEdge` | a CDN hostname | origin configuration plus header or token auth; **no stable egress IP** | `CDN` in all four providers |

Consequences:

- **`Lambda → S3 on 443` must not become a security-group rule.** It is in `model.Seed()`, so it
  is the first thing a generator will hit. Access is IAM-governed and the port is irrelevant.
- **`Internet → CloudFront` is not a rule either.** A distribution is public by construction. The
  honest output is a comment recording the intent.
- **`CloudFront → ALB` uses a managed prefix list**, not a CIDR.
- **Only `NetFirewalled` → `NetFirewalled` edges become real firewall rules.**

Emitting nothing is better than emitting a rule that controls nothing, because the second one
looks like the tool did its job.

## Address mapping and dependency ordering

These are the same mechanism. A reference to another resource's attribute **is** the dependency
edge, so the peer is created first with no `depends_on`:

```hcl
resource "aws_vpc_security_group_ingress_rule" "web_from_droplet" {
  provider          = aws.acct1
  security_group_id = aws_security_group.web.id
  ip_protocol       = "tcp"
  from_port         = 8080
  to_port           = 8080
  # Both the address lookup and the ordering guarantee, in one expression.
  cidr_ipv4 = "${digitalocean_droplet.app.ipv4_address}/32"
}
```

This works across providers because it is all one graph — OpenTofu does not care that one
resource is DigitalOcean and the other AWS. That is the main reason for the single-configuration
decision.

`depends_on` is only for dependencies not expressed by a reference. Reaching for it here almost
always means a reference was missed.

### Address attributes, and which are stable

| Resource | Attribute | Stable? |
|---|---|---|
| `aws_instance` | `public_ip` / `private_ip` | **No** — changes on stop/start |
| `aws_eip` | `public_ip` | Yes |
| `aws_lb` | `dns_name` | No IP at all; an NLB can get static IPs via `subnet_mapping` |
| `aws_db_instance` | `address`, `port` | Hostname, not an IP |
| `azurerm_public_ip` | `ip_address` | Only with `allocation_method = "Static"` |
| `azurerm_linux_virtual_machine` | `public_ip_address` / `private_ip_address` | Reflects the attached public IP |
| `google_compute_address` | `address` | Yes |
| `google_compute_instance` | `network_interface[0].access_config[0].nat_ip` | **No** unless a `google_compute_address` is attached |
| `digitalocean_droplet` | `ipv4_address` / `ipv4_address_private` | Yes, for the droplet's life |
| `digitalocean_database_cluster` | `host`, `private_host`, `port` | Hostname |
| `digitalocean_loadbalancer` | `ip` | Yes |

**Security groups cannot take hostnames.** Where the only attribute is a DNS name, an IP-based
rule is not possible — report it, do not approximate it.

**A rule against an ephemeral address rots silently.** The address changes on restart, the rule
keeps the old value, and connectivity breaks with no signal. So generation refuses that rule and
names the fix, rather than emitting it with a warning comment nobody reads.

## Firewall models — why generation is four jobs

The four providers do not agree on what a firewall rule attaches to:

| Provider | Model | Attaches to | Notes |
|---|---|---|---|
| **AWS** | `aws_security_group` plus `aws_vpc_security_group_ingress_rule` / `..._egress_rule` | instances, load balancers, RDS | a rule's source can be another security group ID — the cleanest expression of an internal connection |
| **Azure** | `azurerm_network_security_group` with `azurerm_network_security_rule` | subnets or NICs, not resources directly | rules are **priority-ordered**; two at the same priority is an apply error, so priorities must be allocated deterministically |
| **GCP** | `google_compute_firewall` | the **VPC network**, targeting by network tag | there is no per-instance group; rules match tags, so assets need generated tags |
| **DigitalOcean** | `digitalocean_firewall` | droplets by ID or tag | inbound and outbound are lists **inside one resource**, so rules must be accumulated per firewall and emitted once |

The DigitalOcean shape is the one that breaks a naive design: you cannot emit one HCL block per
rule. Whatever interface the emitters use must let a provider accumulate rules and emit at the
end. Design for that from the start.

Each provider also needs its own network scaffolding before anything applies: AWS a VPC, subnets,
internet gateway and route table; Azure a resource group, virtual network and subnet; GCP a
network and subnetwork; DigitalOcean a VPC.

## CDNs

A CDN has no stable egress IP — origin fetches come from a large, changing edge fleet. Each
provider solves this only inside its own firewall:

| Edge service | Origin-side control | Works for a non-native origin? |
|---|---|---|
| CloudFront | `data "aws_ec2_managed_prefix_list"` named `com.amazonaws.global.cloudfront.origin-facing`, used as `prefix_list_id` | AWS origins only |
| Azure Front Door / CDN Standard from Microsoft | `source_address_prefix = "AzureFrontDoor.Backend"` in an NSG rule | Azure origins only |
| Azure CDN classic (Verizon / Akamai) | No service tag exists | No |
| GCP Cloud CDN | LB and health-check ranges `130.211.0.0/22`, `35.191.0.0/16` | GCP origins only |
| DigitalOcean CDN | Fronts Spaces only; an external origin is not a supported configuration | N/A |

### The cross-cloud case: Azure CDN → EC2

**There is no valid IP-based control.** Azure publishes its service-tag IP ranges as JSON, but
they change weekly and there is no OpenTofu data source for them, so a generated rule is stale on
arrival.

The control that actually works is authenticating the edge rather than filtering by address:
validate the `X-Azure-FDID` header over HTTPS at the load balancer or in the application. That is
Microsoft's own guidance, and it is not a firewall rule.

So: **a CDN-to-origin edge is origin configuration, not a firewall rule.** Use the native prefix
list or service tag where the provider offers one. Where it does not, emit the origin
configuration plus a commented `variable` and an explicit warning. Never fabricate a CIDR.

## Connection strategies

The generator maps each `model.Connection` to one strategy:

| A → B | Strategy |
|---|---|
| firewalled → firewalled, same account and VPC | security-group reference — no IP, cannot rot |
| firewalled → firewalled, cross account or cloud | CIDR from the peer's address attribute; **refuse if not stable** |
| edge → firewalled, same provider | native prefix list or service tag |
| edge → firewalled, cross provider | origin config + commented variable + warning |
| internet → edge | comment only; a CDN is public by construction |
| anything ↔ service endpoint | IAM policy or comment; never a firewall rule |
| endpoint whose only attribute is a hostname | refuse and report |

Refusals are returned to the caller as warnings and shown above the generated output. A refusal
the user never sees is the same failure as a silently broken rule.

## Repeated apply — the chart gets edited

Charts are not write-once. A branch gets torn down and another stood up: remove a Droplet, add an
Azure VM in its place, and `tofu apply` does the work from state. Generation therefore runs many
times against an evolving chart.

### Resource addresses must derive from `Asset.ID`

If an address comes from the display name — as the original prototype's `slugify(asset.name)` did
— then renaming "Web tier EC2" to "Web tier" changes the address and **`apply` destroys and
recreates the instance.** Anything positional is just as bad: inserting an asset would shift
every address after it.

Addresses must contain only stable parts:

```hcl
# AWS Account 1 → Web tier EC2
resource "aws_instance" "asset_2c8b0d1e6a7f3924" {
  provider = aws.acct1
  tags     = { Name = "Web tier EC2" }
}
```

Readability comes from the comment and the `Name` tag, not from the address. Rename freely and
the plan stays empty.

This makes **`Asset.ID` load-bearing for infrastructure identity**, not just for the UI. Changing
how IDs are minted would now move every resource in every existing session.

### Removal is a destroy

Delete an asset and it leaves the generated config, so `apply` destroys it. That is the intended
behaviour, and it is clean only with a single state — with separate states it would need the
right state applied in the right order to avoid orphaning.

`dropDanglingConnections()` in `app.js` already removes connections whose endpoints are gone, so
rules referencing a deleted asset disappear with it.

### Two lifecycle traps

**Removing the last asset of a provider.** If the provider block and its last resource disappear
in the same apply, OpenTofu cannot destroy the resource — it fails with the provider
configuration missing. This is documented rather than engineered around: empty the account of
assets, apply, and only then remove the account. Keeping an empty account on the chart keeps its
provider block, which is the natural fix. The generated file header should say so.

**Destroying stateful resources is data loss.** Removing an `RDS`, `CSQL`, `DB` or a bucket
destroys what is in it. The warning surface must name the resource and say what will be lost; the
existing `force_destroy` and `backups` params are the relevant context to show alongside.

## User data

A startup script on the four compute types. Each cloud spells it differently, and the differences are
carried as catalog data rather than emitter branches:

| Type | Argument | Wrap |
|---|---|---|
| `EC2`, `DRP` | `user_data` | — |
| `GCE` | `metadata_startup_script` | — |
| `VM` | `custom_data` | `base64encode(%s)` |

Google's is a top-level argument, not an entry in the `metadata` map — the map would have needed
special handling, the top-level one does not.

**An unset script is omitted, not emitted as `""`.** `user_data = ""` is noise and
`base64encode("")` is worse, so `assetResource` skips a `FieldScript` param whose value is empty.

`quote()` escapes newlines to `\n`, because a literal newline inside an HCL quoted string is a parse
error. `terraform validate` on a chart carrying scripts is what proves that, and a golden file would
happily record broken output — so that check lives in the integration test.

## Ansible inventory

An asset with the Ansible directive on lands in an inventory, grouped by
`ansible_group`. **The inventory is written by Terraform, not by infrachart** — it needs real
addresses, and those do not exist until after apply, so it is a `local_file` whose content
interpolates each host's address expression.

```hcl
resource "local_file" "ansible_inventory" {
  filename        = "${path.cwd}/inventory.ini"
  file_permission = "0600"
  content         = <<-EOT
    [web]
    # AWS Account 1 → Web tier EC2
    ${aws_eip.asset_3.public_ip} ansible_user=ec2-user
    ...
  EOT
}
```

A `.gitignore` covering the inventory and the state file is emitted alongside.

**It is a skeleton and says so.** `ansible_user` defaults to the login the provider's stock image
creates (`ec2-user`, `azureuser`, `root`) and is editable per host.
`ansible_ssh_private_key_file` is openly labelled a guess, since infrachart never sees the private
key. Ansible configuration is the user's own.

### Two things to preserve

**Group names are validated as INI section names** (`groupName`). The group reaches a heredoc where
`quote()` does not apply, so it is the injection surface. There are two independent layers:
`Normalise` strips control characters from a text param, and the section-name check rejects
brackets, spaces and `${`. Keep both — the first is incidental, the second is the deliberate one.

**Inventory addresses follow different rules from firewall rules, and `hostAddress` must not be
merged back into `addressExpr`.** They look like the same question and are not:

| | Firewall rule | Inventory |
|---|---|---|
| Lifetime | persists in state | rewritten on every apply |
| Ephemeral IP | **refused** — the rule goes stale as the address moves | **fine** — always current |
| Hostname | refused — a security group needs an address | fine — Ansible connects to names |
| No address attribute | refused | refused; nothing to write |
| Attribute that exists but is never populated | refused | **refused** — see below |

Merging them was a real bug: an AWS-only chart with Ansible enabled produced **no inventory at all**,
because an EC2 without a static IP was rejected on the firewall rule's grounds. A durable address is
still preferred where one exists, so the inventory does not churn between applies, but an ephemeral
one is used rather than refused.

### An unpopulated address attribute is not an address

An address attribute is only populated when the resource actually has that kind of address, and
where it does not, Terraform yields an **empty string rather than an error**. So a reference that
looks correct writes an inventory line with nothing in the address column, and the apply succeeds.

`ResourceType.AddressRequires` names the boolean param that has to be on first. Three types need it:

| Type | Requires | Why |
|---|---|---|
| `aws_instance` | `associate_public_ip_address` | `public_ip` is empty on an instance with no public address, and that is the default |
| `azurerm_linux_virtual_machine` | `static_public_ip` | the generated NIC only gets `public_ip_address_id` when the static toggle is on |
| `google_compute_instance` | `static_public_ip` | without an `access_config` block there is no public address, and `access_config[0]` would index a block that does not exist |

`hostAddress` refuses when the gate is off and the warning names the **field label** — "turn on
`Assign public IP`" — because that is what the user clicks. An HCL argument name is not something
they ever see in the drawer.

The static toggle now attaches the address as well as allocating it. It previously emitted the
address resource with a comment saying to attach it by hand, which meant enabling the toggle on
Azure or GCP produced a reserved address that nothing used. The attachment is a `Fixed` with
`RequiresParam` set, and `companionResource` honours that gate — it did not, which is how the Azure
case survived.

### Providers that come from features, not accounts

`requiredProviders` walks accounts, but the inventory needs `hashicorp/local` and no account is a
"local" account. `featureProviders` supplies those. The declaration and the resource are computed
from the same source and asserted to agree, because a `local_file` with no provider declared — or a
provider declared with nothing using it — are both broken.

## SSH keys — and why nothing generates one

**infrachart never creates an SSH keypair, and neither does the generated configuration.** A public
key is something the user supplies; the private key never reaches the app, the configuration or the
state file.

That is a deliberate position, reached after ruling out the obvious alternative.

### Ephemeral resources cannot supply a keypair

`ephemeral "tls_private_key"` exists, and the idea is appealing: generate the key at plan time, keep
the private half ephemeral so it never persists, use the public half. It does not work, for three
independent reasons. Verified against Terraform 1.16 and the current providers, so this does not have
to be rediscovered:

1. **Ephemerality is per-resource, not per-attribute.** Everything an `ephemeral` block emits is
   ephemeral. "Private ephemeral, public persisted" cannot be expressed.

2. **Nothing can receive the public key.** Ephemeral values may only flow into *write-only*
   attributes. Feeding one to `local_file.content`, or to a `data "tls_public_key"` argument to
   launder it, both fail:

   > Ephemeral values are not valid for "content", because it is not a write-only attribute and must
   > be persisted to state.

   A data source does not help — its results are persisted too. A sweep of all six providers found
   write-only attributes on 26 resources, every one a secret *sink*: `password_wo`,
   `secret_string_wo`, `private_key_wo` for certificates, `value_wo` for secret stores. **None on
   `aws_key_pair`, `aws_instance`, or anything that installs a public key.**

3. **The key differs on every run.** Ephemeral resources persist nothing — `terraform apply` leaves
   `resources: []` — and are re-opened each operation. A keypair whose public half must stay
   installed on a machine is the opposite of ephemeral.

The underlying constraint: **something has to remember the private key between runs** — Terraform
state, a file on disk, or the user. Ephemeral resources are built for values where the answer is
"nobody", such as a fetched auth token passed to a provider block. A durable SSH identity is not
that.

So a generated keypair whose private half is actually usable must be persisted somewhere, and
`tls_private_key` persists it in `terraform.tfstate` in plaintext. Rather than accept that, the key
is the user's to create and supply.

### How a key is resolved

Six sources, first non-empty wins. The four chart ones — two on the asset, two on the account —
resolve to literals here, because their values are in the session. The last two are Terraform
variables, because infrachart cannot see what they will be set to.

| Source | Emits |
|---|---|
| `ssh_public_key` on the asset — pasted | the literal string |
| `ssh_public_key_file` on the asset — a path | `file("/path/to/key.pub")` |
| `ssh_public_key` on the account — pasted | the literal string |
| `ssh_public_key_file` on the account — a path | `file("/path/to/key.pub")` |
| `var.<account>_ssh_public_key` | the account default, set at apply time |
| `var.ssh_public_key` | the deployment default, set at apply time |

```hcl
coalesce(file("/path/to/key.pub"), "ssh-ed25519 AAAA…", var.acc_1_ssh_public_key, var.ssh_public_key)
```

Only the sources actually set appear, so a host in an account with no key of its own emits just
the two variables. Setting both per-resource fields is a warning rather than a silent precedence
rule.

The account key also decides whether the no-key precondition is emitted at all. When the account
supplies one, every host in it resolves a key at generation time, so the plan-time check would only
ever be a false alarm.

**Every machine gets key wiring, whether or not Ansible is involved.** Wanting to SSH into a host is
not the same want as wanting Ansible to configure it, and key wiring was originally gated on the
Ansible flag — which made the first impossible without the second.

The gate is now whether a key actually resolves, and that cannot be decided at generation time
because a key may come from a Terraform variable. So it is decided at plan time:

```hcl
locals {
  asset_3_ssh_key = coalesce(var.acc_1_ssh_public_key, var.ssh_public_key, "")
}

resource "aws_key_pair" "asset_3_key" {
  count      = local.asset_3_ssh_key != "" ? 1 : 0
  public_key = local.asset_3_ssh_key
}

resource "aws_instance" "asset_3" {
  key_name = one(aws_key_pair.asset_3_key[*].key_name)
}
```

Three things there are load-bearing:

- **The trailing `""` in the coalesce.** Without it, `coalesce` fails when every source is empty. A
  host with no key is a host nobody wants to SSH into, not an error.
- **The local.** `count` and the value must read the same resolution, and repeating a
  four-argument coalesce at every use would be a place for them to drift apart.
- **`one(...)` rather than a direct reference.** A counted resource cannot be referenced directly,
  and `one()` yields `null` when the count is zero — which is exactly "no key" for `key_name`.

`Companion.Count` exists for this, and is the only place a count meta-argument is emitted.

The key variables are declared wherever `{{sshkey}}` is **actually referenced** (`usesSSHKey`), never
from a flag that usually implies it. That distinction was a real bug: declaring them from the Ansible
flag left a plain Azure VM — which needs a key regardless — referencing variables that did not
exist.

| Provider | Wiring |
|---|---|
| AWS | `aws_key_pair` companion, `key_name` on the instance |
| Azure | `admin_ssh_key.public_key`, unconditional |
| GCP | `metadata = { ssh-keys = "ansible:<key>" }` |
| DigitalOcean | `digitalocean_ssh_key` companion, `ssh_keys` |

**A host with no key anywhere fails at plan time, not generation time.** infrachart cannot know
whether `TF_VAR_ssh_public_key` is set, so a generation-time error would be a guess. A
`lifecycle.precondition` on each Ansible host produces a better message anyway, because it names the
asset and the command to run.

## Hard rules for the generator

- **Run `model.Validate` then `model.Normalise` first.** Params become HCL argument values, and
  normalisation is what guarantees they match declared types and carry no stray keys.
- **Do not build HCL with templ.** It escapes for HTML — quotes become `&quot;`. Use
  `text/template` or plain string building in `internal/tofu`.
- **Quote and escape string values.** Even after normalisation these are user-supplied strings
  landing in a config file.
- **Iterate `ResourceType.Arguments()`, never `Params`.** `Params` includes generator directives
  such as `static_public_ip`, which are not arguments on any resource — emitting one produces
  invalid HCL. `Arguments()` filters them out in one place so no emitter has to remember.
- **Output must be deterministic.** Iterate `catalog.All()` (sorted by key) and the session's
  slices in order; never range over a map. Golden-file tests and readable diffs both depend on it.
- **Reuse, do not reimplement.** `internetTraffic()` currently lives in `internal/ui/drawer.go`
  and does exactly the inbound/outbound classification the generator needs — move it to
  `internal/model` and have both callers use it. Two implementations of "is this publicly
  reachable" drifting apart is the worst bug this tool could have. Also reuse
  `model.Session.Label()`, `model.ParseCIDR()`, `catalog.All()` and `catalog.Type()`.
- **External IPs are never resources.** They contribute a CIDR to rules and nothing else.
- **An empty rule list means blocked.** Emit nothing for that direction — no deny-all, no
  permissive fallback.
- **A rule's port is always the destination port.** The port being connected *to*, in both
  directions. Source ports are ephemeral and never constrained.

  AWS's field names invite a misreading: `from_port` and `to_port` are the **low and high bounds of
  one range**, not a source and a destination. `from_port = 443, to_port = 443` is the range 443–443
  applied to the destination. The other three name it unambiguously — Azure sets
  `source_port_range = "*"` alongside `destination_port_range`, GCP's `allow { ports }` and
  DigitalOcean's `port_range` are both destination-side.

  All four firewalls are **stateful**, so return traffic is permitted automatically and no ephemeral
  port range is ever emitted. That would only be needed for stateless network ACLs, which this does
  not generate.
- **An all-ports TCP or UDP rule still needs an explicit range on AWS.** `from_port = 0` and
  `to_port = 65535`, because AWS rejects a tcp rule with no ports at apply time and `validate` does
  not catch it — the schema marks both optional. Azure uses `*`, GCP omits the ports list, and
  DigitalOcean uses `"all"`.

## Verification

- Golden-file tests in `internal/tofu`, one per strategy row, driven from `model.Seed()` plus a
  fixture containing a cross-cloud CDN edge and an ephemeral-address peer.
- **Assert no `NetServiceEndpoint` asset ever appears in a firewall rule.** This is the
  regression most likely to creep back in, because it looks like a missing feature.
- Assert a cross-account rule against an asset without a stable address yields a warning and no
  rule.
- **Rename stability:** generate, rename an asset, generate again, and assert the only difference
  is the comment and the `Name` tag — every resource address byte-identical. This is the test that
  stops a rename destroying infrastructure.
- **Swap stability:** generate, replace a Droplet with an Azure VM, generate again, and assert
  only the droplet's resources and the rules referencing it disappear.
- Write output to a temp directory and run `tofu init && tofu validate`. Never `tofu apply` in an
  automated check.
- `tofu graph` should show each peer address resource upstream of the rule referencing it — that
  is how the ordering claim gets checked rather than assumed.
