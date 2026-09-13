package sqlite_test

import (
	"context"
	"sync"
	"testing"

	"drift.local/drift-next/internal/organizations"
	store "drift.local/drift-next/internal/store/sqlite"
)

func TestConcurrentSameIdempotencyReservationHasOneWinner(t *testing.T) {
	db := openTestDB(t)
	if err := store.NewWorkspaceService(db).Create(context.Background(), organizations.Workspace{ID: "concurrent-w", Name: "Concurrent", State: organizations.WorkspaceActive}, "operator", "op"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := db.ReserveIdempotency(context.Background(), "concurrent-w", "same-key", "device.create", "same-hash")
			results <- ok
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	winners := 0
	for ok := range results {
		if ok {
			winners++
		}
	}
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent reservation error = %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("reservation winners = %d, want 1", winners)
	}
}
