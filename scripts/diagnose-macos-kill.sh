#!/bin/bash
# Diagnose why macOS is killing the holt binary

echo "=== Holt Binary Diagnostic ==="
echo ""

echo "1. Checking binary locations:"
echo "   Local:     $(ls -lh ./bin/holt 2>&1 | awk '{print $5, $9}')"
echo "   Installed: $(ls -lh /usr/local/bin/holt 2>&1 | awk '{print $5, $9}')"
echo ""

echo "2. Checking file types:"
file ./bin/holt
file /usr/local/bin/holt
echo ""

echo "3. Checking extended attributes (quarantine flags):"
xattr ./bin/holt 2>/dev/null || echo "   Local: No extended attributes"
xattr /usr/local/bin/holt 2>/dev/null || echo "   Installed: No extended attributes"
echo ""

echo "4. Checking code signatures:"
codesign -dvv ./bin/holt 2>&1 | grep -E "Signature|Authority|TeamIdentifier" || echo "   Local: Not signed"
codesign -dvv /usr/local/bin/holt 2>&1 | grep -E "Signature|Authority|TeamIdentifier" || echo "   Installed: Not signed"
echo ""

echo "5. Testing binaries:"
echo -n "   Local binary:     "
./bin/holt --version >/dev/null 2>&1 && echo "✓ Works" || echo "✗ Killed"
echo -n "   Installed binary: "
/usr/local/bin/holt --version >/dev/null 2>&1 && echo "✓ Works" || echo "✗ Killed"
echo ""

echo "6. Checking system logs for crash reports:"
echo "   Last crash (if any):"
log show --predicate 'processImagePath contains "holt"' --last 1m --style compact 2>/dev/null | tail -5 || echo "   (No recent crashes or log access denied)"
echo ""

echo "=== Recommended fixes ==="
echo ""
echo "Try these in order:"
echo ""
echo "1. Remove quarantine attributes:"
echo "   sudo xattr -cr /usr/local/bin/holt"
echo ""
echo "2. Apply ad-hoc code signature:"
echo "   sudo codesign --force --deep --sign - /usr/local/bin/holt"
echo ""
echo "3. Use local binary instead:"
echo "   Add to ~/.zshrc or ~/.bashrc:"
echo "   export PATH=\"$(pwd)/bin:\$PATH\""
echo ""
