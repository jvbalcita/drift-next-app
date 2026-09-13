#!/usr/bin/env bash

set -euo pipefail

# Keep this scan narrow: it looks for recognizable credential formats. It uses
# only Python and Git so the same gate works on developer machines and CI
# runners. Known placeholders
# are removed from the candidate line only; surrounding content is still scanned.
python3 - <<'PY'
from __future__ import annotations

import re
import subprocess
import sys

patterns = [
    re.compile(r"-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----"),
    re.compile(r'''(?i)(^|[^A-Za-z0-9_])(password|passwd|passphrase|token|api[-_]?key|authorization|cookie|private[-_]?key|dsn|connection[-_]?string|secret|credential) *[:=] *["']?[A-Za-z0-9_./+=:-]{16,}'''),
    re.compile(r"AKIA[0-9A-Z]{16}"),
    re.compile(r"gh[pousr]_[A-Za-z0-9_]{20,}"),
    re.compile(r"xox[baprs]-[A-Za-z0-9-]{20,}"),
]


def remove_placeholders(line: str) -> str:
    delimiters = {" ", chr(9), chr(10), chr(13), chr(34), "'", ","}
    for prefix in ("dsn=", "dsn:", "connection_string=", "connection_string:", "connection-string=", "connection-string:"):
        lower = line.lower()
        start = lower.find(prefix)
        while start >= 0:
            end = start + len(prefix)
            while end < len(line) and line[end] not in delimiters:
                end += 1
            if "***" in line[start:end] or "TEST_ONLY_" in line[start:end] or "[REDACTED]" in line[start:end]:
                line = line[:start] + prefix + "MASKED_DSN" + line[end:]
                lower = line.lower()
            start = lower.find(prefix, start + len(prefix))
    line = line.replace("[REDACTED]", "REDACTED")
    marker = "TEST_ONLY_"
    while marker in line:
        start = line.index(marker)
        end = start + len(marker)
        while end < len(line) and (line[end].isalnum() or line[end] == "_"):
            end += 1
        line = line[:start] + line[end:]
    return line

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
        candidate = remove_placeholders(line)
        if any(pattern.search(candidate) for pattern in patterns):
            violations.append((path, line_number))

if violations:
    for path, line_number in violations:
        print(f"possible credential format: {path}:{line_number}")
    sys.exit(1)
PY
