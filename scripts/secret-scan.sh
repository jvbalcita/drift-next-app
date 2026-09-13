#!/usr/bin/env bash

set -euo pipefail

# Keep this scan narrow: it looks for recognizable credential formats. It uses
# only Python and Git so the same gate works on developer machines and CI
# runners. Known placeholders are removed from candidates; surrounding content
# is still scanned.
python3 - <<'PY'
from __future__ import annotations

import re
import subprocess
import sys

sensitive_key = r"(?:[a-z0-9]+[_-])*(?:password|passwd|passphrase|token|api[_-]?key|apikey|authorization|cookie|private[_-]?key|dsn|connection[_-]?string|secret|credential)(?:[_-][a-z0-9]+)*"
patterns = [
    re.compile(r"-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----"),
    re.compile(
        rf'''(?ix)
        (?<![a-z0-9_])
        ["']?{sensitive_key}["']?
        \s*[:=]\s*
        (?:"[^"\n]{{16,}}"|'[^'\n]{{16,}}'|[a-z0-9_./+=:-]{{16,}})
        '''
    ),
    re.compile(r"AKIA[0-9A-Z]{16}"),
    re.compile(r"gh[pousr]_[A-Za-z0-9_]{20,}"),
    re.compile(r"xox[baprs]-[A-Za-z0-9-]{20,}"),
]


def remove_placeholders(text: str) -> str:
    text = re.sub(r"\$\{[A-Za-z_][A-Za-z0-9_]*:\?[^}\n]*\}", "ENV_REQUIRED", text)
    delimiters = {" ", chr(9), chr(10), chr(13), chr(34), "'", ","}
    for prefix in (
        "dsn=",
        "dsn:",
        "connection_string=",
        "connection_string:",
        "connection-string=",
        "connection-string:",
    ):
        lower = text.lower()
        start = lower.find(prefix)
        while start >= 0:
            end = start + len(prefix)
            while end < len(text) and text[end] not in delimiters:
                end += 1
            if "***" in text[start:end] or "TEST_ONLY_" in text[start:end] or "[REDACTED]" in text[start:end]:
                text = text[:start] + prefix + "MASKED_DSN" + text[end:]
                lower = text.lower()
            start = lower.find(prefix, start + len(prefix))
    text = text.replace("[REDACTED]", "REDACTED")
    marker = "TEST_ONLY_"
    while marker in text:
        start = text.index(marker)
        end = start + len(marker)
        while end < len(text) and (text[end].isalnum() or text[end] == "_"):
            end += 1
        text = text[:start] + text[end:]
    return text


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
    candidate = remove_placeholders(text)
    for pattern in patterns:
        match = pattern.search(candidate)
        if match:
            line_number = candidate.count("\n", 0, match.start()) + 1
            violations.append((path, line_number))
            break

if violations:
    for path, line_number in violations:
        print(f"possible credential format: {path}:{line_number}")
    sys.exit(1)
PY
