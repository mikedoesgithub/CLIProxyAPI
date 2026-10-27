# CLIProxyAPI — prox fork

This is a full fork of [router-for-me/CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI), with upstream history, branches and license retained. You own [mikedoesgithub/CLIProxyAPI](https://github.com/mikedoesgithub/CLIProxyAPI) and can change the entire server. The separate frontend fork is [mikedoesgithub/Cli-Proxy-API-Management-Center](https://github.com/mikedoesgithub/Cli-Proxy-API-Management-Center).

## Fork changes

- Default management-panel repository, release API and fallback asset point to your prox frontend release.
- Existing explicit `management.panel-github-repository` values take precedence; changing server defaults does not rewrite an installed config.
- Removed upstream contribution workflows that automatically close guidance changes, reject translator changes, or retarget main PRs to dev. Build/test and release workflows remain available.
- Go module/import paths retain their upstream names so internal imports and existing SDK users continue to compile. Repository ownership does not depend on renaming the module.

## Develop

Clone both repositories side by side:

```bash
gh repo clone mikedoesgithub/CLIProxyAPI
gh repo clone mikedoesgithub/Cli-Proxy-API-Management-Center
```

The prepared workspace already uses `origin` for your forks and `upstream` for router-for-me. New clones can add upstream explicitly:

```bash
git remote add upstream https://github.com/router-for-me/CLIProxyAPI.git
git switch -c feat/my-change
go test ./internal/managementasset ./internal/config
go build -o cli-proxy-api ./cmd/server
git push -u origin feat/my-change
```

Use the Go version declared in `go.mod`. Run `gofmt` on changed Go files. `go test ./...` runs the complete backend suite. For upstream updates, fetch and merge on a working branch, resolve conflicts, and test before merging into main. Do not force-reset your customized main branch.

The existing backend release workflow builds Linux, macOS, Windows and FreeBSD assets on pushed tags. Docker publication additionally needs your own registry credentials; upstream secrets are not copied by GitHub forks. A fork includes source/history, not upstream issues, secret values, release assets or hosted services. No backend release is required to install the WebUI on an existing compatible v8 server.

## Install prox on an existing server

```bash
curl -fsSL https://raw.githubusercontent.com/mikedoesgithub/Cli-Proxy-API-Management-Center/main/upgrade.sh | bash
```

This installs only the frontend, backs up the current panel/config, and prevents automatic upstream-panel replacement. Backend binaries and OAuth credentials are preserved. See the frontend README for custom paths, rollback and TLS requirements.

For new configs built from this fork, `config.example.yaml` already points at your frontend. Existing servers can opt into automatic prox updates by setting:

```yaml
management:
  panel-github-repository: https://github.com/mikedoesgithub/Cli-Proxy-API-Management-Center
  disable-auto-update-panel: false
```

Merge these keys into the existing `management` section; retain its secret and access policy. Do not replace the entire installed configuration with this example. Automatic updates require a published frontend release containing `management.html`.

## Fork verification

On Linux, 2026-10-27: `go test ./internal/managementasset ./internal/config` passed, and `go build -o <temporary-output> ./cmd/server` completed successfully with Go 1.27.0. The full backend test suite and cross-platform release builds were not run for these panel-default changes. No running installation or credential store was modified.

## Automatic upstream sync

[Sync upstream](https://github.com/mikedoesgithub/CLIProxyAPI/actions/workflows/sync-upstream.yml) checks upstream `main` every 15 minutes, offset from the hour. GitHub schedules can be delayed. It also supports Actions → Sync upstream → Run workflow, or:

```bash
gh workflow run sync-upstream.yml -R mikedoesgithub/CLIProxyAPI -f merge=true
```

For an immediate check without merging, use `-f merge=false`. Dispatch starts the check as soon as GitHub allocates a runner; it does not wait for GitHub's cached PR mergeability calculation. Without an upstream webhook, upstream pushes cannot trigger this fork instantly.

The workflow fetches both tips and performs a real `git merge --no-ff --no-commit`. Zero conflicts means it commits and pushes the merge to this fork's `main`, retaining both histories and custom changes. Conflicts abort the trial merge, list affected files in the run summary and fail the run without changing the remote branch. Check-only runs abort even a clean trial. Concurrent runs are serialized; an ordinary non-force push rejects a concurrent human update instead of overwriting it. No tags, releases, installed binaries or local configs are updated by this workflow.

A clean Git merge does not guarantee compatible behavior. After a published merge, the same workflow runs the full Go test suite and server build. This is explicit because pushes made with `GITHUB_TOKEN` do not trigger normal push workflows. Validation failure marks the run red and leaves the merge in place for repair; it does not rewrite main or automatically roll back later human commits.

The built-in `GITHUB_TOKEN` has Contents write only in the sync job; verification has read-only access and does not retain checkout credentials. Branch protection or GitHub's restrictions on changing workflow files may reject a push. If needed, configure repository secret `UPSTREAM_SYNC_TOKEN` with a fine-grained token restricted to this repository, Contents write and Workflows write. The token is optional; no personal credential is embedded in this source. Protected branches still require an appropriate permitted actor. Failed pushes remain failures and are never reported as successful merges.

The production script is `.github/scripts/sync-upstream.sh`. Its six isolated local-Git fixtures cover no updates, check-only cleanup, a clean divergent merge preserving fork changes, conflicts, stale-checkout races and rejected pushes. Run `python3 .github/scripts/test-sync-upstream.py` to reproduce them. Both workflows run these fixtures before checking upstream.
