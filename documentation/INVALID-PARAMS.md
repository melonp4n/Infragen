# Invalid parameters — rollback record

**This is a history file. Do not delete it when the rewrite is finished.**

The catalog originally carried a parameter table per resource type that was written from memory
rather than from provider schemas. Fifteen of those parameters are not arguments on the resource at
all — they are nested-block fields, renamed arguments, or settings the provider moved into separate
resources. Emitting them produces `Unsupported argument`, so no chart containing those types could
ever pass `tofu validate`.

The rewrite replaces them. This file records what each one *was* and what replaced it, so a
provider file can be reverted or a mapping re-checked without re-deriving it from scratch.

**All rows below have been migrated.** All four provider files were rewritten and
`terraform validate` passes on a chart containing every type. This file is kept as history: it is
what makes a revert possible without re-deriving the mapping from provider schemas again.

## AWS

| Was emitted | Resource | Why invalid | Replaced by |
|---|---|---|---|
| `root_volume_gb` | `aws_instance` | no such argument | `root_block_device { volume_size }` |
| `versioning_enabled` | `aws_s3_bucket` | provider v4 split versioning out | `aws_s3_bucket_versioning` companion resource |
| `cpu` | `aws_ecs_service` | task-definition argument | `aws_ecs_task_definition.cpu` companion |
| `memory` | `aws_ecs_service` | task-definition argument | `aws_ecs_task_definition.memory` companion |
| `default_ttl` | `aws_cloudfront_distribution` | nested | `default_cache_behavior { default_ttl }` |
| `viewer_protocol_policy` | `aws_cloudfront_distribution` | nested | `default_cache_behavior { viewer_protocol_policy }` |

## Azure

| Was emitted | Resource | Why invalid | Replaced by |
|---|---|---|---|
| `replication_type` | `azurerm_storage_account` | wrong name | `account_replication_type` |
| `https_traffic_only` | `azurerm_storage_account` | wrong name | `https_traffic_only_enabled` |
| `node_count` | `azurerm_kubernetes_cluster` | nested | `default_node_pool { node_count }` |
| `vm_size` | `azurerm_kubernetes_cluster` | nested | `default_node_pool { vm_size }` |
| `sku_name` | `azurerm_application_gateway` | `sku` is a block | `sku { name }` |
| `capacity` | `azurerm_application_gateway` | `sku` is a block | `sku { capacity }` |

## GCP

| Was emitted | Resource | Why invalid | Replaced by |
|---|---|---|---|
| `boot_disk_gb` | `google_compute_instance` | nested | `boot_disk { initialize_params { size } }` |
| `tier` | `google_sql_database_instance` | nested | `settings { tier }` |
| `availability_type` | `google_sql_database_instance` | nested | `settings { availability_type }` |
| `machine_type` | `google_container_cluster` | node-pool argument | `google_container_node_pool` companion |
| `cache_mode` | `google_compute_backend_bucket` | nested | `cdn_policy { cache_mode }` |
| `runtime` | `google_cloudfunctions2_function` | nested | `build_config { runtime }` |
| `entry_point` | `google_cloudfunctions2_function` | nested | `build_config { entry_point }` |
| `memory_mb` | `google_cloudfunctions2_function` | nested, renamed | `service_config { available_memory }` |

## DigitalOcean

| Was emitted | Resource | Why invalid | Replaced by |
|---|---|---|---|
| `instance_size_slug` | `digitalocean_app` | nested | `spec { service { instance_size_slug } }` |
| `instance_count` | `digitalocean_app` | nested | `spec { service { instance_count } }` |
| `node_count` | `digitalocean_kubernetes_cluster` | nested | `node_pool { node_count }` |
| `node_size` | `digitalocean_kubernetes_cluster` | nested | `node_pool { size }` |

## Arguments that were missing entirely

Not invalid, but absent, so the resource could not validate:

| Resource | Missing required argument |
|---|---|
| `aws_lambda_function` | `function_name`, `role`, and one of `filename` / `s3_bucket` / `image_uri` |
| `aws_db_instance` | `username` (or `manage_master_user_password`), `db_subnet_group_name` |
| `aws_lb` | `subnets` (≥2 availability zones) |
| `aws_ecs_service` | `task_definition` |
| `aws_cloudfront_distribution` | `enabled`, `origin`, `default_cache_behavior`, `restrictions`, `viewer_certificate` |
| `azurerm_linux_virtual_machine` | `network_interface_ids`, `os_disk`, `source_image_reference`, auth |
| `azurerm_linux_function_app` | `service_plan_id`, `storage_account_name`, `site_config` |
| `azurerm_mssql_database` | `server_id` |
| `azurerm_kubernetes_cluster` | `dns_prefix`, `identity` |
| `azurerm_application_gateway` | seven blocks — see `TOFU-MAPPING.md` |
| `azurerm_cdn_endpoint` | `profile_name`, `origin` |
| `google_compute_instance` | `boot_disk`, `network_interface` |
| `google_cloudfunctions2_function` | `build_config.source`, `service_config` |
| `google_sql_database_instance` | `settings` |
| `google_compute_backend_bucket` | `bucket_name` |
| `digitalocean_database_cluster` | `version` |
| `digitalocean_kubernetes_cluster` | `region`, `node_pool` |
| `digitalocean_loadbalancer` | `region`, `forwarding_rule` |
| `digitalocean_cdn` | `origin` |
| `digitalocean_app` | `spec` |

## How this happened, so it does not happen again

The original tables were written from the resource names alone, without checking a provider schema.
They looked plausible — `root_volume_gb` is exactly what an `aws_instance` argument would be called
if it existed — and nothing verified them, because generation emitted HCL that parsed and no one had
run `tofu validate` against it.

The guard added with the rewrite is the acceptance gate: `terraform init && terraform validate` on a
chart containing every type. Parsing is not validation, and `terraform fmt` passing means only that
the syntax is well formed, not that a single argument is real.
