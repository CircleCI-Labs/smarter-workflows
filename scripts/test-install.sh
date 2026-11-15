#!/bin/sh
# test-install.sh: tests for orb/src/scripts/install.sh, the install command of
# the orb. It sources the script and calls its functions, with shims for curl,
# cosign and uname, so it needs no network and no signing key. A table of
# certificates that openssl makes with the extension source repository ref
# (OID 1.3.6.1.4.1.57264.1.14) tests signed_for, the check that a signature was
# made for main or for the tag of a release.
#
#   scripts/test-install.sh
#   SMARTER_TEST_TMP=DIR   where the temp files go (default: $TMPDIR, then /tmp)
#
# Needs openssl, jq, od, awk and sha256sum (or shasum). Exit status: 0 when
# nothing failed, 1 otherwise.
set -u

pass=0
fail=0

ok() {
  pass=$((pass + 1))
  printf 'ok - %s\n' "$1"
}
not_ok() {
  fail=$((fail + 1))
  printf 'not ok - %s\n' "$1"
}
# assert NAME COMMAND...: ok when COMMAND succeeds.
assert() {
  assert_name=$1
  shift
  if "$@"; then ok "$assert_name"; else not_ok "$assert_name"; fi
}

here=$(cd "$(dirname "$0")" && pwd) || exit 2
root=$(dirname "$here")
install_sh=$root/orb/src/scripts/install.sh
if [ ! -f "$install_sh" ]; then
  echo "missing $install_sh" >&2
  exit 2
fi
for tool in openssl jq od awk; do
  if ! command -v "$tool" > /dev/null 2>&1; then
    echo "test-install: $tool is needed" >&2
    exit 2
  fi
done

base=${SMARTER_TEST_TMP:-${TMPDIR:-/tmp}}
work=$(mktemp -d "$base/smarter-install-test.XXXXXX") || exit 2
trap 'rm -rf "$work"' EXIT
trap 'exit 1' HUP INT TERM

# A clean slate: no user git config or CI variables. The canary is the kind of
# value that must never be printed: the script must not echo the environment.
HOME=$work/home
mkdir -p "$HOME"
export HOME
unset SMARTER_INSTALL_STEP SMARTER_ARG_SOURCE SMARTER_ARG_VERSION SMARTER_ARG_VERIFY
CIRCLE_SHA1=0123456789abcdef0123456789abcdef01234567
SMARTER_TEST_CANARY=canary-value-that-must-not-be-printed
export CIRCLE_SHA1 SMARTER_TEST_CANARY

# --- the script under test ------------------------------------------------------

# shellcheck source=/dev/null
. "$install_sh"
set +e
bin_dir=$work/bin

sum256() {
  if command -v sha256sum > /dev/null 2>&1; then sha256sum "$1" | cut -d ' ' -f 1; else shasum -a 256 "$1" | cut -d ' ' -f 1; fi
}

# --- the certificate table -------------------------------------------------------

openssl ecparam -name prime256v1 -genkey -noout -out "$work/key.pem" 2> /dev/null
# cert NAME VALUE: a self-signed certificate, as the file $work/NAME.der. With no
# VALUE it has no source repository ref extension.
cert() {
  cert_name=$1
  shift
  if [ $# -gt 0 ]; then
    openssl req -x509 -new -key "$work/key.pem" -days 1 -subj "/CN=$cert_name" \
      -addext "1.3.6.1.4.1.57264.1.14=$1" -outform DER -out "$work/$cert_name.der" 2> /dev/null
  else
    openssl req -x509 -new -key "$work/key.pem" -days 1 -subj "/CN=$cert_name" \
      -outform DER -out "$work/$cert_name.der" 2> /dev/null
  fi
}
# bundle NAME: the Sigstore bundle of cosign 3 (a single certificate) for the
# certificate NAME, as $work/NAME.json. chain NAME: the older shape.
bundle() {
  jq -nc --arg c "$(openssl base64 -A < "$work/$1.der")" \
    '{mediaType: "application/vnd.dev.sigstore.bundle.v0.3+json", verificationMaterial: {certificate: {rawBytes: $c}}}' > "$work/$1.json"
}
chain() {
  jq -nc --arg c "$(openssl base64 -A < "$work/$1.der")" \
    '{mediaType: "application/vnd.dev.sigstore.bundle+json;version=0.2", verificationMaterial: {x509CertificateChain: {certificates: [{rawBytes: $c}]}}}' > "$work/$1-chain.json"
}

long_ref=refs/heads/$(printf 'x%.0s' $(seq 1 130))
cert main_utf8 ASN1:UTF8String:refs/heads/main
cert main_printable ASN1:PRINTABLESTRING:refs/heads/main
cert feature ASN1:UTF8String:refs/heads/feature/x
cert longer ASN1:UTF8String:refs/heads/main2
cert prefixed ASN1:UTF8String:refs/heads/x/refs/heads/main
cert tag ASN1:UTF8String:refs/tags/v0.2.0
cert long "ASN1:UTF8String:$long_ref"
cert none
# a feature branch whose certificate also holds the bytes of the main pattern
# half a byte off, in an extension of its own: the hex dump contains the pattern
# at an odd position
shifted_hex=a$(printf '2b0601040183bf30010e04110c0f%s' "$(printf refs/heads/main | od -An -v -tx1 | tr -d ' \n')")a
openssl req -x509 -new -key "$work/key.pem" -days 1 -subj "/CN=shifted" \
  -addext "1.3.6.1.4.1.57264.1.14=ASN1:UTF8String:refs/heads/feature/x" \
  -addext "1.2.3.4=DER:$shifted_hex" -outform DER -out "$work/shifted.der" 2> /dev/null
for c in main_utf8 main_printable feature longer prefixed tag long none shifted; do bundle "$c"; done
chain main_utf8

# expect NAME WANT BUNDLE REF...: signed_for passes or fails.
expect() {
  expect_name=$1
  expect_want=$2
  expect_bundle=$3
  shift 3
  expect_got=fail
  if signed_for "$expect_bundle" "$@" 2> /dev/null; then expect_got=pass; fi
  if [ "$expect_got" = "$expect_want" ]; then ok "signed_for: $expect_name"; else not_ok "signed_for: $expect_name ($expect_got, want $expect_want)"; fi
}

expect "main, UTF8String" pass "$work/main_utf8.json" refs/heads/main
expect "main, PrintableString" pass "$work/main_printable.json" refs/heads/main
expect "main, the chain shape of a bundle" pass "$work/main_utf8-chain.json" refs/heads/main
expect "a feature branch, main wanted" fail "$work/feature.json" refs/heads/main
expect "a feature branch, itself wanted" pass "$work/feature.json" refs/heads/feature/x
expect "main2 is not main" fail "$work/longer.json" refs/heads/main
expect "main is not main2" fail "$work/main_utf8.json" refs/heads/main2
expect "a ref that ends with main" fail "$work/prefixed.json" refs/heads/main
expect "the tag that was asked for" pass "$work/tag.json" refs/tags/v0.2.0
expect "another tag" fail "$work/tag.json" refs/tags/v0.2.1
expect "a tag that is a prefix of the signed one" fail "$work/tag.json" refs/tags/v0.2
expect "a tag, when main is wanted" fail "$work/tag.json" refs/heads/main
expect "main or the tag, signed for the tag" pass "$work/tag.json" refs/heads/main refs/tags/v0.2.0
expect "main or the tag, signed for main" pass "$work/main_utf8.json" refs/heads/main refs/tags/v0.2.0
expect "main or the tag, signed for a feature branch" fail "$work/feature.json" refs/heads/main refs/tags/v0.2.0
expect "a ref of 130+ characters (two length octets)" pass "$work/long.json" "$long_ref"
expect "a ref of 130+ characters is not main" fail "$work/long.json" refs/heads/main
expect "the main pattern half a byte off" fail "$work/shifted.json" refs/heads/main
expect "no extension" fail "$work/none.json" refs/heads/main
expect "no ref asked for" fail "$work/main_utf8.json"
echo 'not json' > "$work/notjson.json"
expect "a bundle that is not JSON" fail "$work/notjson.json" refs/heads/main
echo '{"verificationMaterial":{"publicKey":{"hint":"x"}}}' > "$work/key-bundle.json"
expect "a bundle with a key and no certificate" fail "$work/key-bundle.json" refs/heads/main
jq -nc '{verificationMaterial: {certificate: {rawBytes: "bm90IGEgY2VydGlmaWNhdGU="}}}' > "$work/not-der.json"
expect "a certificate that is not DER" fail "$work/not-der.json" refs/heads/main
expect "no such bundle" fail "$work/absent.json" refs/heads/main
long_head=$(ref_pattern "$long_ref" | cut -c1-37)
if [ "$long_head" = '2b0601040183bf30010e048190(0c|13)818d' ]; then
  ok "ref_pattern: the long form of the DER length"
else
  not_ok "ref_pattern: the long form of the DER length ($long_head)"
fi

# --- shims -----------------------------------------------------------------------

shims=$work/shims
fix=$work/fixtures
mkdir -p "$shims" "$fix/release/v0.2.0" "$fix/release/v0.2.1"
export FAKE_FIXTURES="$fix"

# curl: copy the fixture of the URL to the file after -o. An asset that is not
# there is a 404, which curl -f turns into exit 22.
cat > "$shims/curl" << 'EOF'
#!/bin/sh
out=
url=
while [ $# -gt 0 ]; do
  case $1 in
    -o) out=$2; shift ;;
    --proto | --retry | --connect-timeout) shift ;;
    -*) ;;
    *) url=$1 ;;
  esac
  shift
done
printf '%s\n' "$url" >> "$FAKE_FIXTURES/urls"
case $url in
  https://github.com/CircleCI-Labs/smarter-workflows/releases/download/*) f=$FAKE_FIXTURES/release/${url#*/download/} ;;
  https://github.com/sigstore/cosign/releases/download/v3.1.3/cosign-*) f=$FAKE_FIXTURES/cosign ;;
  *) echo "curl shim: unexpected URL $url" >&2; exit 6 ;;
esac
[ -f "$f" ] || exit 22
cp "$f" "$out"
EOF
# cosign: log the arguments, and fail when FAKE_COSIGN_FAIL is set.
cat > "$fix/cosign" << 'EOF'
#!/bin/sh
printf '%s\n' "$*" >> "$FAKE_FIXTURES/cosign.log"
if [ -n "${FAKE_COSIGN_FAIL:-}" ]; then
  echo "Error: fake verification failure" >&2
  exit 1
fi
echo "Verified OK" >&2
EOF
# uname: FAKE_UNAME_S and FAKE_UNAME_M.
cat > "$shims/uname" << 'EOF'
#!/bin/sh
case $1 in
  -s) printf '%s\n' "${FAKE_UNAME_S:-Linux}" ;;
  -m) printf '%s\n' "${FAKE_UNAME_M:-x86_64}" ;;
  *) exec /usr/bin/uname "$@" ;;
esac
EOF
chmod +x "$shims/curl" "$shims/uname" "$fix/cosign"
PATH=$shims:$PATH
export PATH
fake_cosign_sha=$(sum256 "$fix/cosign")
# install.sh calls cosign_sha256: the pin is the SHA-256 of the fake.
# shellcheck disable=SC2329
cosign_sha256() { echo "$fake_cosign_sha"; }

# release_fixture VERSION: the assets of a release, a signature for main and
# SHA256SUMS over them. The binaries print their version.
release_fixture() {
  rf_dir=$fix/release/$1
  for rf_platform in linux_amd64 linux_arm64 darwin_amd64 darwin_arm64; do
    printf '#!/bin/sh\necho %s\n' "$1" > "$rf_dir/circleci-smarter_$1_$rf_platform"
  done
  printf '#!/bin/sh\necho %s\n' "$1" > "$rf_dir/circleci-smarter_$1_windows_amd64.exe"
  (cd "$rf_dir" && for rf_f in circleci-smarter_*; do printf '%s  %s\n' "$(sum256 "$rf_f")" "$rf_f"; done > SHA256SUMS)
  cp "$work/main_utf8.json" "$rf_dir/SHA256SUMS.sigstore.json"
}
release_fixture v0.2.0

# try NAME WANT VERSION [VERIFY]: run download_binary in a fresh job directory,
# for the release VERSION. WANT is ok, or text that the error message must have.
try() {
  try_name=$1
  try_want=$2
  SMARTER_ARG_SOURCE=release
  SMARTER_ARG_VERSION=$3
  SMARTER_ARG_VERIFY=${4:-true}
  export SMARTER_ARG_SOURCE SMARTER_ARG_VERSION SMARTER_ARG_VERIFY
  rm -rf "$work/job" "$bin_dir" "$fix/cosign.log"
  mkdir -p "$work/job"
  (
    cd "$work/job" || exit 99
    pick_version && download_binary
  ) > "$work/out" 2>&1
  try_rc=$?
  if [ "$try_want" = ok ]; then
    if [ "$try_rc" -eq 0 ]; then ok "$try_name"; else not_ok "$try_name (exit $try_rc: $(tail -n 3 "$work/out" | tr '\n' ' '))"; fi
  else
    if [ "$try_rc" -ne 0 ] && grep -qF "$try_want" "$work/out"; then
      ok "$try_name"
    else
      not_ok "$try_name (exit $try_rc, wanted a failure that says \"$try_want\": $(tail -n 3 "$work/out" | tr '\n' ' '))"
    fi
  fi
  if grep -qF "$SMARTER_TEST_CANARY" "$work/out"; then not_ok "$try_name: the output has a variable of the environment"; fi
}

# --- pick_version ----------------------------------------------------------------

# pick NAME WANT SOURCE VERSION: run pick_version and read .smarter-version.
pick() {
  pick_name=$1
  pick_want=$2
  SMARTER_ARG_SOURCE=$3
  SMARTER_ARG_VERSION=$4
  export SMARTER_ARG_SOURCE SMARTER_ARG_VERSION
  rm -rf "$work/job"
  mkdir -p "$work/job"
  pick_got=$(cd "$work/job" && pick_version > /dev/null 2> "$work/err" && cat .smarter-version)
  pick_rc=$?
  case $pick_want in
    fail:*)
      if [ "$pick_rc" -ne 0 ] && grep -qF "${pick_want#fail:}" "$work/err"; then ok "pick_version: $pick_name"; else not_ok "pick_version: $pick_name (exit $pick_rc: $(cat "$work/err"))"; fi
      ;;
    *)
      if [ "$pick_rc" -eq 0 ] && [ "$pick_got" = "$pick_want" ]; then ok "pick_version: $pick_name"; else not_ok "pick_version: $pick_name (got \"$pick_got\", exit $pick_rc)"; fi
      ;;
  esac
}

pick "build is the commit" "dev-$CIRCLE_SHA1" build ""
pick "build ignores the version" "dev-$CIRCLE_SHA1" build v9.9.9
pick "a release tag" v0.2.0 release v0.2.0
pick "a version without the v" v0.2.0 release 0.2.0
pick "no version, an orb that was not published with one" "fail:not published with a release version" release ""
(
  # shellcheck disable=SC2034
  default_version=v0.3.1
  pick "no version, the version of the orb" v0.3.1 release ""
)
pick "latest is not a version" "fail:must be a release tag" release latest
pick "a branch is not a version" "fail:must be a release tag" release main
pick "a pre-release is not a release tag" "fail:must be a release tag" release v0.2.0-rc1
pick "shell syntax is not a version" "fail:must be a release tag" release 'v0.2.0; echo hi'
pick "an unknown source" "fail:source must be release or build" nowhere ""

# --- download_binary -------------------------------------------------------------

try "a release: checked, installed" ok v0.2.0
assert "the installed binary runs" test "$("$bin_dir/circleci-smarter" version)" = v0.2.0
assert "the binary is executable" test -x "$bin_dir/circleci-smarter"
assert "the binary is the one of this platform" grep -q 'echo v0.2.0' "$bin_dir/circleci-smarter"
# shellcheck disable=SC2154
assert "cosign checks the identity of this project" grep -qF -- "--certificate-identity $signer_identity " "$fix/cosign.log"
assert "cosign checks the OIDC issuer of CircleCI" grep -qF -- "--certificate-oidc-issuer https://oidc.circleci.com " "$fix/cosign.log"
assert "cosign checks the bundle of the release" grep -q -- '--bundle .*/SHA256SUMS.sigstore.json ' "$fix/cosign.log"
for asset in SHA256SUMS SHA256SUMS.sigstore.json circleci-smarter_v0.2.0_linux_amd64; do
  assert "the download of $asset comes from the release of this project" \
    grep -qxF "https://github.com/CircleCI-Labs/smarter-workflows/releases/download/v0.2.0/$asset" "$fix/urls"
done
# shellcheck disable=SC2154
assert "cosign comes from its own release, pinned by version" \
  grep -qxF "https://github.com/sigstore/cosign/releases/download/v$cosign_version/cosign-linux-amd64" "$fix/urls"

try "verify-signature false: checked by SHA-256 only, and it says so" ok v0.2.0 false
assert "verify-signature false: cosign is not downloaded or run" test ! -e "$fix/cosign.log"
assert "verify-signature false: a warning" grep -q 'warning: the signature of SHA256SUMS is not checked' "$work/out"

try "a signature for main" ok v0.2.0
cp "$work/tag.json" "$fix/release/v0.2.0/SHA256SUMS.sigstore.json"
try "a signature for the tag of the release" ok v0.2.0
cp "$work/feature.json" "$fix/release/v0.2.0/SHA256SUMS.sigstore.json"
try "a signature made for a feature branch" "names neither refs/heads/main nor refs/tags/v0.2.0" v0.2.0
cp "$work/none.json" "$fix/release/v0.2.0/SHA256SUMS.sigstore.json"
try "a certificate with no source repository ref" "names neither refs/heads/main" v0.2.0
cp "$work/feature.json" "$fix/release/v0.2.0/SHA256SUMS.sigstore.json"
try "a signature made for a feature branch, verify-signature false" ok v0.2.0 false
release_fixture v0.2.0
cp "$work/tag.json" "$fix/release/v0.2.0/SHA256SUMS.sigstore.json"
release_fixture v0.2.1
cp "$work/tag.json" "$fix/release/v0.2.1/SHA256SUMS.sigstore.json"
try "the signature of v0.2.0 on the release v0.2.1" "names neither refs/heads/main nor refs/tags/v0.2.1" v0.2.1
release_fixture v0.2.0

FAKE_COSIGN_FAIL=1
export FAKE_COSIGN_FAIL
try "cosign refuses the signature" "the signature of SHA256SUMS does not verify" v0.2.0
assert "cosign's own message is shown" grep -q 'fake verification failure' "$work/out"
unset FAKE_COSIGN_FAIL

printf 'tampered\n' >> "$fix/release/v0.2.0/circleci-smarter_v0.2.0_linux_amd64"
try "a binary that is not the one in SHA256SUMS" "does not match its SHA-256 in SHA256SUMS" v0.2.0
assert "a binary that does not match is not installed" test ! -e "$bin_dir/circleci-smarter"
release_fixture v0.2.0

grep -v 'linux_amd64' "$fix/release/v0.2.0/SHA256SUMS" > "$work/sums" && cp "$work/sums" "$fix/release/v0.2.0/SHA256SUMS"
try "SHA256SUMS has no line for the binary" "has no line for circleci-smarter_v0.2.0_linux_amd64" v0.2.0
release_fixture v0.2.0

line=$(grep 'linux_amd64' "$fix/release/v0.2.0/SHA256SUMS")
printf '%s\n' "$line" >> "$fix/release/v0.2.0/SHA256SUMS"
try "SHA256SUMS has two lines for the binary" "more than one line" v0.2.0
release_fixture v0.2.0

sed 's/^[0-9a-f]*  circleci-smarter_v0.2.0_linux_amd64$/nothex  circleci-smarter_v0.2.0_linux_amd64/' "$fix/release/v0.2.0/SHA256SUMS" > "$work/sums" && cp "$work/sums" "$fix/release/v0.2.0/SHA256SUMS"
try "SHA256SUMS has a line that is not a hash" "malformed line" v0.2.0
release_fixture v0.2.0

sed 's/  circleci-smarter_v0.2.0_linux_amd64$/ *circleci-smarter_v0.2.0_linux_amd64/' "$fix/release/v0.2.0/SHA256SUMS" > "$work/sums" && cp "$work/sums" "$fix/release/v0.2.0/SHA256SUMS"
try "SHA256SUMS in binary mode (a star before the name)" ok v0.2.0
release_fixture v0.2.0

rm "$fix/release/v0.2.0/SHA256SUMS.sigstore.json"
try "a release without a bundle" "cannot download" v0.2.0
try "a release without a bundle, verify-signature false" ok v0.2.0 false
release_fixture v0.2.0
try "a release that does not exist" "cannot download" v0.9.9

printf '#!/bin/sh\necho v0.9.9\n' > "$fix/release/v0.2.0/circleci-smarter_v0.2.0_linux_amd64"
(cd "$fix/release/v0.2.0" && for f in circleci-smarter_*; do printf '%s  %s\n' "$(sum256 "$f")" "$f"; done > SHA256SUMS)
try "a binary that says it is another version" "says it is version v0.9.9, not v0.2.0" v0.2.0
release_fixture v0.2.0

# shellcheck disable=SC2329
cosign_sha256() { echo 0000000000000000000000000000000000000000000000000000000000000000; }
try "a cosign that is not the pinned one" "does not match its pinned SHA-256" v0.2.0
# shellcheck disable=SC2329
cosign_sha256() { echo "$fake_cosign_sha"; }

FAKE_UNAME_M=aarch64
export FAKE_UNAME_M
try "arm64: the arm64 binary" ok v0.2.0
assert "arm64: the arm64 asset was downloaded" grep -q 'circleci-smarter_v0.2.0_linux_arm64$' "$fix/urls"
FAKE_UNAME_S=Darwin
FAKE_UNAME_M=arm64
export FAKE_UNAME_S FAKE_UNAME_M
try "macOS on Apple silicon: the darwin arm64 binary" ok v0.2.0
assert "macOS: the darwin asset was downloaded" grep -q 'circleci-smarter_v0.2.0_darwin_arm64$' "$fix/urls"
FAKE_UNAME_M=riscv64
try "an architecture without a release" "unsupported architecture riscv64" v0.2.0
FAKE_UNAME_S=MINGW64_NT
FAKE_UNAME_M=x86_64
try "Windows has no install" "unsupported system MINGW64_NT" v0.2.0
unset FAKE_UNAME_S FAKE_UNAME_M

# --- build_binary ----------------------------------------------------------------

mkdir -p "$work/bin-build"
bin_dir=$work/bin-build
printf '#!/bin/sh\necho cached\n' > "$bin_dir/circleci-smarter"
chmod +x "$bin_dir/circleci-smarter"
SMARTER_ARG_SOURCE=build
export SMARTER_ARG_SOURCE
assert "source build uses the cached binary" sh -c ". '$install_sh'; bin_dir='$bin_dir'; build_binary 2>&1 | grep -q 'using the cached binary'"
rm "$bin_dir/circleci-smarter"
nogo=$work/nogo
mkdir -p "$nogo"
for t in sh dirname rm cat mkdir; do ln -sf "$(command -v $t)" "$nogo/$t"; done
out=$(PATH=$nogo sh -c ". '$install_sh'; bin_dir='$bin_dir'; build_binary" 2>&1)
case $out in
  *"source build needs Go"*) ok "source build without Go says so" ;;
  *) not_ok "source build without Go says so ($out)" ;;
esac

# --- the dispatcher --------------------------------------------------------------

out=$(SMARTER_INSTALL_STEP=bogus sh "$install_sh" 2>&1)
case $out in
  *"unknown install step bogus"*) ok "an unknown step is an error" ;;
  *) not_ok "an unknown step is an error ($out)" ;;
esac
assert "sourced without a step, it only defines functions" sh -c ". '$install_sh' && command -v signed_for > /dev/null"
assert "the script has none of the strings that CircleCI reads as its own syntax" sh -c "! grep -q -e '<<' -e '>>' -e '{{' -e '}}' '$install_sh'"

printf '\n%s passed, %s failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
