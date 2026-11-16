variable "cloudflare_account_id" {
  description = "Authorized Cloudflare account; required, with no placeholder default."
  type        = string

  validation {
    condition     = can(regex("^[0-9a-f]{32}$", var.cloudflare_account_id))
    error_message = "cloudflare_account_id must be the real 32-character lowercase hexadecimal account ID."
  }
}

variable "worker_name" {
  description = "Dedicated courseinfo Worker name."
  type        = string
  default     = "courseinfo"

  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9-]{0,61}[a-z0-9]$", var.worker_name))
    error_message = "worker_name must be 2–63 lowercase letters, digits, or hyphens, without leading/trailing hyphens."
  }
}

variable "database_name" {
  description = "Dedicated D1 database name; not concierge's database."
  type        = string
  default     = "courseinfo-catalogue"

  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9_-]{0,61}[a-z0-9]$", var.database_name))
    error_message = "database_name must be 2–63 lowercase letters, digits, underscores, or hyphens, with alphanumeric endpoints."
  }
}

variable "worker_compatibility_date" {
  description = "Must match wrangler.jsonc and the tested production artifact."
  type        = string
  default     = "2026-09-28"

  validation {
    condition     = can(formatdate("YYYY-MM-DD", "${var.worker_compatibility_date}T00:00:00Z"))
    error_message = "worker_compatibility_date must be a valid YYYY-MM-DD date."
  }
}

variable "base_url" {
  description = "Trusted upstream catalogue base URL, never supplied by an HTTP caller."
  type        = string
  default     = "https://w5.ab.ust.hk/wcq/cgi-bin"

  validation {
    condition     = can(regex("^https://[A-Za-z0-9.-]+(:[0-9]+)?(/[A-Za-z0-9._~-]+)*$", var.base_url))
    error_message = "base_url must be an HTTPS origin/path with no credentials, query, fragment, or trailing slash."
  }
}

variable "workers_dev_enabled" {
  description = "Explicit acceptance of public GET and network-restricted PATCH exposure on workers.dev."
  type        = bool
  default     = false
}

variable "cron_enabled" {
  description = "Enable Monday 03:00 Hong Kong weekly refresh; trigger changes can take 15 minutes to propagate."
  type        = bool
  default     = false
}

variable "custom_domain" {
  description = "Optional explicitly authorized public hostname, with PATCH restricted to 143.89.0.0/16; null disables it."
  type        = string
  default     = null
  nullable    = true

  validation {
    condition     = var.custom_domain == null ? true : can(regex("^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\\.)+[a-z]{2,63}$", var.custom_domain))
    error_message = "custom_domain must be null or a lowercase fully qualified hostname without a scheme/path."
  }
}

variable "deployment_version_id" {
  description = "Optional retained Worker version UUID for rollback. Verify it exists and supports the additive D1 schema first; null activates the tested upload."
  type        = string
  default     = null
  nullable    = true

  validation {
    condition     = var.deployment_version_id == null ? true : can(regex("^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$", var.deployment_version_id))
    error_message = "deployment_version_id must be null or a retained version's lowercase UUID."
  }
}

variable "fetch_timeout_ms" {
  description = "Deadline for acquiring a complete upstream body; may be lowered from the 30-second maximum."
  type        = number
  default     = 30000

  validation {
    condition     = var.fetch_timeout_ms >= 1 && var.fetch_timeout_ms <= 30000 && floor(var.fetch_timeout_ms) == var.fetch_timeout_ms
    error_message = "fetch_timeout_ms must be an integer between 1 and 30000."
  }
}

variable "refresh_timeout_ms" {
  description = "Total refresh deadline including D1 awaits; may be lowered from the 12-minute maximum."
  type        = number
  default     = 720000

  validation {
    condition     = var.refresh_timeout_ms >= 1 && var.refresh_timeout_ms <= 720000 && floor(var.refresh_timeout_ms) == var.refresh_timeout_ms
    error_message = "refresh_timeout_ms must be an integer between 1 and 720000."
  }
}

variable "course_miss_rate_limit_namespace_id" {
  description = "Account-unique rate limit namespace for GET cache misses; use a different ID for each environment."
  type        = string
  default     = "14389001"

  validation {
    condition     = can(regex("^[1-9][0-9]*$", var.course_miss_rate_limit_namespace_id))
    error_message = "course_miss_rate_limit_namespace_id must be a positive integer encoded as a string."
  }
}
