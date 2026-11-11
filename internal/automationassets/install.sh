#!/bin/sh
# Install only the credential-free ProofBridge skill; leave CLI configuration intact.
set -eu
umask 077
fail() { printf '%s\n' "ProofBridge: $*" >&2; exit 1; }
usage() { printf '%s\n' 'Usage: sh install.sh --client codex|claude [--scope user|project] [--hub https://lab.example] [--asset-base https://mirror.example/skill]' 'Downloads the credential-free Skill from a fixed official GitHub version by default. --hub is accepted for compatibility and is never a download source.'; }
hub=''
client=''
scope='user'
asset_base='https://raw.githubusercontent.com/Atingaii/ProofBridge/proofbridge-skill-v1.1.0/internal/automationassets/skill'
while [ "$#" -gt 0 ]; do
  case "$1" in
    --hub|--client|--scope|--asset-base)
      [ "$#" -ge 2 ] || fail "Missing value for $1. Use --help."
      case "$1" in --hub) hub=$2 ;; --client) client=$2 ;; --scope) scope=$2 ;; --asset-base) asset_base=$2 ;; esac
      shift 2 ;;
    --help|-h) usage; exit 0 ;;
    *) fail "Unknown option: $1. Use --help." ;;
  esac
done
validate_address() {
  case "$1" in
    *'?'*|*'#'*|*'@'*|*' '*|*'	'*|*'
'*) fail "$2 must not contain credentials, whitespace, query or fragment." ;;
  esac
  case "$1" in
    https://?*) ;;
    http://127.0.0.1|http://127.0.0.1:*|http://127.0.0.1/*|http://localhost|http://localhost:*|http://localhost/*|http://\[::1\]|http://\[::1\]:*|http://\[::1\]/*) ;;
    *) fail "Use HTTPS for $2; HTTP is allowed only for a local test server." ;;
  esac
}
[ -z "$hub" ] || validate_address "$hub" 'the Hub address'
asset_base=${asset_base%/}
validate_address "$asset_base" 'the Skill source'
transfer_protocols='=https'
case "$asset_base" in http://*) transfer_protocols='=http,https' ;; esac
case "$client" in codex|claude) ;; *) fail 'Choose --client codex or --client claude.' ;; esac
case "$scope" in
  user)
    [ -n "${HOME:-}" ] || fail 'HOME is unavailable. Select --scope project or restore your user environment.'
    case "$client" in
      codex) skill_root=$HOME/.agents/skills ;;
      claude) skill_root="${CLAUDE_CONFIG_DIR:-$HOME/.claude}/skills" ;;
    esac ;;
  project)
    case "$client" in codex) skill_root=$PWD/.agents/skills ;; claude) skill_root=$PWD/.claude/skills ;; esac ;;
  *) fail 'Choose --scope user or --scope project.' ;;
esac
command -v curl >/dev/null 2>&1 || fail 'curl is required. Install curl and run this command again.'
if command -v sha256sum >/dev/null 2>&1; then
  checksum() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
  checksum() { shasum -a 256 "$1" | awk '{print $1}'; }
else
  fail 'A SHA-256 tool is required: sha256sum or shasum.'
fi
target=$skill_root/proofbridge
[ ! -L "$target" ] || fail 'The proofbridge skill directory is a symlink. Choose a regular managed directory.'
if [ -e "$target" ]; then
  [ -d "$target" ] || fail 'The proofbridge skill path is not a directory.'
  [ -f "$target/.proofbridge-managed" ] && [ ! -L "$target/.proofbridge-managed" ] ||
    fail 'An existing proofbridge skill is not managed by this installer. Back it up or choose another scope.'
  [ "$(cat "$target/.proofbridge-managed")" = 'proofbridge-skill-v1' ] ||
    fail 'The existing skill has an unknown installation marker. Preserve it and contact the maintainer.'
fi
for path in SKILL.md references references/tools.md; do
  [ ! -L "$target/$path" ] || fail "Refusing to replace a symlink: $target/$path"
done
mkdir -p "$skill_root"
staging=$(mktemp -d "$skill_root/.proofbridge-install.XXXXXXXX")
trap 'rm -rf "$staging"' EXIT HUP INT TERM
mkdir -p "$staging/references"
download() {
  curl --fail --silent --show-error --location --connect-timeout 15 --max-time 90 \
    --retry 2 --proto "$transfer_protocols" --proto-redir "$transfer_protocols" \
    "$asset_base/$1" --output "$staging/$1" ||
    fail "Could not download $1 from the Skill source. Check access to GitHub (or your explicit mirror), then retry. Do not disable certificate verification. The installed skill has not changed."
}
download checksums.txt
for file in SKILL.md references/tools.md; do
  download "$file"
  expected=$(awk -v file="$file" '$2 == file && length($1) == 64 {print $1}' "$staging/checksums.txt")
  [ -n "$expected" ] || fail "No SHA-256 entry for $file. The installed skill has not changed."
  case "$expected" in *[!0-9a-f]*) fail "Invalid SHA-256 entry for $file." ;; esac
  actual=$(checksum "$staging/$file")
  [ "$actual" = "$expected" ] || fail "SHA-256 mismatch for $file. The installed skill has not changed."
done
[ -s "$staging/SKILL.md" ] || fail 'The downloaded skill is empty.'
mkdir -p "$target/references"
# Stage on the target filesystem; publish the entrypoint after its references.
mv -f "$staging/references/tools.md" "$target/references/tools.md"
mv -f "$staging/SKILL.md" "$target/SKILL.md"
printf '%s\n' 'proofbridge-skill-v1' > "$staging/marker"
mv -f "$staging/marker" "$target/.proofbridge-managed"
printf '%s\n' "Installed ProofBridge skill for $client ($scope): $target" \
  'Your existing CLI authentication, models and MCP configuration were not changed.' \
  'Copy the MCP configuration from your personal center. If the skill is not listed, restart the CLI.'
