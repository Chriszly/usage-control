#!/usr/bin/env bash
# Fixes known vulnerabilities in the backend's and the frontend's dependencies, for
# the nightly "Vulnerability fixes" workflow:
# - Go: every vulnerability govulncheck finds in code the backend calls, the
#   standard library included, is raised to the version that fixes it
# - npm: `npm audit fix` updates package-lock.json within the ranges package.json allows
# The changes are left in the working tree. A list of them goes to the file given as
# the first argument, for the pull request. Exits 1 when a vulnerability is left that
# none of this fixes, so the workflow fails and someone looks at it; once it got that
# far, it creates <summary file>.done, which an exit on an error before leaves out.
set -euo pipefail

summary="${1:?usage: ci/fix-vulnerabilities.sh <summary file>}"
root="$(cd "$(dirname "$0")/.." && pwd)"
: >"$summary"
rm -f "$summary.done"
unfixed=0

# Prints "<module> <fixed version>" for each module with a vulnerability the code
# calls, with the highest version that fixes all of that module's ones. A module
# with no fix for any of them prints "-" as its version. Reads govulncheck's JSON output.
go_fixes() {
  jq -r 'select(.finding) | .finding
         | select(.trace[0].function)
         | "\(.trace[0].module) \(.fixed_version // "-")"' |
    sort -u | sort -k1,1 -k2,2V |
    awk '$2 == "-" { if (!($1 in last)) last[$1] = "-"; next }
         { last[$1] = $2 }
         END { for (m in last) print m, last[m] }' | sort
}

cd "$root/backend"
# Raising the Go version in go.mod needs that newer Go, and so does every go command
# after it, so let go switch to the version go.mod asks for instead of the installed one.
export GOTOOLCHAIN=auto
report="$(mktemp)"
go tool govulncheck -format json ./... >"$report"
while read -r module version; do
  if [[ "$version" == "-" ]]; then
    echo "::warning::$module has a vulnerability without a fixed version yet"
    continue
  fi
  case "$module" in
    # The standard library and the go command come with Go itself: raise the Go
    # version in go.mod, which CI, the release builds and the Dockerfile's floating
    # golang:1.x image follow.
    stdlib | toolchain) target="go@${version#v}" ;;
    *) target="$module@$version" ;;
  esac
  echo "Updating $target"
  go get "$target"
  echo "- Go: \`$module\` to \`$version\`" >>"$summary"
done < <(go_fixes <"$report")
go mod tidy
if ! go tool govulncheck ./...; then
  unfixed=1
fi

cd "$root/frontend"
# Only the lock file changes; `npm audit fix` never takes a new major version here.
npm audit fix --package-lock-only --ignore-scripts || true
if ! git diff --quiet -- package-lock.json; then
  echo "- npm: \`npm audit fix\` updated package-lock.json" >>"$summary"
fi
# The same level the CI workflow fails at.
if ! npm audit --package-lock-only --audit-level=high; then
  unfixed=1
fi

: >"$summary.done"
if ((unfixed)); then
  echo "::error::Vulnerabilities are left that no update fixes yet; see the output above"
  exit 1
fi
