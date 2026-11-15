#!/bin/sh
# Pack and publish the Smarter Workflows orb.
#
#   scripts/publish-orb.sh dev          cci-labs/smarter-workflows@dev:SHA
#   scripts/publish-orb.sh production   cci-labs/smarter-workflows@X.Y.Z
#
# Needs circleci on PATH and CIRCLE_TOKEN (context smarter-workflows-release).
# The token is never printed. Stamps default_version in install.sh for production.
#
# Environment:
#   CIRCLE_TOKEN            required to publish
#   CIRCLE_SHA1             dev tag (dev mode)
#   CIRCLE_TAG              vX.Y.Z (production)
#   ORB_SRC                 orb source dir (default orb/src)
#   ORB_PACK_DIR            where orb.yml is written (default /tmp/smarter-orb)
set -eu

orb_name=cci-labs/smarter-workflows
root=$(cd "$(dirname "$0")/.." && pwd)
src=${ORB_SRC:-$root/orb/src}
pack_dir=${ORB_PACK_DIR:-/tmp/smarter-orb}
install_sh=$src/scripts/install.sh
stamp=$src/scripts/install.sh.stamp

say() { printf '==> %s\n' "$*"; }
die() { printf 'publish-orb: %s\n' "$*" >&2; exit 1; }

require_token() {
  if [ -z "${CIRCLE_TOKEN:-}" ]; then
    die "CIRCLE_TOKEN is not set (context smarter-workflows-release)"
  fi
}

restore_install() {
  if [ -f "$stamp" ]; then
    mv "$stamp" "$install_sh"
    rm -f "$stamp"
  fi
}

cleanup() {
  restore_install
}
trap cleanup EXIT

pack_orb() {
  mkdir -p "$pack_dir"
  circleci orb pack "$src" > "$pack_dir/orb.yml"
}

publish_to() {
  target=$1
  say "Publish $orb_name@$target"
  circleci orb publish "$pack_dir/orb.yml" "$orb_name@$target"
}

main() {
  mode=${1:-}
  require_token
  case $mode in
    dev)
      [ -n "${CIRCLE_SHA1:-}" ] || die "CIRCLE_SHA1 is not set"
      pack_orb
      publish_to "dev:${CIRCLE_SHA1}"
      ;;
    production)
      tag=${CIRCLE_TAG:-}
      [ -n "$tag" ] || die "CIRCLE_TAG is not set"
      printf '%s\n' "$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' ||
        die "CIRCLE_TAG must be vX.Y.Z, not $tag"
      ver=${tag#v}
      cp "$install_sh" "$stamp"
      sed "s/^default_version=@SMARTER_VERSION@/default_version=$tag/" "$install_sh" > "$install_sh.tmp" &&
        mv "$install_sh.tmp" "$install_sh"
      pack_orb
      publish_to "$ver"
      restore_install
      trap - EXIT
      ;;
    *)
      die "usage: scripts/publish-orb.sh dev|production"
      ;;
  esac
}

main "$@"