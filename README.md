# Bosun

[![Build Status][ci-image]][ci-url]
[![Code Coverage][coverage-image]][coverage-url]

A CLI tool for automating repeated SDLC tasks across issue trackers, version
control, CI/CD, and notification systems. Named for the ship's officer who
directs the crew and signals state changes.

See [DESIGN.md](DESIGN.md) for architecture and design decisions.
See [ROADMAP.md](ROADMAP.md) for planned work and future ideas.

## Lifecycle Commands

| Command            | Transition          | Actions                                            |
| ------------------ | ------------------- | -------------------------------------------------- |
| `bosun create`     | → Ready             | Create issue                                       |
| `bosun start`      | → In Progress       | Branch + worktrees, transition status              |
| `bosun review`     | → Review            | Open PRs, notify review channel, transition status |
| `bosun preview`    | → In Preview Env    | Trigger deploy, notify thread, transition status   |
| `bosun prerelease` | → Ready for Release | Create releases/tags, notify release channel       |
| `bosun release`    | → Done              | Trigger prod deploy, transition status             |
| `bosun cleanup`    | _(Done)_            | Delete branches + worktrees                        |
| `bosun status`     | _(query only)_      | Show issue, branch, PR, and deploy status          |

## Getting Started

```sh
# Initialize a project
bosun init

# Begin work on an issue
bosun start --issue PROJ-123

# Submit for review
bosun review --issue PROJ-123

# Check status at any point
bosun status --issue PROJ-123
```

## Configuration

Two-tier config: global at `~/.config/bosun/config.yaml`, project-level at
`.bosun/config.yaml` (merged on top). Run `bosun init` in your project root
to set up interactively, or `bosun doctor` to verify connectivity.

### Supported Providers

| Capability      | Provider       |
| --------------- | -------------- |
| Issue Tracking  | Jira           |
| Code Hosting    | GitHub         |
| Notifications   | Slack          |
| CI/CD           | GitHub Actions |
| Version Control | Git            |

## Utility Commands

```sh
bosun config {get,set,list,edit,path,show,check,unset}
bosun workspace {create,add,status,rm}
bosun doctor
bosun init [--quick] [--yes]
```

[ci-image]: https://img.shields.io/github/actions/workflow/status/nickawilliams/bosun/release.yaml?logo=GitHub&logoColor=white
[ci-url]: https://github.com/nickawilliams/bosun/actions/workflows/release.yaml
[coverage-image]: https://img.shields.io/codecov/c/github/nickawilliams/bosun?logo=codecov&logoColor=white
[coverage-url]: https://codecov.io/gh/nickawilliams/bosun
