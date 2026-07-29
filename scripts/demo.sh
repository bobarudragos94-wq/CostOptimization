#!/bin/sh
# Full offline demo of the collector -> encrypted bundle -> analyzer -> report
# workflow using the synthetic fleet. No network access is used or needed.
set -e
cd "$(dirname "$0")/.."
OUT=demo-out
rm -rf "$OUT"
mkdir -p "$OUT"

echo "==> 1. Generating the offline keypair (analyzer machine step)"
./bin/ura-analyzer keygen --out "$OUT/keys"

echo ""
echo "==> 2. Generating a synthetic 6-host fleet as real encrypted bundles"
echo "       (agents would produce these on the monitored servers)"
./bin/ura-analyzer synth --recipient "@$OUT/keys/recipient.txt" --out "$OUT/bundles" --days 14

echo ""
echo "==> 3. Importing all bundles and producing the consolidated reports"
./bin/ura-analyzer analyze --key "$OUT/keys/identity.txt" --in "$OUT/bundles" --out "$OUT/reports"

echo ""
echo "==> Done. Open:"
echo "      $OUT/reports/report.html   (human-readable)"
echo "      $OUT/reports/report.md"
echo "      $OUT/reports/report.json   (machine-readable)"
