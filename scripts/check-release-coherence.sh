#!/usr/bin/env bash
# Guards the release metadata against drift.
#
# A release is consumed without anyone reading the source, so a wrong install command or a
# stale claim in the notes ships to users and gets cached by proxies before anyone notices.
# Run before GoReleaser, so a bad release fails instead of publishing.
#
# Usage: check-release-coherence.sh <tag> [--repo owner/name]
set -euo pipefail

tag="${1:-}"
repo="${2:-E-Timileyin/Sail-CLI}"
# Tolerate a git URL or a trailing .git; the repo is only used in messages.
repo="${repo%.git}"
repo="${repo##*[:/]}"

if [ -z "$tag" ]; then
  echo "usage: $0 <tag> [owner/repo]" >&2
  exit 2
fi

fail() { echo "::error::$*" >&2; exit 1; }
pass() { echo "  ok: $*"; }

echo "Checking release coherence for tag '$tag' (repo $repo)"

# 1. Tag is a semver release tag. GoReleaser derives .Version by stripping the leading v,
#    and a tag like 'v1.0' or 'release-1' yields an empty or wrong .Version in asset names.
if ! printf '%s' "$tag" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'; then
  fail "tag '$tag' is not semver of the form vX.Y.Z (optionally with a -prerelease suffix)"
fi
version="${tag#v}"
pass "tag is valid semver, version '$version'"

# 2. The footer's install URL is the only install path users copy. Its asset name is built
#    from .Version, so a mismatch means a 404 for every user who follows it.
footer_line="$(grep -n 'releases/download' .goreleaser.yaml || true)"
if [ -z "$footer_line" ]; then
  echo "  warn: no install URL found in .goreleaser.yaml footer; skipping asset check"
else
  # The asset name must interpolate GoReleaser's version, never a hardcoded string.
  if ! printf '%s' "$footer_line" | grep -q 'sail_{{ \.Version }}_'; then
    fail "install URL in .goreleaser.yaml does not use {{ .Version }} in the asset name:
$footer_line"
  fi
  pass "install URL interpolates {{ .Version }}"
fi

# 3. No advertised install command that cannot resolve. v0.1.0 shipped exactly this: the
#    module path is not the repo URL, so 'go install <module>@latest' 404s. It must not
#    reappear as a live instruction.
#
#    Scoped to shell code fences only. Prose that names the command while warning it is
#    broken (README does this deliberately, see #16) is documentation of the defect, not an
#    advertisement for it, and must not fail the release.
advertised=""
for f in .goreleaser.yaml README.md; do
  [ -f "$f" ] || continue
  hits="$(awk '
    /^[[:space:]]*```/ { fence = !fence; next }
    fence && /go install[[:space:]]+github\.com\/E-Timileyin\/sail@/ { print FILENAME":"FNR": "$0 }
  ' "$f" || true)"
  [ -n "$hits" ] && advertised="$advertised$hits"$'\n'
done
if [ -n "$(printf '%s' "$advertised" | tr -d '[:space:]')" ]; then
  fail "an unresolvable 'go install' command is advertised in a shell block (module path != repo URL, see #16):
$advertised"
fi
pass "no unresolvable 'go install' command advertised"

# 4. The footer must not carry release-specific claims. It is permanent config, so wording
#    like 'this release changes X' becomes false on the next tag while staying in the notes.
stale_claim="$(grep -nEi 'this release (changes|introduces|removes|adds)' .goreleaser.yaml || true)"
if [ -n "$stale_claim" ]; then
  fail "release-specific wording in the permanent footer will be wrong on later tags:
$stale_claim"
fi
pass "footer carries no release-specific claims"

# 5. The changelog may not be empty. GoReleaser happily publishes a release with no notes,
#    and a user cannot tell that from a release whose commits were all filtered out.
if ! grep -q '^changelog:' .goreleaser.yaml; then
  fail ".goreleaser.yaml has no changelog section; release notes would be empty"
fi
pass "changelog is configured"

echo "Release coherence OK for $tag"
