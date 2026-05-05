# ============================================================================
# Provider
# ============================================================================

variable "github_token" {
  description = "GitHub personal access token (repo + admin:org scopes)"
  type        = string
  sensitive   = true
  default     = null
}

variable "github_owner" {
  description = "GitHub user or organization that owns the repository"
  type        = string
  default     = "nickawilliams"
}

variable "github_bot" {
  description = "GitHub username of the CI bot account (added as repo admin for release pushes)"
  type        = string
  default     = "nickawilliams-bot"
}

# ============================================================================
# Secrets (optional — only managed when provided)
# ============================================================================

variable "github_bot_token" {
  description = "PAT for the CI bot account (used for authenticated pushes to protected branches)"
  type        = string
  sensitive   = true
  default     = null
}

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

variable "homebrew_token" {
  description = "GitHub token with write access to the homebrew tap repository"
  type        = string
  sensitive   = true
  default     = null
}

variable "macports_token" {
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
  default     = "ci@nickawilliams.com"
}

variable "homebrew_repo" {
  description = "Homebrew tap repository (owner/name)"
  type        = string
  default     = "nickawilliams/homebrew-tap"
}

variable "macports_repo" {
  description = "MacPorts fork repository (owner/name)"
  type        = string
  default     = "nickawilliams/fork-macports-ports"
}

variable "macports_pullrequest" {
  description = "Whether macports publish opens a PR to upstream (true/false)"
  type        = string
  default     = "false"
}

variable "publish_enabled" {
  description = "Enable homebrew and macports publishing (true/false)"
  type        = string
  default     = "false"
}
