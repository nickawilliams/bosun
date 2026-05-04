# ============================================================================
# Provider
# ============================================================================

variable "github_token" {
  description = "GitHub personal access token (repo + admin:org scopes)"
  type        = string
  sensitive   = true
  default     = null
}

variable "github_organization" {
  description = "GitHub user or organization that owns the repository"
  type        = string
  default     = "nickawilliams"
}

# ============================================================================
# Secrets (optional — only managed when provided)
# ============================================================================

variable "gpg_private_key" {
  description = "GPG private key for release commit and tag signing"
  type        = string
  sensitive   = true
  default     = null
}

variable "codecov_token" {
  description = "Codecov upload token"
  type        = string
  sensitive   = true
  default     = null
}

variable "homebrew_tap_token" {
  description = "GitHub token with write access to the homebrew tap repository"
  type        = string
  sensitive   = true
  default     = null
}

variable "macports_port_token" {
  description = "GitHub token with write access to the macports fork repository"
  type        = string
  sensitive   = true
  default     = null
}

# ============================================================================
# Actions Variables
# ============================================================================

variable "git_user_name" {
  description = "Git author name for release commits"
  type        = string
  default     = "CI Bot"
}

variable "git_user_email" {
  description = "Git author email for release commits"
  type        = string
  default     = "ci@nickawilliams"
}

variable "port_repo" {
  description = "MacPorts fork repository (owner/name)"
  type        = string
  default     = "nickawilliams/fork-macports-ports"
}

variable "port_pullrequest" {
  description = "Whether macports publish opens a PR to upstream (true/false)"
  type        = string
  default     = "false"
}

variable "publish_enabled" {
  description = "Enable homebrew and macports publishing (true/false)"
  type        = string
  default     = "false"
}
