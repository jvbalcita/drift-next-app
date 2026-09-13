# Disposable migration testdata

This directory is reserved for small, SQLite-compatible fixtures used by
migration tests. Fixtures are disposable test inputs, not shipped schema and
not production exports. Prefer `testing/fstest.MapFS` in focused tests when a
fixture does not need a file.

Use the same `NNNN_name.sql` convention as the runner and start at `0002`.
Do not copy the historical PostgreSQL `0001_initial.sql` here, and do not put
passwords, tokens, API keys, cookies, private keys, DSNs, connection strings,
device data, or other production values in this directory. Use
`[REDACTED]` or an unmistakable `TEST_ONLY_...` sentinel where a redaction
test needs a value.
