#!/usr/bin/env bash

set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"

temporary_root=$(mktemp -d "${TMPDIR:-/tmp}/drift-next-generated.XXXXXX")
trap 'rm -rf "$temporary_root"' EXIT

template="$temporary_root/buf.gen.yaml"
expected_root="$temporary_root/generated"

python3 - "$template" "$expected_root" <<'PY'
from pathlib import Path
import json
import sys

source = Path("buf.gen.yaml").read_text(encoding="utf-8")
expected_root = Path(sys.argv[2])
output_paths = {
    "gen/go": expected_root / "gen/go",
    "apps/console/src/gen": expected_root / "apps/console/src/gen",
}
lines = []
for line in source.splitlines(keepends=True):
    stripped = line.strip()
    replacement = None
    for relative, output in output_paths.items():
        if stripped == f"out: {relative}":
            indent = line[: len(line) - len(line.lstrip())]
            replacement = f"{indent}out: {json.dumps(str(output))}\n"
            break
    lines.append(replacement if replacement is not None else line)

Path(sys.argv[1]).write_text("".join(lines), encoding="utf-8")
PY

# Generate into the temporary tree. The tracked output directories are never
# modified, so stale files cannot be hidden by an in-place regeneration.
buf generate --template "$template"

python3 - "$expected_root" "$repo_root" <<'PY'
from pathlib import Path
import sys

expected_root = Path(sys.argv[1])
repo_root = Path(sys.argv[2])
roots = (
    ("gen/go", expected_root / "gen/go", repo_root / "gen/go"),
    (
        "apps/console/src/gen",
        expected_root / "apps/console/src/gen",
        repo_root / "apps/console/src/gen",
    ),
)


def normalize_eof(data: bytes) -> bytes:
    """Apply the generator's known EOF line-ending difference only."""
    if not data:
        return data
    return data.rstrip(b"\r\n") + b"\n"


def snapshot(root: Path) -> dict[Path, bytes]:
    if not root.exists():
        return {}
    return {
        path.relative_to(root): normalize_eof(path.read_bytes())
        for path in root.rglob("*")
        if path.is_file()
    }

failures: list[str] = []
for label, expected_dir, actual_dir in roots:
    expected = snapshot(expected_dir)
    actual = snapshot(actual_dir)
    for path in sorted(expected.keys() - actual.keys()):
        failures.append(f"missing generated file: {label}/{path}")
    for path in sorted(actual.keys() - expected.keys()):
        failures.append(f"stale generated file: {label}/{path}")
    for path in sorted(expected.keys() & actual.keys()):
        if expected[path] != actual[path]:
            failures.append(f"changed generated file: {label}/{path}")

if failures:
    print("generated artifacts are stale:", file=sys.stderr)
    for failure in failures:
        print(f"- {failure}", file=sys.stderr)
    raise SystemExit(1)
PY
