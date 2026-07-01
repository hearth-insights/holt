#!/usr/bin/env bash
set -euo pipefail

# Holt Release Verification Script
#
# Usage:
#   ./verify.sh [VERSION]
#   ./verify.sh v1.2.3
#   ./verify.sh latest        (resolves to the most recent stable release)
#
# Run from the directory containing the files you want to verify.
# Only files present in the current directory are checked; others are skipped.
# All verification material is fetched exclusively from the public Hearth Insights
# transparency log at https://github.com/hearth-insights/holt — never from the
# bundle itself, which could be tampered with in transit.
#
# Environment:
#   TRANSPARENCY_BASE  Override the transparency log base URL (for testing)

VERSION="${1:-latest}"
TRANSPARENCY_BASE="${TRANSPARENCY_BASE:-https://raw.githubusercontent.com/hearth-insights/holt/main}"

# ---- helpers ----

die() {
  echo "ERROR: $*" >&2
  exit 1
}

sha256_of() {
  if command -v sha256sum &>/dev/null; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum &>/dev/null; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    die "No SHA256 tool found. Install sha256sum (Linux) or shasum (macOS)."
  fi
}

# Check files present in the current directory against a checksums file.
# Entries in the checksums file whose filename is not present are silently skipped.
# Outputs per-file results; sets PASSED, FAILED, SKIPPED globals.
check_files_against_checksums() {
  local checksums_file="$1"
  PASSED=0; FAILED=0; SKIPPED=0

  while IFS= read -r line; do
    # Skip blank lines and comments
    [[ -z "${line// }" ]] && continue
    [[ "$line" == \#* ]] && continue

    local expected_hash filename
    expected_hash="${line%%  *}"
    filename="${line#*  }"
    filename="${filename#./}"   # normalise: strip any leading ./

    if [[ ! -f "$filename" ]]; then
      (( SKIPPED++ )) || true
      continue
    fi

    local actual_hash
    actual_hash=$(sha256_of "$filename")

    if [[ "$actual_hash" == "$expected_hash" ]]; then
      printf "  ✓ %s\n" "$filename"
      (( PASSED++ )) || true
    else
      printf "  ✗ %s  (hash mismatch)\n" "$filename"
      printf "      expected: %s\n" "$expected_hash"
      printf "      actual:   %s\n" "$actual_hash"
      (( FAILED++ )) || true
    fi
  done < "$checksums_file"
}

# ---- main ----

printf "================================================\n"
printf "Holt Release Verification\n"
printf "Version: %s\n" "$VERSION"
printf "================================================\n\n"

TMPDIR_VERIFY=$(mktemp -d)
trap 'rm -rf "$TMPDIR_VERIFY"' EXIT

# Step 1: Fetch trust material from the transparency log
printf "[1/3] Fetching verification artifacts from transparency log...\n"
printf "      %s\n\n" "$TRANSPARENCY_BASE"

curl -fsSL "${TRANSPARENCY_BASE}/verify/cosign.pub" \
     -o "${TMPDIR_VERIFY}/cosign.pub"

curl -fsSL "${TRANSPARENCY_BASE}/releases/${VERSION}/checksums.txt" \
     -o "${TMPDIR_VERIFY}/checksums.txt"

curl -fsSL "${TRANSPARENCY_BASE}/releases/${VERSION}/checksums.txt.sig" \
     -o "${TMPDIR_VERIFY}/checksums.txt.sig"

# Fetch metadata for version resolution (best-effort; not required)
curl -fsSL "${TRANSPARENCY_BASE}/releases/${VERSION}/release-metadata.json" \
     -o "${TMPDIR_VERIFY}/release-metadata.json" 2>/dev/null || true

# Print resolved version (latest → v1.2.3)
if [[ -f "${TMPDIR_VERIFY}/release-metadata.json" ]]; then
  RESOLVED_VERSION=$(grep -o '"version"[[:space:]]*:[[:space:]]*"[^"]*"' \
    "${TMPDIR_VERIFY}/release-metadata.json" 2>/dev/null \
    | grep -o '"[^"]*"$' | tr -d '"') || true
  if [[ -n "${RESOLVED_VERSION:-}" ]]; then
    printf "Resolved version: %s\n\n" "$RESOLVED_VERSION"
  fi
fi

# Step 2: Verify the checksums file signature
printf "[2/3] Verifying checksums file signature...\n"

if command -v cosign &>/dev/null; then
  cosign verify-blob \
    --key "${TMPDIR_VERIFY}/cosign.pub" \
    --signature "${TMPDIR_VERIFY}/checksums.txt.sig" \
    "${TMPDIR_VERIFY}/checksums.txt"
  printf "✓ Checksums cryptographically verified (cosign)\n"
else
  printf "WARNING: cosign not installed — signature check skipped.\n"
  printf "         Install from https://docs.sigstore.dev/cosign/installation/\n"
  printf "         Continuing with SHA256 integrity check only.\n"
fi
printf "\n"

# Step 3: Verify files present in the working directory
printf "[3/3] Checking files in: %s\n\n" "$(pwd)"

set +e
check_files_against_checksums "${TMPDIR_VERIFY}/checksums.txt"
set -e

TOTAL=$(grep -c '[^[:space:]]' "${TMPDIR_VERIFY}/checksums.txt" || true)
printf "\nSummary: %d verified, %d failed, %d not present (skipped) of %d total release artifacts\n\n" \
  "$PASSED" "$FAILED" "$SKIPPED" "$TOTAL"

if [[ $FAILED -gt 0 ]]; then
  printf "================================================\n"
  printf "VERIFICATION FAILED — do not use these files.\n"
  printf "Report suspicious artifacts to: security@hearth-insights.com\n"
  printf "Transparency log: https://github.com/hearth-insights/holt/tree/main/releases/%s\n" "$VERSION"
  printf "================================================\n"
  exit 1
fi

if [[ $PASSED -eq 0 ]]; then
  printf "WARNING: No release artifacts found in the current directory.\n"
  printf "         Run this script from inside an extracted Holt release bundle.\n"
  exit 1
fi

printf "================================================\n"
printf "✓ ALL PRESENT FILES VERIFIED\n"
printf "================================================\n\n"
printf "Transparency log: https://github.com/hearth-insights/holt/tree/main/releases/%s\n" "$VERSION"
