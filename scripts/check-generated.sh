#!/usr/bin/env bash

set -euo pipefail

# Buf's pinned TypeScript plugin currently emits an additional blank line at
# EOF. Normalize only trailing line endings before comparing; all substantive
# generated changes remain visible to git diff.
buf generate
python3 - <<'PY'
from pathlib import Path

for root in (Path("gen"), Path("apps/console/src/gen")):
    if not root.exists():
        continue
    for path in root.rglob("*"):
        if path.is_file():
            data = path.read_bytes()
            if data:
                path.write_bytes(data.rstrip(b"\r\n") + b"\n")
PY

if untracked=$(git ls-files --others --exclude-standard -- gen apps/console/src/gen); [ -n "$untracked" ]; then
    printf '%s\n' "unexpected untracked generated files:" >&2
    printf '%s\n' "$untracked" >&2
    exit 1
fi

git diff --exit-code -- gen apps/console/src/gen
