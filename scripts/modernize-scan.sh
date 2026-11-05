#!/usr/bin/env bash
# modernize-scan.sh — read-only modernization scan for common-go.
#
# Produces a Markdown report describing how far the library is from "current":
# Go toolchain, dependency updates, vulnerabilities, modernizer suggestions
# (`go fix -diff`), vet/lint, tests, non-English text, and whether the reference
# downstream project (auth) still builds against the local checkout.
#
# It never modifies tracked files in this repo or the downstream repo. The
# downstream build uses a throwaway go.work in a temp dir.
#
# Usage:
#   scripts/modernize-scan.sh                    # report to stdout
#   scripts/modernize-scan.sh docs/maintenance/scans/$(date +%F).md
#
# Env:
#   DOWNSTREAM   path to the reference consumer module (default: ../auth/backend)
#   SKIP_TESTS=1 skip `go test -race` (slow)
#   SKIP_NET=1   skip anything that needs the network (dep updates, govulncheck, latest Go)
#
# See docs/maintenance/playbook.md for how the report is used.
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DOWNSTREAM="$(realpath -m "${DOWNSTREAM:-$ROOT/../auth/backend}")"
OUT="${1:-/dev/stdout}"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
cd "$ROOT" || exit 1

# Tool versions are pinned so two scans a quarter apart are comparable.
# Bump them deliberately as step 1 of each cycle (playbook §3.1).
GOVULNCHECK="golang.org/x/vuln/cmd/govulncheck@latest"
GOLANGCI="github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest"

section() { printf '\n## %s\n\n' "$1"; }
fence()   { printf '```text\n'; cat; printf '```\n'; }
status()  { if [ "$1" -eq 0 ]; then echo "**PASS**"; else echo "**FAIL (exit $1)**"; fi; }

{
echo "# Modernization scan — $(date +%F)"
echo
echo "- Commit: \`$(git rev-parse --short HEAD)\` on \`$(git rev-parse --abbrev-ref HEAD)\`"
echo "- Local toolchain: \`$(go env GOVERSION)\`; go.mod: \`$(awk '/^go /{print $2}' go.mod)\`"
if [ -z "${SKIP_NET:-}" ]; then
  echo "- Latest stable Go: \`$(curl -fsS --max-time 10 'https://go.dev/VERSION?m=text' | head -1 || echo unknown)\`"
fi

section "1. Build and vet"
go build ./... >"$TMP/build" 2>&1; rc=$?; echo "go build: $(status $rc)"; [ $rc -ne 0 ] && fence <"$TMP/build"
go vet ./... >"$TMP/vet" 2>&1; rc=$?; echo; echo "go vet: $(status $rc)"; [ $rc -ne 0 ] && fence <"$TMP/vet"

section "2. Modernizers (go fix -diff)"
echo "Suggested rewrites to current Go idioms. Review them; they are not applied."
echo
go fix -diff ./... >"$TMP/fix" 2>&1
if [ -s "$TMP/fix" ]; then
  echo "Files touched: $(grep -c '^--- ' "$TMP/fix" || true)"
  echo; echo '<details><summary>diff</summary>'; echo; printf '```diff\n'; cat "$TMP/fix"; printf '```\n'; echo '</details>'
else
  echo "None."
fi

if [ -z "${SKIP_NET:-}" ]; then
  section "3. Direct dependency updates"
  go list -m -u -f '{{if and (not .Indirect) (not .Main) .Update}}| {{.Path}} | {{.Version}} | {{.Update.Version}} |{{end}}' all \
    >"$TMP/deps" 2>/dev/null
  if [ -s "$TMP/deps" ]; then
    echo "| Module | Current | Latest |"; echo "|---|---|---|"; cat "$TMP/deps"
  else
    echo "All direct dependencies are current."
  fi

  section "4. Vulnerabilities (govulncheck)"
  go run "$GOVULNCHECK" ./... >"$TMP/vuln" 2>&1; rc=$?
  echo "govulncheck: $(status $rc)"
  echo
  # One line per reachable vulnerability: ID, module, found/fixed versions.
  awk '/^Vulnerability #/{id=$3} /Found in:/{f=$3} /Fixed in:/{print "- " id " — " f " → fixed in " $3}' "$TMP/vuln" | sort -u
  echo
  grep -E '^Your code is affected|^This scan also found' "$TMP/vuln" || true

  section "5. Lint (golangci-lint)"
  if [ -f .golangci.yml ] || [ -f .golangci.yaml ]; then
    go run "$GOLANGCI" run ./... >"$TMP/lint" 2>&1; rc=$?
    echo "golangci-lint: $(status $rc) — $(grep -cE '^[^ ].*:[0-9]+:[0-9]+:' "$TMP/lint" || true) issues"
    fence < <(tail -60 "$TMP/lint")
  else
    echo "Skipped: no .golangci.yml in repo (adding one is a playbook gate)."
  fi
fi

section "6. Tests"
if [ -z "${SKIP_TESTS:-}" ]; then
  go test -race -count=1 ./... >"$TMP/test" 2>&1; rc=$?
  echo "go test -race: $(status $rc)"
  fence < <(grep -E '^(ok|FAIL|---|panic)|DATA RACE' "$TMP/test" | head -60)
  echo
  echo "Packages without tests: $(grep -c 'no test files' "$TMP/test" || true)"
else
  echo "Skipped (SKIP_TESTS=1)."
fi

section "7. Non-English text (CJK) in code and docs"
grep -rlP '[\x{3040}-\x{30ff}\x{3400}-\x{9fff}\x{ac00}-\x{d7af}]' \
  --include='*.go' --include='*.md' --include='*.proto' --include='*.yaml' --include='*.yml' \
  --include='*.ts' --include='*.js' --include='*.py' --include='*.html' \
  --exclude-dir=node_modules --exclude-dir=.git . >"$TMP/cjk" 2>/dev/null
if [ -s "$TMP/cjk" ]; then echo "Files:"; sed 's/^/- /' "$TMP/cjk"; else echo "None."; fi

section "8. Repository hygiene"
for f in go.sum .golangci.yml .github/workflows CHANGELOG.md; do
  if [ -e "$f" ] && { [ -d "$f" ] || git ls-files --error-unmatch "$f" >/dev/null 2>&1; }; then
    echo "- [x] \`$f\` tracked"
  else
    echo "- [ ] \`$f\` missing or untracked"
  fi
done
echo "- Latest tag: \`$(git describe --tags --abbrev=0 2>/dev/null || echo none)\`"
echo "- \`package main\` outside examples/docs: $(grep -rl '^package main' --include='*.go' . | grep -vE '^\./(examples|docs)/' | tr '\n' ' ' || true)"

section "9. Downstream: $(basename "$(dirname "$DOWNSTREAM")")/$(basename "$DOWNSTREAM")"
if [ -f "$DOWNSTREAM/go.mod" ]; then
  echo "- Pinned common-go: \`$(awk '/ti\/common-go /{print $2}' "$DOWNSTREAM/go.mod")\`"
  echo "- Downstream go.mod: \`$(awk '/^go /{print $2}' "$DOWNSTREAM/go.mod")\`"
  printf 'go %s\n\nuse (\n\t%s\n\t%s\n)\n' "$(go env GOVERSION | sed 's/^go//')" "$DOWNSTREAM" "$ROOT" >"$TMP/go.work"
  (cd "$DOWNSTREAM" && GOWORK="$TMP/go.work" go build ./... >"$TMP/ds-build" 2>&1); rc=$?
  echo "- Build against local checkout: $(status $rc)"; [ $rc -ne 0 ] && fence < <(head -40 "$TMP/ds-build")
  (cd "$DOWNSTREAM" && GOWORK="$TMP/go.work" go vet ./... >"$TMP/ds-vet" 2>&1); rc=$?
  echo "- Vet against local checkout: $(status $rc)"; [ $rc -ne 0 ] && fence < <(head -40 "$TMP/ds-vet")
  if [ -z "${SKIP_TESTS:-}" ]; then
    (cd "$DOWNSTREAM" && GOWORK="$TMP/go.work" go test -count=1 ./... >"$TMP/ds-test" 2>&1); rc=$?
    echo "- Tests against local checkout: $(status $rc)"
    [ $rc -ne 0 ] && fence < <(grep -E '^(FAIL|---|panic)' "$TMP/ds-test" | head -40)
  fi
  echo
  echo "Exported common-go symbols used downstream (top 40):"
  echo
  grep -rhoE '\b(grpcmux|mux|dependencies|graceful|config|database|dephttp|redis|broker|async|mqlru|codecs|query)\.[A-Z][A-Za-z0-9]+' \
    --include='*.go' $(grep -rlE '"github.com/ti/common-go' --include='*.go' "$DOWNSTREAM" | grep -v '\.pb\.') 2>/dev/null \
    | sort | uniq -c | sort -rn | head -40 | fence
else
  echo "Not found at \`$DOWNSTREAM\` (set DOWNSTREAM=...)."
fi
} >"$OUT"
