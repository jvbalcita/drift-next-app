#!/usr/bin/env bash

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
fixture_root=$(mktemp -d "${TMPDIR:-/tmp}/drift-next-generated-fixture.XXXXXX")
trap 'rm -rf "$fixture_root"' EXIT

mkdir -p "$fixture_root/bin" "$fixture_root/scripts" "$fixture_root/gen/go" "$fixture_root/apps/console/src/gen" "$fixture_root/proto"
cp "$script_dir/check-generated.sh" "$fixture_root/scripts/check-generated.sh"
chmod +x "$fixture_root/scripts/check-generated.sh"
printf '%s\n' 'version: v2' 'modules:' '  - path: proto' > "$fixture_root/buf.yaml"
printf '%s\n' 'version: v2' 'plugins:' '  - local: fake' '    out: gen/go' '  - local: fake' '    out: apps/console/src/gen' > "$fixture_root/buf.gen.yaml"
printf '%s\n' 'syntax = "proto3";' > "$fixture_root/proto/fixture.proto"
printf '%s\n' 'stale generated artifact' > "$fixture_root/gen/go/fixture.txt"
printf '%s\n' 'obsolete generated artifact' > "$fixture_root/gen/go/obsolete.txt"
printf '%s\n' 'unrelated file must remain unchanged' > "$fixture_root/unrelated.txt"

# The fake generator follows the same output paths that Buf receives from the
# check script, allowing this fixture to exercise comparison without network or
# a plugin install.
printf '%s\n' \
    '#!/usr/bin/env bash' \
    'set -euo pipefail' \
    'template=""' \
    'previous=""' \
    'for argument in "$@"; do' \
    '  if [ "$previous" = "--template" ]; then template="$argument"; fi' \
    '  previous="$argument"' \
    'done' \
    'while IFS= read -r line; do' \
    '  case "$line" in' \
    '    *"out: "*)' \
    '      output=${line#*out: }' \
    '      output=${output#\"}' \
    '      output=${output%\"}' \
    '      mkdir -p "$output"' \
    '      printf "%s\\n" "fresh generated artifact" > "$output/fixture.txt"' \
    '      ;;' \
    '  esac' \
    'done < "$template"' > "$fixture_root/bin/buf"
chmod +x "$fixture_root/bin/buf"

before=$(shasum -a 256 "$fixture_root/unrelated.txt")
if PATH="$fixture_root/bin:$PATH" bash "$fixture_root/scripts/check-generated.sh" > "$fixture_root/output.txt" 2>&1; then
    printf '%s\n' 'check-generated unexpectedly accepted a stale fixture' >&2
    cat "$fixture_root/output.txt" >&2
    exit 1
fi
if ! grep -q 'generated artifacts are stale' "$fixture_root/output.txt"; then
    cat "$fixture_root/output.txt" >&2
    exit 1
fi
after=$(shasum -a 256 "$fixture_root/unrelated.txt")
if [ "$before" != "$after" ]; then
    printf '%s\n' 'check-generated modified an unrelated file' >&2
    exit 1
fi
printf '%s\n' 'check-generated stale-artifact fixture passed'
