#!/bin/sh
# install.sh: the install command of the Smarter Workflows orb
# (orb/src/commands/install.yml). It puts circleci-smarter at
# /tmp/smarter/bin/circleci-smarter, in two steps that the command runs with
# SMARTER_INSTALL_STEP set (the cache restore sits between them):
#
#   pick  writes .smarter-version: the planner version and a newline. Its
#         checksum is part of the cache key of the binary, and the generated
#         a later plan run can restore the same binary from cache.
#   get   installs the binary: for source build, go build of this checkout; for
#         source release, the binary of a GitHub release, checked as below.
#
# Source release trusts nothing it downloads until it is checked:
#   1. SHA256SUMS and its Sigstore bundle come from the release. cosign
#      (pinned below, with the SHA-256 of its download) checks the bundle: the
#      signing certificate must name the identity of the release pipeline of
#      this project and the CircleCI OIDC issuer, and must be for refs/heads/main
#      or for the tag of the release (the certificate extension source repository
#      ref, which cosign has no flag for: signed_for reads it).
#   2. The binary must match its line of SHA256SUMS.
# verify-signature false skips 1 and says so: then only a damaged download is
# caught, not a replaced release.
#
# Environment: SMARTER_INSTALL_STEP (pick or get), SMARTER_ARG_SOURCE (release or
# build), SMARTER_ARG_VERSION (a release tag, or empty for the release this orb
# was published with), SMARTER_ARG_VERIFY (true or false), CIRCLE_SHA1. The
# script is POSIX sh, and never prints an environment variable. With no
# SMARTER_INSTALL_STEP it only defines its functions (the tests and the release
# script source it).
#
# CircleCI reads the double angle brackets and the double braces in an orb's
# commands as its own syntax, so none of them may appear in this file.
set -eu

repo=CircleCI-Labs/smarter-workflows
bin_dir=/tmp/smarter/bin

# The release this orb installs when no version is given. The publish script
# (scripts/publish-orb.sh) replaces the next line with the tag that the orb is
# published for. An orb that was not published from a release keeps it as it is.
default_version=@SMARTER_VERSION@

# The identity of the pipeline that signs a release: the pipeline definition of
# this project that runs for tags, and the root issuer of CircleCI's OIDC tokens.
# scripts/release.sh stops a release if the pipeline that signs is not this one.
signer_identity=https://circleci.com/api/v2/projects/4ac72be5-a688-4ffe-89b3-cd7f08d9d9e0/pipeline-definitions/12eb0a5c-4f1c-4ebe-a1e5-d8dda5c13c53
signer_issuer=https://oidc.circleci.com

# cosign, pinned: the version and the SHA-256 of each download (the digests that
# GitHub shows for the assets of the release, which match cosign_checksums.txt).
cosign_version=3.1.3
cosign_sha256() {
  case $1 in
    linux-amd64) echo 4629c757b7618056f8ddd7e2625ae9fdd94c0372a65049520bc7d9df9efc7f71 ;;
    linux-arm64) echo c5d324e091826b0d7a78eb16fef316450b4eb9aaec045611c08ba06f5e73220a ;;
    darwin-amd64) echo 2347488e5d5b25336644024dfeca5601b190e91197a71a917bda44744aff106c ;;
    darwin-arm64) echo 5cf948c2f4dfe59687bdd0b8523709067383e03982cc543475c8a7dc70e92a76 ;;
    *) return 1 ;;
  esac
}

say() { printf 'smarter: %s\n' "$*"; }
warn() { printf 'smarter: warning: %s\n' "$*" >&2; }
die() {
  printf 'smarter: %s\n' "$*" >&2
  exit 1
}

# fetch URL FILE: download over HTTPS, trying again on a failure.
fetch() {
  curl -fsSL --proto '=https' --tlsv1.2 --retry 5 --retry-all-errors --connect-timeout 20 \
    -o "$2" "$1" || die "cannot download $1"
}

# sha256_of FILE: the SHA-256 of a file, in hex.
sha256_of() {
  if command -v sha256sum > /dev/null 2>&1; then
    sha256sum "$1" | cut -d ' ' -f 1
  else
    shasum -a 256 "$1" | cut -d ' ' -f 1
  fi
}

# detect_platform: sets os and arch to the names that releases use.
detect_platform() {
  case $(uname -s) in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *) die "unsupported system $(uname -s): the planner runs on Linux and macOS jobs" ;;
  esac
  case $(uname -m) in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) die "unsupported architecture $(uname -m)" ;;
  esac
}

# --- the certificate of a signature -------------------------------------------
#
# A keyless signature names who signed in the SAN of its certificate, and the
# ref that the signing job ran for in the extension source repository ref (OID
# 1.3.6.1.4.1.57264.1.14, which Fulcio fills from the vcs-ref claim of the
# CircleCI token). The identity of a project is the same for every branch and
# tag, and a job of any branch can get a token, so the ref is what tells a
# release from a signature made by a job of a feature branch. cosign has flags
# for the GitHub extensions only, so the certificate is read here, with jq,
# openssl and od. The same check, by hand, is in docs/spec/v0.1.md.

# der_len N: the DER length octets of a string of N bytes, as hex digits.
der_len() {
  if [ "$1" -lt 128 ]; then
    printf '%02x' "$1"
  elif [ "$1" -lt 256 ]; then
    printf '81%02x' "$1"
  else
    printf '82%04x' "$1"
  fi
}

# ref_pattern REF: an extended regular expression for the hex dump of a
# certificate that holds the extension with exactly the value REF: the OID, an
# OCTET STRING of the exact length, a UTF8String (0c) or a PrintableString (13)
# of the exact length, then REF. The lengths make it an equality, not a prefix
# or a suffix.
ref_pattern() {
  rp_n=$(printf '%s' "$1" | LC_ALL=C wc -c | tr -d ' ')
  rp_len=$(der_len "$rp_n")
  printf '2b0601040183bf30010e04%s(0c|13)%s%s' \
    "$(der_len $((1 + ${#rp_len} / 2 + rp_n)))" "$rp_len" \
    "$(printf '%s' "$1" | od -An -v -tx1 | tr -d ' \n')"
}

# signed_for BUNDLE REF...: succeeds when the Sigstore bundle holds a signing
# certificate whose source repository ref is one of the REFs. Run cosign
# verify-blob first: this checks the ref, not the signature.
signed_for() {
  sf_bundle=$1
  shift
  sf_pattern=
  for sf_ref in "$@"; do
    sf_pattern=${sf_pattern:+$sf_pattern|}$(ref_pattern "$sf_ref")
  done
  [ -n "$sf_pattern" ] || return 1
  sf_cert=$(jq -r '.verificationMaterial | ((.certificate // .x509CertificateChain.certificates[0]) // {}) | .rawBytes // empty' "$sf_bundle" 2> /dev/null) || return 1
  [ -n "$sf_cert" ] || return 1
  printf '%s' "$sf_cert" | openssl base64 -d -A 2> /dev/null | od -An -v -tx1 | tr -d ' \n' |
    grep -Eq "^([0-9a-f]{2})*($sf_pattern)"
}

# install_cosign DIR: download the pinned cosign into DIR.
install_cosign() {
  detect_platform
  ic_want=$(cosign_sha256 "$os-$arch") || die "no pinned cosign for $os/$arch"
  mkdir -p "$1"
  fetch "https://github.com/sigstore/cosign/releases/download/v$cosign_version/cosign-$os-$arch" "$1/cosign"
  [ "$(sha256_of "$1/cosign")" = "$ic_want" ] || die "the cosign that was downloaded does not match its pinned SHA-256"
  chmod 0755 "$1/cosign"
}

# verify_signature COSIGN SUMS BUNDLE VERSION: SUMS was signed by the release
# pipeline of this project, for main or for the tag VERSION.
verify_signature() {
  for vs_tool in jq openssl od; do
    command -v "$vs_tool" > /dev/null 2>&1 || die "$vs_tool is needed to check the signature (or set verify-signature to false)"
  done
  vs_err=$(mktemp "${TMPDIR:-/tmp}/smarter-cosign.XXXXXX")
  if ! "$1" verify-blob --bundle "$3" --certificate-identity "$signer_identity" \
    --certificate-oidc-issuer "$signer_issuer" "$2" > /dev/null 2> "$vs_err"; then
    cat "$vs_err" >&2
    rm -f "$vs_err"
    die "the signature of SHA256SUMS does not verify: this is not a release of this project"
  fi
  rm -f "$vs_err"
  signed_for "$3" refs/heads/main "refs/tags/$4" ||
    die "the signing certificate names neither refs/heads/main nor refs/tags/$4: this release was not signed for $4"
  say "SHA256SUMS is signed by the release pipeline of $repo for $4"
}

# --- the steps ----------------------------------------------------------------

# normalize_version V: V with a leading v, which release tags have.
normalize_version() {
  case $1 in
    v*) printf '%s\n' "$1" ;;
    *) printf 'v%s\n' "$1" ;;
  esac
}

# pick_version: write .smarter-version.
pick_version() {
  mkdir -p "$bin_dir"
  case ${SMARTER_ARG_SOURCE:-release} in
    build)
      pv_version=dev-${CIRCLE_SHA1:?CIRCLE_SHA1 is not set}
      ;;
    release)
      pv_version=${SMARTER_ARG_VERSION:-}
      [ -n "$pv_version" ] || pv_version=$default_version
      case $pv_version in
        @*) die "this orb was not published with a release version: set version to a release tag, or use source build" ;;
      esac
      pv_version=$(normalize_version "$pv_version")
      printf '%s\n' "$pv_version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' ||
        die "version must be a release tag such as v0.2.0, not $pv_version"
      ;;
    *) die "source must be release or build, not ${SMARTER_ARG_SOURCE}" ;;
  esac
  printf '%s\n' "$pv_version" > .smarter-version
  say "planner version $pv_version"
}

# build_binary: go build of the current checkout, unless the cache has it.
build_binary() {
  if [ -x "$bin_dir/circleci-smarter" ]; then
    say "using the cached binary"
    return 0
  fi
  command -v go > /dev/null 2>&1 || die "source build needs Go; run this job on the go executor"
  CGO_ENABLED=0 go build -ldflags "-X main.version=dev-${CIRCLE_SHA1:?CIRCLE_SHA1 is not set}" \
    -o "$bin_dir/circleci-smarter" ./cmd/circleci-smarter
}

# download_binary: the binary of the release in .smarter-version, checked.
download_binary() {
  db_version=$(cat .smarter-version)
  detect_platform
  db_asset=circleci-smarter_${db_version}_${os}_${arch}
  db_base=https://github.com/$repo/releases/download/$db_version
  db_tmp=$(mktemp -d "${TMPDIR:-/tmp}/smarter-install.XXXXXX")
  trap 'rm -rf "$db_tmp"' EXIT

  fetch "$db_base/SHA256SUMS" "$db_tmp/SHA256SUMS"
  if [ "${SMARTER_ARG_VERIFY:-true}" = true ]; then
    fetch "$db_base/SHA256SUMS.sigstore.json" "$db_tmp/SHA256SUMS.sigstore.json"
    install_cosign "$db_tmp/tools"
    verify_signature "$db_tmp/tools/cosign" "$db_tmp/SHA256SUMS" "$db_tmp/SHA256SUMS.sigstore.json" "$db_version"
  else
    warn "the signature of SHA256SUMS is not checked (verify-signature is false): only a damaged download is caught, not a replaced release"
  fi

  db_lines=$(awk -v name="$db_asset" '{ n = $2; sub(/^\*/, "", n); if (n == name) print $1 }' "$db_tmp/SHA256SUMS")
  case $db_lines in
    '') die "SHA256SUMS of $db_version has no line for $db_asset" ;;
    *"
"*) die "SHA256SUMS of $db_version has more than one line for $db_asset" ;;
  esac
  printf '%s\n' "$db_lines" | grep -Eq '^[0-9a-f]{64}$' || die "SHA256SUMS of $db_version has a malformed line for $db_asset"

  fetch "$db_base/$db_asset" "$db_tmp/$db_asset"
  [ "$(sha256_of "$db_tmp/$db_asset")" = "$db_lines" ] ||
    die "$db_asset does not match its SHA-256 in SHA256SUMS: the download is not the release"

  rm -f "$bin_dir/circleci-smarter"
  mkdir -p "$bin_dir"
  cp "$db_tmp/$db_asset" "$bin_dir/circleci-smarter"
  chmod 0755 "$bin_dir/circleci-smarter"
  db_got=$("$bin_dir/circleci-smarter" version) || die "the downloaded binary does not run on this system"
  [ "$db_got" = "$db_version" ] || die "the binary says it is version $db_got, not $db_version"
  say "installed circleci-smarter $db_version for $os/$arch"
}

get_binary() {
  case ${SMARTER_ARG_SOURCE:-release} in
    build) build_binary ;;
    release) download_binary ;;
    *) die "source must be release or build, not ${SMARTER_ARG_SOURCE}" ;;
  esac
}

case ${SMARTER_INSTALL_STEP:-} in
  pick) pick_version ;;
  get) get_binary ;;
  '') ;;
  *) die "unknown install step ${SMARTER_INSTALL_STEP}" ;;
esac
