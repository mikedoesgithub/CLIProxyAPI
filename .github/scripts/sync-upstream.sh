#!/usr/bin/env bash
# Trial-merge upstream into the checked-out fork branch, then optionally push.
set -Eeuo pipefail
: "${UPSTREAM_REMOTE_URL:?Set UPSTREAM_REMOTE_URL}"
target=${TARGET_BRANCH:-main}
merge=${SYNC_MERGE:-true}
case "$merge" in true|false) ;; *) echo 'SYNC_MERGE must be true or false' >&2; exit 1 ;; esac
git check-ref-format --branch "$target" >/dev/null
[ "$(git branch --show-current)" = "$target" ] || { echo "Check out $target first" >&2; exit 1; }
[ -z "$(git status --porcelain)" ] || { echo 'Working tree must be clean' >&2; exit 1; }
output() { if [ -n "${GITHUB_OUTPUT:-}" ]; then printf '%s=%s\n' "$1" "$2" >> "$GITHUB_OUTPUT"; fi; }
summary() { printf '%s\n' "$*"; if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then printf '%s\n' "$*" >> "$GITHUB_STEP_SUMMARY"; fi; }
cleanup() { if git rev-parse -q --verify MERGE_HEAD >/dev/null; then git merge --abort; fi; }
trap cleanup EXIT
output changed false
output status checking
# Pin both tips for this run. The final ordinary push rejects concurrent changes.
git fetch --no-tags origin "refs/heads/$target:refs/remotes/origin/$target"
[ "$(git rev-parse HEAD)" = "$(git rev-parse "refs/remotes/origin/$target")" ] || { summary 'Fork moved since checkout. Rerun against the latest main.'; exit 1; }
git fetch --no-tags "$UPSTREAM_REMOTE_URL" "refs/heads/$target:refs/remotes/prox-upstream/$target"
upstream=$(git rev-parse "refs/remotes/prox-upstream/$target")
before=$(git rev-parse HEAD)
output upstream_sha "$upstream"
output sha "$before"
if git merge-base --is-ancestor "$upstream" HEAD; then
  output status up-to-date
  summary "Already includes upstream $upstream. No changes."
  exit 0
fi
# No commit is made until Git has actually resolved the entire merge cleanly.
if ! git -c core.hooksPath=/dev/null merge --no-ff --no-commit "$upstream"; then
  if [ -n "$(git diff --name-only --diff-filter=U)" ]; then
    output status conflicts
    summary '### Upstream merge blocked: conflicts'
    summary 'Resolve these paths manually; the fork was not changed:'
    while IFS= read -r path; do summary "- \`$path\`"; done < <(git diff --name-only --diff-filter=U)
    exit 1
  fi
  output status error
  summary 'Git could not prepare the merge. The fork was not changed; inspect the log.'
  exit 1
fi
if [ "$merge" = false ]; then
  output status mergeable
  summary "Zero conflicts with upstream $upstream. Check-only: no commit or push."
  exit 0
fi
git -c core.hooksPath=/dev/null commit -m "chore(sync): merge upstream $target ($upstream)"
# Never force. A human push or branch protection rejects this update safely.
git -c core.hooksPath=/dev/null push origin "HEAD:refs/heads/$target"
output changed true
output status merged
output sha "$(git rev-parse HEAD)"
summary "Merged upstream $upstream with zero conflicts. Published $(git rev-parse HEAD) to $target."
