#!/bin/sh
# Generates a software bill of materials from the locked module graph.
# go.sum pins every dependency hash; this emits the resolved list as JSON.
# For a CycloneDX SBOM in a signing pipeline, run (offline mirror recommended):
#   go run github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@latest app -json -output sbom.cdx.json ./cmd/ura-agent
set -e
cd "$(dirname "$0")/.."
{
  echo '{'
  echo '  "product": "ura",'
  echo "  \"generated\": \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\","
  echo "  \"go\": \"$(go version | cut -d' ' -f3)\","
  echo '  "modules": ['
  go list -m -json all | grep -E '"(Path|Version)"' \
    | paste - - | sed 's/\t/ /' \
    | awk -F'"' '{printf "    {\"path\": \"%s\", \"version\": \"%s\"},\n", $4, $8}' \
    | sed '$ s/,$//'
  echo '  ]'
  echo '}'
} > sbom.json
echo "sbom.json written ($(grep -c '"path"' sbom.json) modules)"
