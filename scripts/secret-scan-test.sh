#!/usr/bin/env bash

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
fixture_root=$(mktemp -d "${TMPDIR:-/tmp}/drift-next-secret-fixture.XXXXXX")
trap 'rm -rf "$fixture_root"' EXIT

mkdir -p "$fixture_root/scripts" "$fixture_root/fixtures"
cp "$script_dir/secret-scan.sh" "$fixture_root/scripts/secret-scan.sh"
chmod +x "$fixture_root/scripts/secret-scan.sh"
git -c init.defaultBranch=main init -q "$fixture_root"

key_one_prefix=client
key_one_suffix=_secret
key_two_prefix=access
key_two_suffix=_token
positive_one=POSITIVE_CONTROL_VALUE_123456
positive_two=ANOTHER_POSITIVE_CONTROL_123456
escaped_quote=$(printf '\\\"')
escaped_backslash=$(printf '%s' '\\')
escaped_suffix=LONG_ESCAPED_SECRET_VALUE_123456
printf '{"%s%s":"%s","%s%s":"%s","%s":"%s%s%s%s"}\n' \
    "$key_one_prefix" "$key_one_suffix" "$positive_one" \
    "$key_two_prefix" "$key_two_suffix" "$positive_two" \
    "password" "abc" "$escaped_backslash" "$escaped_quote" "$escaped_suffix" \
    > "$fixture_root/fixtures/structured.json"
git -C "$fixture_root" add scripts/secret-scan.sh fixtures/structured.json
python3 -c 'import json, pathlib, sys; json.loads(pathlib.Path(sys.argv[1]).read_text())' "$fixture_root/fixtures/structured.json"

if (cd "$fixture_root" && bash scripts/secret-scan.sh > scan-output.txt 2>&1); then
    printf '%s\n' 'secret-scan unexpectedly accepted structured credential controls' >&2
    cat "$fixture_root/scan-output.txt" >&2
    exit 1
fi
if ! grep -q 'fixtures/structured.json' "$fixture_root/scan-output.txt"; then
    cat "$fixture_root/scan-output.txt" >&2
    exit 1
fi

printf '{"%s":"%s%s%s%s"}\n' \
    "password" "abc" "$escaped_backslash" "$escaped_quote" "$escaped_suffix" \
    > "$fixture_root/fixtures/structured.json"
git -C "$fixture_root" add fixtures/structured.json
python3 -c 'import json, pathlib, sys; json.loads(pathlib.Path(sys.argv[1]).read_text())' "$fixture_root/fixtures/structured.json"
if (cd "$fixture_root" && bash scripts/secret-scan.sh > scan-output.txt 2>&1); then
    printf '%s\n' 'secret-scan unexpectedly accepted escaped credential control' >&2
    cat "$fixture_root/scan-output.txt" >&2
    exit 1
fi
if ! grep -q 'fixtures/structured.json' "$fixture_root/scan-output.txt"; then
    cat "$fixture_root/scan-output.txt" >&2
    exit 1
fi

printf '{"%s%s":"[REDACTED]","%s%s":"TEST_ONLY_ACCESS_TOKEN_PLACEHOLDER","%s":"%s%s%s"}\n' \
    "$key_one_prefix" "$key_one_suffix" \
    "$key_two_prefix" "$key_two_suffix" \
    "password" "$escaped_backslash" "$escaped_quote" "TEST_ONLY_ESCAPED_PLACEHOLDER" \
    > "$fixture_root/fixtures/structured.json"
git -C "$fixture_root" add fixtures/structured.json
if ! (cd "$fixture_root" && bash scripts/secret-scan.sh > scan-output.txt 2>&1); then
    cat "$fixture_root/scan-output.txt" >&2
    exit 1
fi
printf '%s\n' 'secret-scan structured/placeholder fixture passed'

cat > "$fixture_root/fixtures/code.go" <<'EOF'
package example

type Observation struct {
    Token string
}

func project(bundle Bundle) Observation {
    return Observation{Token: bundle.HierarchyFreshnessToken}
}
EOF
git -C "$fixture_root" add fixtures/code.go
if ! (cd "$fixture_root" && bash scripts/secret-scan.sh > scan-output.txt 2>&1); then
    cat "$fixture_root/scan-output.txt" >&2
    exit 1
fi
printf '%s\n' 'secret-scan code selector fixture passed'
