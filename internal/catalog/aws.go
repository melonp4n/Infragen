package catalog

// Lambda's badge glyph doubles as its Code. Saved sessions reference it, so it
// must not change.
const codeLambda = "λ"

// Arguments and blocks below were checked against the AWS provider schema rather
// than written from memory — see documentation/INVALID-PARAMS.md for what the
// previous table got wrong and why.
func init() {
	Register(Provider{
		Key:           "aws",
		Label:         "AWS",
		Color:         "#E0A458",
		Dim:           "#4A3C29",
		TofuLocalName: "aws",
		TofuSource:    "hashicorp/aws",
		LabelArg:      "tags",
		Config:        []Fixed{{Key: "region", Expr: "{{region}}"}},
		Types: []ResourceType{
			{
				Code: "EC2", Name: "EC2 instance", TofuType: "aws_instance",
				Network: NetFirewalled, AddressAttr: "public_ip", AddressKind: AddrEphemeralIP,
				StaticAddr: &StaticAddress{TofuType: "aws_eip", Attr: "public_ip"},
				Fixed: []Fixed{
					{Key: "subnet_id", Expr: "aws_subnet.{{account}}.id"},
				},
				// Created only when a key resolves from somewhere — which may be a
				// Terraform variable, so the decision belongs at plan time.
				Companions: []Companion{{
					TofuType: "aws_key_pair", Suffix: "key",
					Count: `{{sshkey}} != "" ? 1 : 0`,
					Fixed: []Fixed{
						{Key: "key_name", Expr: `"{{asset-dashed}}"`},
						{Key: "public_key", Expr: "{{sshkey}}"},
					},
					ParentRef:  "key_name",
					ParentExpr: "one(aws_key_pair.{{asset}}_key[*].key_name)",
				}},
				Params: append([]ParamField{
					StaticAddressToggle(),
					{Key: "instance_type", Label: "Instance type", Type: FieldSelect, Options: []string{"t3.micro", "t3.small", "t3.medium", "m5.large", "c5.xlarge"}, Default: "t3.micro"},
					{Key: "ami", Label: "AMI ID", Type: FieldText, Default: "ami-0c55b159cbfafe1f0"},
					{Key: "associate_public_ip_address", Label: "Assign public IP", Type: FieldBoolean, Default: false, Advanced: true},
					{Key: "monitoring", Label: "Detailed monitoring", Type: FieldBoolean, Default: false, Advanced: true},
					// IMDSv2. Requiring a token is what stops an SSRF bug reaching
					// the instance credentials.
					{Key: "http_tokens", Block: "metadata_options", Label: "IMDS tokens", Type: FieldSelect, Options: []string{"required", "optional"}, Default: "required", Advanced: true},
					{Key: "user_data", Label: "Startup script", Type: FieldScript, Default: "", Advanced: true},
					{Key: "volume_size", Block: "root_block_device", Label: "Root volume (GB)", Type: FieldNumber, Default: 20, Advanced: true},
					{Key: "encrypted", Block: "root_block_device", Label: "Encrypt root volume", Type: FieldBoolean, Default: true, Advanced: true},
				}, sshAndAnsible("ec2-user")...),
			},
			{
				Code: codeLambda, Name: "Lambda", TofuType: "aws_lambda_function",
				Network: NetServiceEndpoint,
				Fixed: []Fixed{
					{Key: "function_name", Expr: `"{{asset-dashed}}"`},
					{Key: "filename", Expr: "var.{{asset}}_package"},
				},
				Companions: []Companion{{
					TofuType: "aws_iam_role", Suffix: "role",
					Fixed: []Fixed{
						{Key: "name", Expr: `"{{asset-dashed}}-role"`},
						{Key: "assume_role_policy", Expr: `jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Action    = "sts:AssumeRole"
      Principal = { Service = "lambda.amazonaws.com" }
    }]
  })`},
					},
					ParentRef:  "role",
					ParentExpr: "aws_iam_role.{{asset}}_role.arn",
				}},
				Variables: []Variable{{
					Name: "{{asset}}_package", Description: "Path to the deployment package zip",
					Default: `"lambda.zip"`,
				}},
				Params: []ParamField{
					{Key: "runtime", Label: "Runtime", Type: FieldSelect, Options: []string{"nodejs20.x", "python3.12", "java21", "provided.al2023"}, Default: "nodejs20.x"},
					{Key: "handler", Label: "Handler", Type: FieldText, Default: "index.handler"},
					{Key: "memory_size", Label: "Memory (MB)", Type: FieldNumber, Default: 128, Advanced: true},
					{Key: "timeout", Label: "Timeout (s)", Type: FieldNumber, Default: 3, Advanced: true},
				},
			},
			{
				Code: "RDS", Name: "RDS database", TofuType: "aws_db_instance",
				// Only a hostname is exposed, so a cross-account rule against it
				// cannot be generated — the generator reports that rather than guessing.
				Network: NetFirewalled, AddressAttr: "address", AddressKind: AddrHostname,
				Fixed: []Fixed{
					// AWS creates and rotates the password in Secrets Manager, so no
					// password is written here or held in state.
					{Key: "manage_master_user_password", Expr: "true"},
				},
				Companions: []Companion{{
					TofuType: "aws_db_subnet_group", Suffix: "subnets",
					Fixed: []Fixed{
						{Key: "name", Expr: `"{{asset-dashed}}"`},
						{Key: "subnet_ids", Expr: "[aws_subnet.{{account}}.id, aws_subnet.{{account}}_b.id]"},
					},
					ParentRef:  "db_subnet_group_name",
					ParentExpr: "aws_db_subnet_group.{{asset}}_subnets.name",
				}},
				Params: []ParamField{
					{Key: "engine", Label: "Engine", Type: FieldSelect, Options: []string{"postgres", "mysql", "mariadb"}, Default: "postgres"},
					{Key: "engine_version", Label: "Engine version", Type: FieldText, Default: "16.3"},
					{Key: "instance_class", Label: "Instance class", Type: FieldSelect, Options: []string{"db.t3.micro", "db.t3.medium", "db.r6g.large"}, Default: "db.t3.micro"},
					{Key: "allocated_storage", Label: "Allocated storage (GB)", Type: FieldNumber, Default: 20},
					{Key: "username", Label: "Master username", Type: FieldText, Default: "dbadmin"},
					{Key: "storage_encrypted", Label: "Encrypt storage", Type: FieldBoolean, Default: true, Advanced: true},
					{Key: "publicly_accessible", Label: "Publicly accessible", Type: FieldBoolean, Default: false, Advanced: true},
					{Key: "auto_minor_version_upgrade", Label: "Auto minor upgrades", Type: FieldBoolean, Default: true, Advanced: true},
					{Key: "multi_az", Label: "Multi-AZ", Type: FieldBoolean, Default: false, Advanced: true},
					// Teardown defaults: this tool is for infrastructure that gets
					// stood up and torn down.
					{Key: "skip_final_snapshot", Label: "Skip final snapshot", Type: FieldBoolean, Default: true, Advanced: true},
					{Key: "deletion_protection", Label: "Deletion protection", Type: FieldBoolean, Default: false, Advanced: true},
					{Key: "backup_retention_period", Label: "Backup retention (days)", Type: FieldNumber, Default: 0, Advanced: true},
				},
			},
			{
				Code: "S3", Name: "S3 bucket", TofuType: "aws_s3_bucket",
				Network: NetServiceEndpoint,
				Fixed: []Fixed{
					// A prefix rather than a name: bucket names are globally unique.
					{Key: "bucket_prefix", Expr: `"{{asset-dashed}}-"`},
				},
				// The v4 provider split these out of aws_s3_bucket. Without them a
				// bucket is unencrypted, unversioned and public-by-ACL.
				Companions: []Companion{
					{
						TofuType: "aws_s3_bucket_public_access_block", Suffix: "public_access",
						Fixed: []Fixed{
							{Key: "bucket", Expr: "aws_s3_bucket.{{asset}}.id"},
							{Key: "block_public_acls", Expr: "true"},
							{Key: "block_public_policy", Expr: "true"},
							{Key: "ignore_public_acls", Expr: "true"},
							{Key: "restrict_public_buckets", Expr: "true"},
						},
					},
					{
						TofuType: "aws_s3_bucket_server_side_encryption_configuration", Suffix: "encryption",
						Fixed: []Fixed{
							{Key: "bucket", Expr: "aws_s3_bucket.{{asset}}.id"},
							{Block: "rule.apply_server_side_encryption_by_default", Key: "sse_algorithm", Expr: `"AES256"`},
						},
					},
					{
						TofuType: "aws_s3_bucket_versioning", Suffix: "versioning",
						Fixed: []Fixed{
							{Key: "bucket", Expr: "aws_s3_bucket.{{asset}}.id"},
							{Block: "versioning_configuration", Key: "status", Expr: `"Enabled"`},
						},
					},
				},
				Params: []ParamField{
					{Key: "force_destroy", Label: "Destroy with contents", Type: FieldBoolean, Default: true, Advanced: true},
				},
			},
			{
				Code: "ALB", Name: "Load balancer", TofuType: "aws_lb",
				// An ALB has no IP. An NLB can get static IPs via subnet_mapping,
				// which is not modelled yet.
				Network: NetFirewalled, AddressAttr: "dns_name", AddressKind: AddrHostname,
				Fixed: []Fixed{
					// Two zones: an application load balancer requires it.
					{Key: "subnets", Expr: "[aws_subnet.{{account}}.id, aws_subnet.{{account}}_b.id]"},
				},
				Params: []ParamField{
					{Key: "internal", Label: "Internal only", Type: FieldBoolean, Default: false},
					{Key: "load_balancer_type", Label: "Type", Type: FieldSelect, Options: []string{"application", "network"}, Default: "application"},
					{Key: "drop_invalid_header_fields", Label: "Drop invalid headers", Type: FieldBoolean, Default: true, Advanced: true},
					{Key: "enable_deletion_protection", Label: "Deletion protection", Type: FieldBoolean, Default: false, Advanced: true},
				},
			},
			{
				Code: "ECS", Name: "ECS service", TofuType: "aws_ecs_service",
				// Fargate tasks get ephemeral network interfaces, so there is no
				// address to reference. Security groups attach via the network block.
				Network: NetFirewalled,
				Fixed: []Fixed{
					{Key: "name", Expr: `"{{asset-dashed}}"`},
					{Block: "network_configuration", Key: "subnets", Expr: "[aws_subnet.{{account}}.id]"},
					{Block: "network_configuration", Key: "assign_public_ip", Expr: "false"},
				},
				Companions: []Companion{
					{
						TofuType: "aws_ecs_cluster", Suffix: "cluster",
						Fixed:      []Fixed{{Key: "name", Expr: `"{{asset-dashed}}"`}},
						ParentRef:  "cluster",
						ParentExpr: "aws_ecs_cluster.{{asset}}_cluster.id",
					},
					{
						TofuType: "aws_ecs_task_definition", Suffix: "task",
						Fixed: []Fixed{
							{Key: "family", Expr: `"{{asset-dashed}}"`},
							{Key: "requires_compatibilities", Expr: `["FARGATE"]`},
							{Key: "network_mode", Expr: `"awsvpc"`},
							{Key: "cpu", Expr: `"256"`},
							{Key: "memory", Expr: `"512"`},
							{Key: "container_definitions", Expr: `jsonencode([{
    name      = "app"
    image     = var.{{asset}}_image
    essential = true
  }])`},
						},
						ParentRef:  "task_definition",
						ParentExpr: "aws_ecs_task_definition.{{asset}}_task.arn",
					},
				},
				Variables: []Variable{{
					Name: "{{asset}}_image", Description: "Container image for the service",
					Default: `"public.ecr.aws/nginx/nginx:latest"`,
				}},
				Params: []ParamField{
					{Key: "launch_type", Label: "Launch type", Type: FieldSelect, Options: []string{"FARGATE", "EC2"}, Default: "FARGATE"},
					{Key: "desired_count", Label: "Desired count", Type: FieldNumber, Default: 1},
				},
			},
			{
				Code: "CDN", Name: "CloudFront CDN", TofuType: "aws_cloudfront_distribution",
				// CloudFront origin fetches come from a managed prefix list, not a CIDR.
				Network: NetEdge, AddressAttr: "domain_name", AddressKind: AddrHostname,
				Fixed: []Fixed{
					{Key: "enabled", Expr: "true"},
					{Block: "origin", Key: "domain_name", Expr: "var.{{asset}}_origin_domain"},
					{Block: "origin", Key: "origin_id", Expr: `"{{asset-dashed}}-origin"`},
					{Block: "default_cache_behavior", Key: "target_origin_id", Expr: `"{{asset-dashed}}-origin"`},
					{Block: "default_cache_behavior", Key: "allowed_methods", Expr: `["GET", "HEAD"]`},
					{Block: "default_cache_behavior", Key: "cached_methods", Expr: `["GET", "HEAD"]`},
					{Block: "restrictions.geo_restriction", Key: "restriction_type", Expr: `"none"`},
					{Block: "viewer_certificate", Key: "cloudfront_default_certificate", Expr: "true"},
				},
				Variables: []Variable{{
					Name: "{{asset}}_origin_domain", Description: "Origin the distribution fetches from",
					Default: `"origin.example.com"`,
				}},
				Params: []ParamField{
					{Key: "price_class", Label: "Price class", Type: FieldSelect, Options: []string{"PriceClass_All", "PriceClass_200", "PriceClass_100"}, Default: "PriceClass_100"},
					{Key: "viewer_protocol_policy", Block: "default_cache_behavior", Label: "Viewer protocol policy", Type: FieldSelect, Options: []string{"redirect-to-https", "https-only", "allow-all"}, Default: "redirect-to-https"},
					{Key: "default_ttl", Block: "default_cache_behavior", Label: "Default TTL (s)", Type: FieldNumber, Default: 3600, Advanced: true},
				},
			},
		},
	})
}
