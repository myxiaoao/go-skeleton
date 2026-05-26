#!/usr/bin/env bash
# scripts/rename.sh — one-shot script to re-brand the skeleton.
#
# Usage:
#   ./scripts/rename.sh <NEW_MODULE> <NEW_SHORTNAME> [NEW_REPO_URL]
#
# Example:
#   ./scripts/rename.sh github.com/acme/payments payments github.com/acme/payments
#
# NEW_REPO_URL (optional, no scheme, no trailing slash) replaces the upstream
# repo URL embedded in deploy docs, issue templates, systemd Documentation=,
# cosign certificate-identity-regexp, and CLAUDE/AGENTS "remote origin" notes.
# When omitted, NEW_MODULE is used as a best-effort fallback — works fine for
# `github.com/<org>/<repo>` shaped modules, but custom git hosts should pass
# this explicitly.
#
# Environment:
#   RENAME_BARE=1  also rewrite bare `go-skeleton` tokens in markdown prose
#                  (default off, to keep README's "this is a Go skeleton"
#                  upstream-attribution intact).
#
# What it does:
#   1. Verifies the working tree is clean and the current module is still
#      `go-skeleton` (refuses to run twice or against a dirty checkout).
#   2. `go mod edit -module <NEW_MODULE>`.
#   3. Rewrites repo-URL / import-path / string-literal patterns across the
#      repo, in order of specificity (most specific first):
#        github.com/myxiaoao/go-skeleton  →  <NEW_REPO_URL>
#        "go-skeleton-test"               →  "<SHORTNAME>-test"
#        "go-skeleton"                    →  "<SHORTNAME>"
#        /etc/go-skeleton/                →  /etc/<SHORTNAME>/
#        /opt/go-skeleton/                →  /opt/<SHORTNAME>/
#        go-skeleton/                     →  <MODULE>/
#        go-skeleton-                     →  <SHORTNAME>-
#        chown / install bare user|group  →  <SHORTNAME>
#   4. Renames systemd unit files
#        deploy/systemd/go-skeleton-{api,worker,migrate}.service
#          →                <SHORTNAME>-{api,worker,migrate}.service
#   5. Updates Makefile MODULE / DOCKER_IMAGE / ldflags / tarball name.
#   6. Updates .golangci.yml gci prefix() and gofumpt module-path.
#   7. Optionally (RENAME_BARE=1) rewrites bare `go-skeleton` mentions in
#      markdown prose.
#   8. Runs `make fmt && make verify`-equivalent gate to confirm result lints.
#   9. Prints any remaining `go-skeleton` mentions so you can decide manually.
#  10. Reminds you to delete this script — it's a one-shot rename, not a
#      maintained tool.
#
# Files NOT touched (intentional):
#   - CHANGELOG.md historical entries that mention `go-skeleton` as the
#     skeleton's identity — they describe past state.
#   - Makefile comments that explain why the short module name caused
#     historical issues — informational.
#   - README / docs that explain "this is a Go skeleton" — those are about
#     the upstream project, not your fork. Pass RENAME_BARE=1 to override.

set -euo pipefail

usage() {
  cat <<'EOF' >&2
Usage: ./scripts/rename.sh <NEW_MODULE> <NEW_SHORTNAME> [NEW_REPO_URL]

  NEW_MODULE       Full Go module path, e.g. github.com/acme/payments
  NEW_SHORTNAME    Short service name, e.g. payments
                   Used for systemd unit names, Docker images, JWT issuer,
                   docker-compose container names, OS user/group.
  NEW_REPO_URL     Optional. Upstream repo URL with no scheme / no trailing
                   slash, e.g. github.com/acme/payments. Replaces the
                   hard-coded `github.com/myxiaoao/go-skeleton` references
                   in deploy docs and unit-file Documentation= lines. When
                   omitted, NEW_MODULE is used as a fallback (works for
                   GitHub-shaped modules; custom git hosts should pass it).

Environment:
  RENAME_BARE=1    Also rewrite bare `go-skeleton` mentions in markdown.
                   Default off (preserves README upstream-attribution).

Examples:
  ./scripts/rename.sh github.com/acme/payments       payments
  ./scripts/rename.sh github.com/acme/payments       payments  github.com/acme/payments
  ./scripts/rename.sh git.internal/team/foo-service  foo       git.internal/team/foo-service
EOF
  exit 2
}

if [ $# -lt 2 ] || [ $# -gt 3 ]; then
  usage
fi

NEW_MODULE="$1"
NEW_SHORTNAME="$2"
NEW_REPO_URL="${3:-$NEW_MODULE}"
OLD_MODULE="go-skeleton"
OLD_SHORTNAME="go-skeleton"
OLD_REPO_URL="github.com/myxiaoao/go-skeleton"

# --- 0. preflight ---------------------------------------------------------

if [ ! -f go.mod ]; then
  echo "rename: must be run from the repo root (no go.mod here)" >&2
  exit 1
fi

current_module="$(awk '$1=="module"{print $2; exit}' go.mod)"
if [ "$current_module" != "$OLD_MODULE" ]; then
  echo "rename: refusing to run — go.mod module is '$current_module', not '$OLD_MODULE'." >&2
  echo "        This script is one-shot. If you need to rename again, do it manually." >&2
  exit 1
fi

if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
  echo "rename: refusing to run — working tree is dirty. Commit or stash first." >&2
  exit 1
fi

# Validate args look reasonable.
case "$NEW_MODULE" in
  go-skeleton|"")
    echo "rename: NEW_MODULE looks bogus: '$NEW_MODULE'" >&2; exit 1 ;;
esac
case "$NEW_SHORTNAME" in
  go-skeleton|"")
    echo "rename: NEW_SHORTNAME looks bogus: '$NEW_SHORTNAME'" >&2; exit 1 ;;
  *[!a-zA-Z0-9_-]*)
    echo "rename: NEW_SHORTNAME must be [a-zA-Z0-9_-]+, got '$NEW_SHORTNAME'" >&2; exit 1 ;;
esac
case "$NEW_REPO_URL" in
  *://*|*go-skeleton*)
    echo "rename: NEW_REPO_URL must not include scheme and must not contain go-skeleton: '$NEW_REPO_URL'" >&2
    exit 1 ;;
  */)
    echo "rename: NEW_REPO_URL must not have trailing slash: '$NEW_REPO_URL'" >&2
    exit 1 ;;
esac

echo "rename: applying"
echo "  module:    $OLD_MODULE   →  $NEW_MODULE"
echo "  shortname: $OLD_SHORTNAME → $NEW_SHORTNAME"
echo "  repo URL:  $OLD_REPO_URL → $NEW_REPO_URL"
if [ "${RENAME_BARE:-0}" = "1" ]; then
  echo "  bare-word: ON (RENAME_BARE=1) — markdown 'go-skeleton' mentions will be rewritten"
fi
echo ""

# --- 1. go.mod ------------------------------------------------------------

go mod edit -module "$NEW_MODULE"
echo "  ✓ go mod edit"

# --- 2. file-content rewrites --------------------------------------------

# Collect target files: tracked text-ish files only, exclude generated /
# vendored / binary blobs. Bash 3.2 (macOS default) doesn't have mapfile,
# so we read line-by-line into a variable separated by NUL bytes — handles
# spaces in filenames if anyone is so inclined.
files=()
while IFS= read -r f; do
  files+=("$f")
done < <(
  git ls-files \
    | grep -Ev '^(internal/oapi/|dist/|bin/|vendor/)' \
    | grep -Ev '\.(png|jpg|jpeg|gif|ico|pdf)$'
)

# Rewrite patterns in order of specificity (most specific first so later
# rules don't double-replace). The full ordering is:
#
#   1. github.com/myxiaoao/go-skeleton  →  <NEW_REPO_URL>
#       MUST run before any `go-skeleton/` rule, otherwise `go-skeleton/`
#       eats the URL path and produces `https://github.com/myxiaoao/<MODULE>/...`
#       garbage. Covers: ISSUE_TEMPLATE/config.yml discussions URL,
#       docs/deploy.md release tarball BASE, cosign --certificate-identity-regexp,
#       systemd Documentation=, AGENTS/CLAUDE "remote origin" notes.
#   2. "go-skeleton-test"   →  "<SHORTNAME>-test"
#       Go test JWT issuer fixture.
#   3. "go-skeleton"        →  "<SHORTNAME>"
#       Quoted string literals: JWT_ISSUER default, config default.
#   4. /etc/go-skeleton/    →  /etc/<SHORTNAME>/
#   5. /opt/go-skeleton/    →  /opt/<SHORTNAME>/
#       systemd EnvironmentFile / ExecStart paths — filesystem paths, so
#       take SHORTNAME, not MODULE.
#   6. go-skeleton/         →  <MODULE>/
#       Go import paths and ldflags -X 'go-skeleton/pkg/buildinfo...'.
#   7. go-skeleton-         →  <SHORTNAME>-
#       systemd unit names, docker container_name, image tag prefix, tarball
#       filename prefix. Runs after slash patterns so it doesn't catch them.
#   8. user / group / chown / install -o / -g / sudo -u  go-skeleton
#       →  <SHORTNAME>
#       Bare user/group names in shell commands (cannot use blanket bare-word
#       rule — would catch markdown prose). These are explicit shell-command
#       forms that are safe to rewrite unconditionally.
#   9. Kubernetes labels / namespace / kubectl -n  →  <SHORTNAME>
#       app.kubernetes.io/{name,part-of}, namespace declarations, Namespace
#       resource name, ServiceMonitor matchNames list, kubectl -n flag,
#       kubectl create namespace. K8s identifiers must be RFC 1123 (no
#       slashes), so SHORTNAME is the right target, never MODULE.
#
# Each sed uses | as delimiter — we already validated SHORTNAME to be
# [a-zA-Z0-9_-]+, and Go module / repo paths conventionally have no '|'.
# sed -i.bak then rm works on both macOS (BSD) and Linux (GNU).
for f in "${files[@]}"; do
  [ -f "$f" ] || continue
  # Skip the rename script itself — don't rewrite our own logic.
  case "$f" in scripts/rename.sh) continue ;; esac
  sed -i.bak \
      -e "s|github\\.com/myxiaoao/go-skeleton|${NEW_REPO_URL}|g" \
      -e "s|\"go-skeleton-test\"|\"${NEW_SHORTNAME}-test\"|g" \
      -e "s|\"go-skeleton\"|\"${NEW_SHORTNAME}\"|g" \
      -e "s|want go-skeleton|want ${NEW_SHORTNAME}|g" \
      -e "s|/etc/go-skeleton/|/etc/${NEW_SHORTNAME}/|g" \
      -e "s|/etc/go-skeleton$|/etc/${NEW_SHORTNAME}|g" \
      -e "s|/opt/go-skeleton/|/opt/${NEW_SHORTNAME}/|g" \
      -e "s|/opt/go-skeleton$|/opt/${NEW_SHORTNAME}|g" \
      -e "s|^User=go-skeleton$|User=${NEW_SHORTNAME}|g" \
      -e "s|^Group=go-skeleton$|Group=${NEW_SHORTNAME}|g" \
      -e "s|root:go-skeleton|root:${NEW_SHORTNAME}|g" \
      -e "s|^Description=go-skeleton |Description=${NEW_SHORTNAME} |g" \
      -e "s|title: go-skeleton API|title: ${NEW_SHORTNAME} API|g" \
      -e "s|the go-skeleton service|the ${NEW_SHORTNAME} service|g" \
      -e "s|^    name: go-skeleton$|    name: ${NEW_SHORTNAME}|g" \
      -e "s|go-skeleton/|${NEW_MODULE}/|g" \
      -e "s|go-skeleton-|${NEW_SHORTNAME}-|g" \
      -e "s|go-skeleton:go-skeleton|${NEW_SHORTNAME}:${NEW_SHORTNAME}|g" \
      -e "s| -o go-skeleton | -o ${NEW_SHORTNAME} |g" \
      -e "s| -g go-skeleton | -g ${NEW_SHORTNAME} |g" \
      -e "s| -g go-skeleton$| -g ${NEW_SHORTNAME}|g" \
      -e "s| -u go-skeleton | -u ${NEW_SHORTNAME} |g" \
      -e "s|nologin go-skeleton$|nologin ${NEW_SHORTNAME}|g" \
      -e "s|nologin go-skeleton |nologin ${NEW_SHORTNAME} |g" \
      -e "s|grep go-skeleton$|grep ${NEW_SHORTNAME}|g" \
      -e "s|grep go-skeleton |grep ${NEW_SHORTNAME} |g" \
      -e "s|app.kubernetes.io/name: go-skeleton$|app.kubernetes.io/name: ${NEW_SHORTNAME}|g" \
      -e "s|app.kubernetes.io/part-of: go-skeleton$|app.kubernetes.io/part-of: ${NEW_SHORTNAME}|g" \
      -e "s|^namespace: go-skeleton$|namespace: ${NEW_SHORTNAME}|g" \
      -e "s|^  name: go-skeleton$|  name: ${NEW_SHORTNAME}|g" \
      -e "s|^      - go-skeleton$|      - ${NEW_SHORTNAME}|g" \
      -e "s|kubectl -n go-skeleton |kubectl -n ${NEW_SHORTNAME} |g" \
      -e "s|kubectl create namespace go-skeleton$|kubectl create namespace ${NEW_SHORTNAME}|g" \
      -e "s|go-skeleton\\.tar\\.gz|${NEW_SHORTNAME}.tar.gz|g" \
      -- "$f"
  rm -- "${f}.bak"
done
echo "  ✓ content rewrites across ${#files[@]} files"

# --- 3. Bare go-skeleton values that need explicit context --------------
#
# These are configuration values where `go-skeleton` appears as a whole
# word (no slash, no dash, no quote) and the surrounding context tells
# us whether to substitute MODULE or SHORTNAME. Section 2's blanket
# rewrites can't handle these without false positives.

# Makefile: MODULE := go-skeleton  →  MODULE := <MODULE>
# (BSD sed compat: [[:space:]], no \s. Use POSIX character class.)
if grep -Eq '^MODULE[[:space:]]*:=[[:space:]]*go-skeleton$' Makefile 2>/dev/null; then
  sed -i.bak -E "s|^MODULE([[:space:]]*):=([[:space:]]*)go-skeleton$|MODULE\\1:=\\2${NEW_MODULE}|" Makefile
  rm Makefile.bak
fi

# Makefile: DOCKER_IMAGE ?= go-skeleton  →  DOCKER_IMAGE ?= <SHORTNAME>
if grep -Eq '^DOCKER_IMAGE[[:space:]]*\?=[[:space:]]*go-skeleton$' Makefile 2>/dev/null; then
  sed -i.bak -E "s|^DOCKER_IMAGE([[:space:]]*)\\?=([[:space:]]*)go-skeleton$|DOCKER_IMAGE\\1?=\\2${NEW_SHORTNAME}|" Makefile
  rm Makefile.bak
fi

# .env.example: JWT_ISSUER=go-skeleton  →  JWT_ISSUER=<SHORTNAME>
if grep -q '^JWT_ISSUER=go-skeleton$' .env.example 2>/dev/null; then
  sed -i.bak "s|^JWT_ISSUER=go-skeleton$|JWT_ISSUER=${NEW_SHORTNAME}|" .env.example
  rm .env.example.bak
fi

# .golangci.yml: module-path: go-skeleton  →  module-path: <MODULE>
if grep -Eq '^[[:space:]]*module-path:[[:space:]]*go-skeleton$' .golangci.yml 2>/dev/null; then
  sed -i.bak -E "s|^([[:space:]]*module-path:[[:space:]]*)go-skeleton$|\\1${NEW_MODULE}|" .golangci.yml
  rm .golangci.yml.bak
fi

# .golangci.yml: prefix(go-skeleton)  →  prefix(<MODULE>)
if grep -q 'prefix(go-skeleton)' .golangci.yml 2>/dev/null; then
  sed -i.bak "s|prefix(go-skeleton)|prefix(${NEW_MODULE})|g" .golangci.yml
  rm .golangci.yml.bak
fi

echo "  ✓ Makefile / .env.example / .golangci.yml"

# --- 4. systemd unit file renames ----------------------------------------

if [ -d deploy/systemd ]; then
  for proc in api worker migrate; do
    old="deploy/systemd/go-skeleton-${proc}.service"
    new="deploy/systemd/${NEW_SHORTNAME}-${proc}.service"
    if [ -f "$old" ]; then
      git mv -- "$old" "$new"
    fi
  done
  echo "  ✓ systemd unit filenames"
fi

# --- 5. Optional: bare-word `go-skeleton` rewrite -----------------------
#
# Default off because README / AGENTS / CLAUDE have prose like
# "把 go-skeleton 装到 Linux 主机上" or "go-skeleton 项目约定" that
# describe the upstream skeleton, not the fork. With RENAME_BARE=1 the
# fork owner says "I want everything to mention my service name" and we
# do a final pass on all markdown / yaml prose. CHANGELOG is still
# skipped — historical entries describe the past.

if [ "${RENAME_BARE:-0}" = "1" ]; then
  bare_files=()
  while IFS= read -r f; do
    bare_files+=("$f")
  done < <(
    git ls-files \
      | grep -Ev '^(internal/oapi/|dist/|bin/|vendor/|CHANGELOG\.md$|scripts/rename\.sh$)' \
      | grep -Ev '\.(png|jpg|jpeg|gif|ico|pdf)$' \
      | xargs grep -l 'go-skeleton' 2>/dev/null || true
  )
  if [ ${#bare_files[@]} -gt 0 ]; then
    for f in "${bare_files[@]}"; do
      [ -f "$f" ] || continue
      # \b in BSD sed is not portable; use [[:<:]] / [[:>:]] for word
      # boundaries on macOS, fall back to GNU \< \> on Linux. Cheapest
      # cross-platform option: surround with a character class that's
      # neither alnum nor dash/dot/slash. This is intentionally
      # conservative — only replaces `go-skeleton` flanked by space,
      # paren, comma, period (Chinese 。), backtick, etc.
      sed -i.bak \
          -e "s|\`go-skeleton\`|\`${NEW_SHORTNAME}\`|g" \
          -e "s| go-skeleton | ${NEW_SHORTNAME} |g" \
          -e "s|^go-skeleton |${NEW_SHORTNAME} |g" \
          -e "s| go-skeleton$| ${NEW_SHORTNAME}|g" \
          -e "s| go-skeleton\\.| ${NEW_SHORTNAME}.|g" \
          -e "s| go-skeleton,| ${NEW_SHORTNAME},|g" \
          -e "s| go-skeleton)| ${NEW_SHORTNAME})|g" \
          -e "s|把 go-skeleton |把 ${NEW_SHORTNAME} |g" \
          -e "s|（go-skeleton |（${NEW_SHORTNAME} |g" \
          -e "s|go-skeleton 项目|${NEW_SHORTNAME} 项目|g" \
          -e "s|、go-skeleton |、${NEW_SHORTNAME} |g" \
          -e "s|、go-skeleton$|、${NEW_SHORTNAME}|g" \
          -e "s|、go-skeleton\\.|、${NEW_SHORTNAME}.|g" \
          -e "s|、go-skeleton。|、${NEW_SHORTNAME}。|g" \
          -e "s|、go-skeleton|、${NEW_SHORTNAME}|g" \
          -- "$f"
      rm -- "${f}.bak"
    done
    echo "  ✓ bare-word rewrite across ${#bare_files[@]} files (RENAME_BARE=1)"
  fi
fi

# --- 6. final fmt + (partial) verify -------------------------------------

# Clean lint cache: golangci-lint caches abs paths and gets confused after
# bulk rewrites (especially across worktrees), surfacing as ghost issues
# pointing at non-existent files. Cheap to clean here, expensive to debug
# later. Best-effort — older versions don't have `cache clean`.
golangci-lint cache clean >/dev/null 2>&1 || true

# Regenerate the embedded OpenAPI spec (its base64 changes because we
# rewrote `title: <name> API` in api/openapi.yaml).
echo ""
echo "rename: regenerating OpenAPI artifact + running checks…"
make oapi >/dev/null 2>&1 || {
  echo "rename: make oapi failed; run manually to inspect" >&2; exit 1;
}

# Skip oapi-verify here because it checks git-diff against HEAD, and we
# have intentionally uncommitted rewrites. Run the rest of the gate.
make fmt >/dev/null 2>&1 || true
for step in vet test lint docs-verify; do
  if ! make "$step" >/dev/null 2>&1; then
    echo "" >&2
    echo "rename: make $step FAILED after rewrites." >&2
    echo "        Run 'make $step' interactively to inspect." >&2
    echo "        Your repo state is intact — files have been rewritten but not committed." >&2
    exit 1
  fi
done
echo "  ✓ fmt / vet / test / lint / docs-verify clean"

# --- 7. surface remaining mentions ----------------------------------------
#
# Anything that still contains `go-skeleton` after the rewrites is either:
#   (a) intentional historical attribution (CHANGELOG entries, "this is a
#       Go skeleton" notes in README) — fine to leave;
#   (b) something we missed — needs manual inspection.
#
# Print them so the user can decide, instead of silently leaving them.
echo ""
echo "rename: remaining 'go-skeleton' mentions (review manually):"
remaining=$(git grep -n 'go-skeleton' -- ':!scripts/rename.sh' 2>/dev/null || true)
if [ -z "$remaining" ]; then
  echo "  (none — repo is fully renamed)"
else
  echo "$remaining" | sed 's/^/  /'
  echo ""
  echo "  Most of these are intentional (CHANGELOG history, upstream-attribution"
  echo "  prose). Anything that surprises you, fix by hand."
fi

# --- 8. done --------------------------------------------------------------

cat <<EOF

rename: done.
  - go.mod module:   $NEW_MODULE
  - service short:   $NEW_SHORTNAME
  - repo URL:        $NEW_REPO_URL

Manual pass checklist (review the diff before committing):
  - CHANGELOG history entries (left as-is by design)
  - Markdown prose like "this is a Go skeleton" — rerun with
    RENAME_BARE=1 if you want those rewritten too
  - Makefile comments explaining historical short-module-name pitfalls
  - Anything listed in the "remaining mentions" block above

Next steps:
  1. Review the diff:        git diff
  2. Commit:                 git add -A && git commit -m 'chore: rename to $NEW_MODULE'
  3. Delete this script:     git rm scripts/rename.sh && git commit -m 'chore: drop rename script (one-shot)'
EOF
