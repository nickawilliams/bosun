# Bosun

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

---

✓ = working
✘ = broken
~ = not yet implemented

| Command      |     | Outside                |     | Project Root                       |     | Workspace                          |     | Repo                               |
| ------------ | :-: | ---------------------- | :-: | ---------------------------------- | :-: | ---------------------------------- | :-: | ---------------------------------- |
| `.`          |  ✓  | show help              |  ✓  | show help                          |  ✓  | show help                          |  ✓  | show help                          |
|              |     |                        |     |                                    |     |                                    |     |                                    |
| `config`     |  ✓  | show `<global>` config |  ✓  | show `<global> + <project>` config |  ✓  | show `<global> + <project>` config |  ✓  | show `<global> + <project>` config |
| `doctor`     |  ✘  | show `<global>` health |  ✓  | show `<project>` health            |     | show `<project>` health            |     | show `<project>` health            |
| `init`       |  ✓  | create `<project>`     |  ✓  | upsert `<project>`                 |     | `<error>`                          |     | `<error>`                          |
| `status`     |  ✘  | show `<error>`         |  ✘  | show `<project>` status            |  ✓  | show `<workspace>` status          |     | show `<repo>` status               |
| `workspace`  |     |                        |     |                                    |     |                                    |     |                                    |
| `help`       |     |                        |     |                                    |     |                                    |     |                                    |
| `completion` |     |                        |     |                                    |     |                                    |     |                                    |
|              |     |                        |     |                                    |     |                                    |     |                                    |
| `create`     |     |                        |     |                                    |     |                                    |     |                                    |
| `start`      |     |                        |     |                                    |     |                                    |     |                                    |
| `review`     |     |                        |     |                                    |     |                                    |     |                                    |
| `preview`    |     |                        |     |                                    |     |                                    |     |                                    |
| `prerelease` |     |                        |     |                                    |     |                                    |     |                                    |
| `release`    |     |                        |     |                                    |     |                                    |     |                                    |
| `cleanup`    |     |                        |     |                                    |     |                                    |     |                                    |

---

| `container.type` | `container.name` | `resource.type` | `resource.name` | `action.type` | `action.name` |                                                                            |
| ---------------- | ---------------- | --------------- | --------------- | ------------- | ------------- | -------------------------------------------------------------------------- |
| repo             | foo              | branch          | bar             | +             | create        | create branch "bar" in repo "foo"                                          |
| repo             | foo              | worktree        | foo-bar         | +             | create        | create worktree "foo-bar" in repo "foo"                                    |
| issue            | ABC-123          | status          | In-Progress     | ~             | create        | update status "In-Progress" for issue "ABC-123"                            |
| ???              | ???              | environment     | quirky-quail    | +             | create        | create environment "quirky-quail" from pull-request(s) "[one, two, three]" |
|                  |                  | environment     | quirky-quail    | +             | deploy        | deploy environment "quirky-quail"                                          |
|                  |                  | environment     | quirky-quail    | -             | teardown      | teardown environment "quirky-quail"                                        |
| channel          | #bb-prs          | message         | `<message>`     | +             | notify        | notify message "<message>" in channel "#bb-prs"                            |
| channel          | #bb-prs          | message         | `<message>`     | ~             | notify        | notify message "<message>" in channel "#bb-prs"                            |

```
  + create    issue         "ABC-123"
  ~ update    status        "In-Progress"       for issue "ABC-123"
  + create    branch        "some/branch-name"  in repo "org/repo"
  + create    pull-request  [known after apply] in repo "org/repo"
  + deploy    namespace     "foo-bar"           in environment "preview"
  - teardown  namespace     "foo-bar"           in environment "preview"
  ~ adopt     namespace     "foo-bar"           in environment "preview"
  =           namespace     "foo-bar"           in environment "preview"
```

Appreciating the push-back, please keep doing so if you if it makes sense. But for the action type/name, what if the name is just the domain-specific word for some action, and
the type is just what category of action it is? For example:

```
+ deploy   environment "foo-bar"
- teardown environment "foo-bar"
~ redeploy environment "foo-bar"
= adopt    environment "foo-bar"
= retain   environment "foo-bar"
```
