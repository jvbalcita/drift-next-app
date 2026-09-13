package sqlite

import "database/sql"

// SQLForTest exposes the database only to package integration tests. It is
// compiled solely in *_test builds and is not part of the production API.
func SQLForTest(store *DB) *sql.DB {
	if store == nil {
		return nil
	}
	return store.db
}
