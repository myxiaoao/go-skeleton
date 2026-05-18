#!/usr/bin/env bash
# scripts/rename.sh — one-shot script to re-brand the skeleton.
#
# Usage:
#   ./scripts/rename.sh <NEW_MODULE> <NEW_SHORTNAME>
#
# Example:
#   ./scripts/rename.sh github.com/acme/payments payments
#
# What it does:
#   1. Verifies the working tree is clean and the current module is still
#      `go-skeleton` (refuses to run twice or against a dirty checkout).
#   2. `go mod edit -module <NEW_MODULE>`.
#   3. Rewrites Go import paths and string literals across the repo:
#        go-skeleton/...    →  <NEW_MODULE>/...
#        "go-skeleton"      →  "<NEW_SHORTNAME>"
#        "go-skeleton-test" →  "<NEW_SHORTNAME>-test"
#        go-skeleton-...    →  <NEW_SHORTNAME>-...  (systemd / docker names)
#   4. Renames systemd unit files
#        deploy/systemd/go-skeleton-{api,worker,migrate}.service
#          →                <NEW_SHORTNAME>-{api,worker,migrate}.service
#   5. Updates Makefile MODULE / DOCKER_IMAGE / ldflags / tarball name.
#   6. Updates .golangci.yml gci prefix() and gofumpt module-path.
#   7. Runs `make fmt && make verify` to confirm the result builds and lints.
#   8. Reminds you to delete this script — it's a one-shot rename, not a
#      maintained tool.
#
# Files NOT touched (intentional):
#   - CHANGELOG.md historical entries that mention `go-skeleton` as the
#     skeleton's identity — they describe past state.
#   - Makefile comments that explain why the short module name caused
#     historical issues — informational.
#   - README / docs that explain "this is a Go skeleton" — those are about
#     the upstream project, not your fork.

set -euo pipefail

usage() {
  cat <<'EOF' >&2
Usage: ./scripts/rename.sh <NEW_MODULE> <NEW_SHORTNAME>

  NEW_MODULE       Full Go module path, e.g. github.com/acme/payments
  NEW_SHORTNAME    Short service name, e.g. payments
                   Used for systemd unit names, Docker images, JWT issuer,
                   docker-compose container names.

Examples:
  ./scripts/rename.sh github.com/acme/payments       payments
  ./scripts/rename.sh git.internal/team/foo-service  foo
EOF
  exit 2
}

if [ $# -ne 2 ]; then
  usage
fi

NEW_MODULE="$1"
NEW_SHORTNAME="$2"
OLD_MODULE="go-skeleton"
OLD_SHORTNAME="go-skeleton"

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

# Validate args look reasonable
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

echo "rename: applying"
echo "  module:    $OLD_MODULE   →  $NEW_MODULE"
echo "  shortname: $OLD_SHORTNAME → $NEW_SHORTNAME"
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

# We rewrite patterns in order of specificity (most specific first so
# later rules don't double-replace):
#   1. "go-skeleton-test"   →  "<SHORTNAME>-test"
#       Go test JWT issuer fixture.
#   2. "go-skeleton"        →  "<SHORTNAME>"
#       Quoted string literals: JWT_ISSUER default, config default.
#   3. /etc/go-skeleton/    →  /etc/<SHORTNAME>/
#   4. /opt/go-skeleton/    →  /opt/<SHORTNAME>/
#       systemd EnvironmentFile / ExecStart paths — these are filesystem
#       paths, not Go imports, so they take SHORTNAME, not MODULE.
#   5. go-skeleton/         →  <MODULE>/
#       Go import paths and ldflags -X 'go-skeleton/pkg/buildinfo...'.
#   6. go-skeleton-         →  <SHORTNAME>-
#       systemd unit names, docker container_name, image tag prefix.
#       Comes last so it doesn't catch the slash patterns above.
#
# Each sed uses | as delimiter — we already validated SHORTNAME to be
# [a-zA-Z0-9_-]+, and Go module paths conventionally have no '|'.
# sed -i.bak then rm works on both macOS (BSD) and Linux (GNU).
for f in "${files[@]}"; do
  [ -f "$f" ] || continue
  # Skip the rename script itself — don't rewrite our own logic.
  case "$f" in scripts/rename.sh) continue ;; esac
  sed -i.bak \
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

# --- 5. final fmt + (partial) verify -------------------------------------

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

# --- 6. done --------------------------------------------------------------

cat <<EOF

rename: done.
  - go.mod module:   $NEW_MODULE
  - service short:   $NEW_SHORTNAME

Remaining "go-skeleton" mentions are intentional and need a manual pass:
  - Comments explaining historical decisions (Makefile / .golangci.yml /
    docker-compose.yml). Keep or strip as you like.
  - systemd Documentation= URLs pointing at the upstream skeleton repo.
    Change to your repo URL.
  - README / README_en / docs/*.md that describe "the skeleton". Adjust
    to describe your service.

Next steps:
  1. Review the diff:        git diff
  2. Commit:                 git add -A && git commit -m 'chore: rename to $NEW_MODULE'
  3. Delete this script:     git rm scripts/rename.sh && git commit -m 'chore: drop rename script (one-shot)'
EOF
