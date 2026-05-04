# ============================================================================
# Repository
# ============================================================================

# Import existing repo: terraform import github_repository.bosun bosun
resource "github_repository" "bosun" {
  name         = "bosun"
  description  = "Automate SDLC lifecycle tasks across issue trackers, version control, CI/CD, and notification systems."
  homepage_url = "https://github.com/nickawilliams/bosun"

  visibility = "public"

  has_issues      = true
  has_wiki        = false
  has_projects    = false
  has_discussions = false

  allow_merge_commit     = true
  allow_squash_merge     = true
  allow_rebase_merge     = true
  delete_branch_on_merge = true

  security_and_analysis {
    secret_scanning {
      status = "enabled"
    }
    secret_scanning_push_protection {
      status = "enabled"
    }
  }
}

resource "github_repository_vulnerability_alerts" "bosun" {
  repository = github_repository.bosun.name
}

# ============================================================================
# Branch Protection
# ============================================================================

resource "github_branch_protection" "main" {
  repository_id = github_repository.bosun.node_id
  pattern       = "main"

  required_status_checks {
    strict = true
    contexts = [
      "lint",
      "build",
      "bench",
      "test",
    ]
  }

  enforce_admins = false
}

# ============================================================================
# Actions Secrets
# ============================================================================

resource "github_actions_secret" "gpg_private_key" {
  count       = var.gpg_private_key != null ? 1 : 0
  repository  = github_repository.bosun.name
  secret_name = "GPG_PRIVATE_KEY"
  value       = var.gpg_private_key
}

resource "github_actions_secret" "codecov_token" {
  count       = var.codecov_token != null ? 1 : 0
  repository  = github_repository.bosun.name
  secret_name = "CODECOV_TOKEN"
  value       = var.codecov_token
}

resource "github_actions_secret" "homebrew_tap_token" {
  count       = var.homebrew_tap_token != null ? 1 : 0
  repository  = github_repository.bosun.name
  secret_name = "HOMEBREW_TAP_TOKEN"
  value       = var.homebrew_tap_token
}

resource "github_actions_secret" "macports_port_token" {
  count       = var.macports_port_token != null ? 1 : 0
  repository  = github_repository.bosun.name
  secret_name = "MACPORTS_PORT_TOKEN"
  value       = var.macports_port_token
}

# ============================================================================
# Actions Variables
# ============================================================================

resource "github_actions_variable" "git_user_name" {
  repository    = github_repository.bosun.name
  variable_name = "GIT_USER_NAME"
  value         = var.git_user_name
}

resource "github_actions_variable" "git_user_email" {
  repository    = github_repository.bosun.name
  variable_name = "GIT_USER_EMAIL"
  value         = var.git_user_email
}

resource "github_actions_variable" "tap_repo" {
  repository    = github_repository.bosun.name
  variable_name = "TAP_REPO"
  value         = var.tap_repo
}

resource "github_actions_variable" "port_repo" {
  repository    = github_repository.bosun.name
  variable_name = "PORT_REPO"
  value         = var.port_repo
}

resource "github_actions_variable" "port_pullrequest" {
  repository    = github_repository.bosun.name
  variable_name = "PORT_PULLREQUEST"
  value         = var.port_pullrequest
}

resource "github_actions_variable" "publish_enabled" {
  repository    = github_repository.bosun.name
  variable_name = "PUBLISH_ENABLED"
  value         = var.publish_enabled
}
