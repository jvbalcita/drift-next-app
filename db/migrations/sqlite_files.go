package migrations

import "embed"

// SQLiteFiles contains the immutable domain migration series. The historical
// PostgreSQL 0001 bootstrap is intentionally excluded from this filesystem.
//
//go:embed 0002_*.sql 0003_*.sql 0004_*.sql 0005_*.sql 0006_*.sql 0007_*.sql 0008_*.sql 0009_*.sql 0010_*.sql 0011_*.sql 0012_*.sql 0013_*.sql 0014_*.sql 0015_*.sql 0016_*.sql 0017_*.sql 0018_*.sql 0019_*.sql 0020_*.sql 0021_*.sql 0022_*.sql 0023_*.sql 0024_*.sql 0025_*.sql
var SQLiteFiles embed.FS
