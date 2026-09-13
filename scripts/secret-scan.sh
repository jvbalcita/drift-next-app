#!/usr/bin/env bash

set -euo pipefail

# Keep this scan narrow: it looks for recognizable credential formats and
# ignores explicit redaction/test sentinels. It uses only Python and Git so
# the same gate works on developer machines and CI runners.
python3 - <<'PY'
from __future__ import annotations

import re
import subprocess
import sys

patterns = [
    re.compile(r"-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----"),
    re.compile(r'''(?i)\b(password|passwd|passphrase|token|api[_-]?key|authorization|cookie|private[_-]?key|dsn|connection[_-]?string)\s*[:=]\s*["']?[A-Za-z0-9_./+=:-]{16,}'''),
    re.compile(r"AKIA[0-9A-Z]{16}"),
    re.compile(r"gh[pousr]_[A-Za-z0-9_]{20,}"),
    re.compile(r"xox[baprs]-[A-Za-z0-9-]{20,}"),
]

files = subprocess.run(
    ["git", "ls-files", "-z"], check=True, capture_output=True
).stdout.split(b"\0")
violations: list[tuple[str, int]] = []
for raw_path in files:
    if not raw_path:
        continue
    path = raw_path.decode("utf-8")
    if path in {"scripts/secret-scan.sh", "go.sum", "pnpm-lock.yaml"}:
        continue
    try:
        text = open(path, "r", encoding="utf-8").read()
    except (OSError, UnicodeDecodeError):
        continue
    for line_number, line in enumerate(text.splitlines(), 1):
        if "[REDACTED]" in line or "TEST_ONLY_" in line or "${" in line:
            continue
        if any(pattern.search(line) for pattern in patterns):
            violations.append((path, line_number))

if violations:
    for path, line_number in violations:
        print(f"possible credential format: {path}:{line_number}")
    sys.exit(1)
PY
