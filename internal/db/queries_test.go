package db

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// newMigratedTestDB adapts the *sql.DB helper from open_test.go to the
// sqlc-generated DBTX interface.
func newMigratedTestDB(t *testing.T) DBTX {
	t.Helper()
	return newMigratedDB(t)
}

// TestLocationCTEBoundedUnderParentCycle: a parent cycle (only formable by
// writing the DB directly; the PATCH guard prevents it via the API) must
// not send the recursive CTEs into unbounded recursion. Both queries must
// terminate with a bounded row count — the depth cap in the recursive arm
// is defense-in-depth, and this test times out (rather than hanging) if a
// regression drops it.
func TestLocationCTEBoundedUnderParentCycle(t *testing.T) {
	d := newMigratedTestDB(t)
	q := New(d)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)

	u, err := q.CreateUser(ctx, CreateUserParams{
		Email: "cycle@b.c", PasswordHash: "h", DisplayName: "A",
		DefaultItemVisibility: "private", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	a, err := q.CreateLocation(ctx, CreateLocationParams{
		UserID: u.ID, Name: "A", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := q.CreateLocation(ctx, CreateLocationParams{
		UserID: u.ID, Name: "B", ParentID: sql.NullInt64{Int64: a.ID, Valid: true},
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	// hand-craft the cycle A -> B -> A (PATCH would refuse)
	if _, err := d.ExecContext(ctx, `UPDATE locations SET parent_id = ? WHERE id = ?`,
		b.ID, a.ID); err != nil {
		t.Fatal(err)
	}

	runBounded := func(name string, fn func()) {
		t.Helper()
		done := make(chan int, 1)
		go func() {
			fn()
			done <- 0
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s did not terminate under a parent cycle (depth guard missing)", name)
		}
	}

	runBounded("LocationDescendants", func() {
		rows, err := q.LocationDescendants(ctx, LocationDescendantsParams{
			ID: a.ID, UserID: u.ID, UserID_2: u.ID,
		})
		if err != nil {
			t.Error(err)
			return
		}
		// cycle A<->B: anchor + 50 capped steps = 51 rows max
		if len(rows) > 51 {
			t.Errorf("LocationDescendants returned %d rows, want <= 51", len(rows))
		}
	})
	runBounded("LocationBreadcrumbs", func() {
		rows, err := q.LocationBreadcrumbs(ctx, LocationBreadcrumbsParams{
			ID: a.ID, UserID: u.ID, UserID_2: u.ID,
		})
		if err != nil {
			t.Error(err)
			return
		}
		if len(rows) > 51 {
			t.Errorf("LocationBreadcrumbs returned %d rows, want <= 51", len(rows))
		}
	})
}

func TestUserSessionInviteQueries(t *testing.T) {
	d := newMigratedTestDB(t)
	q := New(d)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)

	if n, err := q.CountUsers(ctx); err != nil || n != 0 {
		t.Fatalf("CountUsers = %d, %v; want 0", n, err)
	}

	// Note: sqlc's sqlite engine maps INTEGER columns to int64 (IsAdmin),
	// last_ip to LastIp, and nullable columns to sql.Null*.
	u, err := q.CreateUser(ctx, CreateUserParams{
		Email: "a@b.c", PasswordHash: "h", DisplayName: "A",
		DefaultItemVisibility: "private", IsAdmin: 1, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != 1 || u.IsAdmin != 1 {
		t.Fatalf("created user = %+v", u)
	}

	s, err := q.CreateSession(ctx, CreateSessionParams{
		TokenHash: "th1", UserID: u.ID, UserAgent: "test", LastIp: "127.0.0.1",
		CreatedAt: now, LastSeenAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := q.GetSessionWithUser(ctx, "th1")
	if err != nil {
		t.Fatal(err)
	}
	if got.UserID != u.ID || got.Disabled != 0 {
		t.Fatalf("session join = %+v", got)
	}
	if err := q.DeleteSession(ctx, DeleteSessionParams{ID: s.ID, UserID: u.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetSessionWithUser(ctx, "th1"); err == nil {
		t.Fatal("deleted session still found")
	}

	inv, err := q.CreateInvite(ctx, CreateInviteParams{Code: "ABCD2345", CreatedBy: u.ID, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.MarkInviteUsed(ctx, MarkInviteUsedParams{
		UsedBy: sql.NullInt64{Int64: u.ID, Valid: true},
		UsedAt: sql.NullString{String: now, Valid: true},
		Code:   "ABCD2345",
	}); err != nil {
		t.Fatal(err)
	}
	gotInv, err := q.GetInvite(ctx, "ABCD2345")
	if err != nil || gotInv.UsedBy.Int64 != u.ID || inv.Code != "ABCD2345" {
		t.Fatalf("invite = %+v err=%v", gotInv, err)
	}
}
