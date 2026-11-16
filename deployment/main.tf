locals {
  migration_root  = abspath("${path.module}/../db/migrations")
  migration_files = sort(tolist(fileset(local.migration_root, "*.sql")))
  migration_hash = sha256(join("\n", [
    for filename in local.migration_files :
    "${filename}:${filesha256("${local.migration_root}/${filename}")}"
  ]))

  migration_config = jsonencode({
    name               = var.worker_name
    account_id         = var.cloudflare_account_id
    compatibility_date = var.worker_compatibility_date
    d1_databases = [{
      binding        = "DB"
      database_name  = cloudflare_d1_database.catalogue.name
      database_id    = cloudflare_d1_database.catalogue.id
      migrations_dir = local.migration_root
    }]
  })
}

resource "cloudflare_d1_database" "catalogue" {
  account_id = var.cloudflare_account_id
  name       = var.database_name
  read_replication = {
    mode = "disabled"
  }

  # Application rollback never destroys persistent catalogue data.
  lifecycle {
    prevent_destroy = true
  }
}

resource "cloudflare_worker" "api" {
  account_id = var.cloudflare_account_id
  name       = var.worker_name

  observability = {
    enabled = true
  }

  subdomain = {
    enabled          = var.workers_dev_enabled
    previews_enabled = false
  }
}

# Terraform owns schema ordering; Wrangler only executes additive migrations.
resource "terraform_data" "migrations" {
  triggers_replace = [
    cloudflare_d1_database.catalogue.id,
    local.migration_hash,
    filesha256("${path.module}/../bun.lock"),
  ]

  lifecycle {
    precondition {
      condition     = length(local.migration_files) > 0
      error_message = "No D1 migrations found in db/migrations."
    }
  }

  provisioner "local-exec" {
    working_dir = abspath("${path.module}/..")
    command     = <<-EOT
      set -eu
      dir="$(mktemp -d)"
      trap 'rm -rf "$dir"' EXIT
      config="$dir/wrangler-d1.json"
      printf '%s' "$D1_MIGRATION_CONFIG" > "$config"
      "$WRANGLER_BIN" d1 migrations apply DB --remote --config "$config"
    EOT

    environment = {
      D1_MIGRATION_CONFIG   = local.migration_config
      WRANGLER_BIN          = abspath("${path.module}/../node_modules/.bin/wrangler")
      CI                    = "true"
      WRANGLER_SEND_METRICS = "false"
    }
  }
}

resource "cloudflare_worker_version" "api" {
  account_id         = var.cloudflare_account_id
  worker_id          = cloudflare_worker.api.id
  main_module        = "index.js"
  compatibility_date = var.worker_compatibility_date

  limits = {
    cpu_ms = 300000
  }

  bindings = [
    {
      name        = "DB"
      type        = "d1"
      database_id = cloudflare_d1_database.catalogue.id
    },
    {
      name         = "COURSE_MISS_LIMITER"
      type         = "ratelimit"
      namespace_id = var.course_miss_rate_limit_namespace_id
      simple = {
        limit  = 30
        period = 60
      }
    },
    {
      name = "BASE_URL"
      type = "plain_text"
      text = var.base_url
    },
    {
      name = "WORKER_NAME"
      type = "plain_text"
      text = var.worker_name
    },
    {
      name = "FETCH_TIMEOUT_MS"
      type = "plain_text"
      text = tostring(var.fetch_timeout_ms)
    },
    {
      name = "REFRESH_TIMEOUT_MS"
      type = "plain_text"
      text = tostring(var.refresh_timeout_ms)
    },
  ]

  # content_file preserves Wasm bytes; provider 5.23.0 computes replacement hashes.
  modules = [
    {
      name         = "index.js"
      content_file = "${path.module}/../build/index.js"
      content_type = "application/javascript+module"
    },
    {
      name         = "index_bg.wasm"
      content_file = "${path.module}/../build/index_bg.wasm"
      content_type = "application/wasm"
    },
  ]

  lifecycle {
    precondition {
      condition     = fileexists("${path.module}/../build/index.js") && fileexists("${path.module}/../build/index_bg.wasm")
      error_message = "Build and runtime-test build/index.js and build/index_bg.wasm before terraform plan; use the same bytes for apply."
    }
  }

  depends_on = [terraform_data.migrations]
}

resource "cloudflare_workers_deployment" "api" {
  account_id  = var.cloudflare_account_id
  script_name = cloudflare_worker.api.name
  strategy    = "percentage"

  versions = [{
    version_id = var.deployment_version_id == null ? cloudflare_worker_version.api.id : var.deployment_version_id
    percentage = 100
  }]

  # Even rollback must wait for the current additive migration operation.
  depends_on = [cloudflare_worker_version.api]
}

resource "cloudflare_workers_cron_trigger" "refresh" {
  account_id  = var.cloudflare_account_id
  script_name = cloudflare_worker.api.name
  schedules   = var.cron_enabled ? [{ cron = "0 19 * * SUN" }] : []

  depends_on = [cloudflare_workers_deployment.api]
}

resource "cloudflare_workers_custom_domain" "api" {
  count      = var.custom_domain == null ? 0 : 1
  account_id = var.cloudflare_account_id
  hostname   = var.custom_domain
  service    = cloudflare_worker.api.name

  depends_on = [cloudflare_workers_deployment.api]
}

output "d1_database_id" {
  description = "Dedicated database managed by this courseinfo state."
  value       = cloudflare_d1_database.catalogue.id
}

output "uploaded_version_id" {
  description = "Record this retained version UUID before a later rollout."
  value       = cloudflare_worker_version.api.id
}

output "deployed_version_id" {
  description = "Active version; can differ from the uploaded version during rollback."
  value       = var.deployment_version_id == null ? cloudflare_worker_version.api.id : var.deployment_version_id
}
