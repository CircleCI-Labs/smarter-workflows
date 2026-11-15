#!/bin/sh
# Release circleci-smarter for tag pipelines (vX.Y.Z).
#
#   scripts/release.sh              build, SHA256SUMS, sign, publish
#   scripts/release.sh install-tools   cosign and gh (pinned)
#
# Builds linux/darwin amd64+arm64 and windows/amd64 with CGO_ENABLED=0 and
# -trimpath. Signs SHA256SUMS with cosign (keyless, circleci run oidc get).
# Creates a GitHub release when GITHUB_TOKEN is set (context
# smarter-workflows-release).
#
# Environment:
#   CIRCLE_TAG            release tag (required)
#   PIPELINE_DEFINITION_ID  must match the signing pipeline (required to sign)
#   GITHUB_TOKEN          from smarter-workflows-release; without it, build
#                         and sign still run but nothing is published
#   RELEASE_SKIP_SIGN=1   local dry run: build and SHA256SUMS only
#   RELEASE_OUT           output directory (default /tmp/release)
#
# Never run with set -x: tokens may be in the environment.
set -eu

expected_def=12eb0a5c-4f1c-4ebe-a1e5-d8dda5c13c53
oidc_issuer=https://oidc.circleci.com
out=${RELEASE_OUT:-/tmp/release}
bin=${RELEASE_BIN:-${HOME}/.local/bin}
root=$(cd "$(dirname "$0")/.." && pwd)

# shellcheck source=orb/src/scripts/install.sh
. "$root/orb/src/scripts/install.sh"
# shellcheck source=scripts/github-slug.sh
. "$root/scripts/github-slug.sh"

say() { printf '==> %s\n' "$*"; }
die() { printf 'release: %s\n' "$*" >&2; exit 1; }

fetch_bin() {
  url=$1
  want=$2
  file=$3
  curl -fsSL --proto '=https' --tlsv1.2 --retry 5 --retry-all-errors -o "$file" "$url" ||
    die "cannot download $url"
  got=$(sha256_of "$file")
  [ "$got" = "$want" ] || die "SHA-256 of $url does not match the pinned value"
}

install_tools() {
  case $(uname -s) in
    Linux) ;;
    *) die "install-tools supports Linux only" ;;
  esac
  case $(uname -m) in
    x86_64) ;;
    *) die "install-tools supports x86_64 only" ;;
  esac
  mkdir -p "$bin"
  tmp=$(mktemp -d)
  # renovate: datasource=github-releases depName=cli/cli extractVersion=^v(?<version>.*)$
  gh_version=2.102.0
  gh_sha256=bb766f710eef8ede859c18578c72c327597cd4c8a85b06001b1f3843c6019386
  fetch_bin "https://github.com/cli/cli/releases/download/v${gh_version}/gh_${gh_version}_linux_amd64.tar.gz" \
    "$gh_sha256" "$tmp/gh.tar.gz"
  tar -xzf "$tmp/gh.tar.gz" -C "$tmp"
  install -m 0755 "$tmp/gh_${gh_version}_linux_amd64/bin/gh" "$bin/gh"
  install_cosign "$bin"
  rm -rf "$tmp"
  gh --version | head -n 1
  cosign version 2>&1 | grep -E '^GitVersion' || true
}

version_from_tag() {
  tag=${CIRCLE_TAG:-}
  [ -n "$tag" ] || die "CIRCLE_TAG is not set"
  printf '%s\n' "$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' ||
    die "CIRCLE_TAG must be a release tag vX.Y.Z, not $tag"
  version=$tag
}

build_binaries() {
  say "Build circleci-smarter $version"
  host_os=linux
  host_arch=amd64
  case $(uname -s) in
    Linux) host_os=linux ;;
    Darwin) host_os=darwin ;;
  esac
  case $(uname -m) in
    aarch64 | arm64) host_arch=arm64 ;;
    x86_64 | amd64) host_arch=amd64 ;;
  esac
  mkdir -p "$out"
  for spec in linux:amd64 linux:arm64 darwin:amd64 darwin:arm64 windows:amd64; do
    os=${spec%%:*}
    arch=${spec##*:}
    ext=
    [ "$os" = windows ] && ext=.exe
    name=circleci-smarter_${version}_${os}_${arch}${ext}
    env GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 go build -trimpath \
      -ldflags "-X main.version=${version}" \
      -o "$out/$name" "$root/cmd/circleci-smarter" ||
      die "go build failed for $os/$arch"
    if [ "$os" = "$host_os" ] && [ "$arch" = "$host_arch" ]; then
      got=$("$out/$name" version) || die "$name does not run"
      [ "$got" = "$version" ] || die "$name reports version $got, want $version"
    fi
  done
  (
    cd "$out" || exit 1
    sha256sum circleci-smarter_"${version}"_* > SHA256SUMS
  )
}

sign_sums() {
  def=${PIPELINE_DEFINITION_ID:-}
  [ -n "$def" ] || die "PIPELINE_DEFINITION_ID is not set"
  [ "$def" = "$expected_def" ] || die "PIPELINE_DEFINITION_ID is not the release pipeline ($expected_def)"
  id="https://circleci.com/api/v2/projects/${CIRCLE_PROJECT_ID:?}/pipeline-definitions/${def}"
  install_cosign "$out/tools"
  cosign_bin=$out/tools/cosign
  say "Sign SHA256SUMS (keyless) for $version"
  SIGSTORE_ID_TOKEN="$(circleci run oidc get --claims '{"aud":"sigstore"}' --root-issuer)" \
    "$cosign_bin" sign-blob --yes --bundle "$out/SHA256SUMS.sigstore.json" "$out/SHA256SUMS" > /dev/null
  if ! "$cosign_bin" verify-blob --bundle "$out/SHA256SUMS.sigstore.json" \
    --certificate-identity "$id" --certificate-oidc-issuer "$oidc_issuer" "$out/SHA256SUMS" > /dev/null 2>&1; then
    die "SHA256SUMS signature does not verify for this project"
  fi
  signed_for "$out/SHA256SUMS.sigstore.json" "refs/tags/$version" ||
    die "the signing certificate does not name refs/tags/$version"
  say "SHA256SUMS is signed for $version"
}

publish_github() {
  if [ -z "${GITHUB_TOKEN:-}" ]; then
    say "publishing skipped: GITHUB_TOKEN is not set"
    return 0
  fi
  [ "${RELEASE_SKIP_SIGN:-}" != 1 ] || die "refusing to publish an unsigned release"
  slug=$(github_slug_from_env) || die "cannot resolve the GitHub repository"
  sha=${CIRCLE_SHA1:-$(git -C "$root" rev-parse HEAD)}
  assets="$out/SHA256SUMS"
  for f in "$out"/circleci-smarter_"${version}"_*; do
    assets="$assets $f"
  done
  assets="$assets $out/SHA256SUMS.sigstore.json"
  # shellcheck disable=SC2086
  set -- $assets
  if gh release view "$version" --repo "$slug" > /dev/null 2>&1; then
    say "Release $version exists: upload assets again"
    gh release upload "$version" "$@" --clobber --repo "$slug"
  else
    say "Create GitHub release $version"
    gh release create "$version" "$@" --repo "$slug" --target "$sha" \
      --title "$version" --notes "Planner release $version"
  fi
}

main() {
  cd "$root"
  version_from_tag
  version=$tag
  rm -rf "$out"
  mkdir -p "$out"
  build_binaries
  if [ "${RELEASE_SKIP_SIGN:-}" = 1 ]; then
    say "Signing skipped (RELEASE_SKIP_SIGN=1)"
  else
    sign_sums
  fi
  publish_github
  say "Release $version prepared in $out"
}

case ${1:-} in
  install-tools) install_tools ;;
  "") main ;;
  *) die "usage: scripts/release.sh [install-tools]" ;;
esac