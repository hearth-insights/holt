#!/bin/bash
set -euo pipefail

VERSION="${1:-latest}"
TRANSPARENCY_BASE="https://raw.githubusercontent.com/hearth-insights/holt/main"
RELEASE_BASE="https://github.com/hearth-insights/holt-engine/releases/download"

echo "================================================"
echo "Holt Release Verification Script"
echo "Version: ${VERSION}"
echo "================================================"
echo ""

# Create temp directory
TEMP_DIR=$(mktemp -d)
trap "rm -rf ${TEMP_DIR}" EXIT

cd "${TEMP_DIR}"

# Download public key
echo "[1/5] Downloading public key..."
curl -sSfL "${TRANSPARENCY_BASE}/verify/cosign.pub" -o cosign.pub
echo "✓ Public key downloaded"
echo ""

# Download checksums and signature
echo "[2/5] Downloading checksums from transparency log..."
curl -sSfL "${TRANSPARENCY_BASE}/releases/${VERSION}/checksums.txt" -o checksums.txt
curl -sSfL "${TRANSPARENCY_BASE}/releases/${VERSION}/checksums.txt.sig" -o checksums.txt.sig
echo "✓ Checksums downloaded"
echo ""

# Verify checksums signature
echo "[3/5] Verifying checksums signature..."
if ! command -v cosign &> /dev/null; then
    echo "ERROR: cosign not found. Install from: https://docs.sigstore.dev/cosign/installation/"
    exit 1
fi

cosign verify-blob --key cosign.pub --signature checksums.txt.sig checksums.txt
echo "✓ Checksums signature VERIFIED"
echo ""

# Download a binary for demonstration (user can choose which)
echo "[4/5] Download verification example (holt-linux-amd64)..."
echo "You can verify any binary listed in checksums.txt"
curl -sSfL "${RELEASE_BASE}/${VERSION}/holt-linux-amd64" -o holt-linux-amd64
curl -sSfL "${RELEASE_BASE}/${VERSION}/holt-linux-amd64.sig" -o holt-linux-amd64.sig
echo "✓ Binary downloaded"
echo ""

# Verify binary signature
echo "[5/5] Verifying binary signature..."
cosign verify-blob --key cosign.pub --signature holt-linux-amd64.sig holt-linux-amd64
echo "✓ Binary signature VERIFIED"
echo ""

# Verify checksum
echo "Verifying checksum matches transparency log..."
EXPECTED_CHECKSUM=$(grep "holt-linux-amd64" checksums.txt | awk '{print $1}')
ACTUAL_CHECKSUM=$(sha256sum holt-linux-amd64 | awk '{print $1}')

if [ "${EXPECTED_CHECKSUM}" = "${ACTUAL_CHECKSUM}" ]; then
    echo "✓ Checksum VERIFIED"
else
    echo "✗ Checksum MISMATCH"
    echo "  Expected: ${EXPECTED_CHECKSUM}"
    echo "  Got:      ${ACTUAL_CHECKSUM}"
    exit 1
fi

echo ""
echo "================================================"
echo "✓ ALL VERIFICATIONS PASSED"
echo "================================================"
echo ""
echo "The Holt ${VERSION} release is cryptographically verified."
echo "You can now use the downloaded binary with confidence."
echo ""
echo "Transparency log: https://github.com/hearth-insights/holt/tree/main/releases/${VERSION}"
