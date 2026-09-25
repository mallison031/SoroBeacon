package store_test

import (
	"context"
	"os"
	"testing"
	"time"
	"fmt"
	"net/url"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sorotrail/sorobeacon/internal/store"
	"github.com/stretchr/testify/require"
)

func TestReplicaRouting(t *testing.T) {
	primaryURL := os.Getenv("TEST_DATABASE_URL")
	if primaryURL == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Replica test")
	}

	// Create a replica test database by connecting to the primary and issuing CREATE DATABASE
	u, err := url.Parse(primaryURL)
	require.NoError(t, err)

	replicaDBName := "beacon_test_replica_" + fmt.Sprintf("%d", time.Now().UnixNano())

	// Connect to postgres default DB or the current one to create a new DB
	pool, err := pgxpool.New(context.Background(), primaryURL)
	require.NoError(t, err)
	defer pool.Close()

	_, err = pool.Exec(context.Background(), "CREATE DATABASE " + replicaDBName)
	if err != nil {
		t.Logf("Failed to create replica DB (might need superuser), skipping: %v", err)
		t.Skip()
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), "DROP DATABASE " + replicaDBName)
	}()

	u.Path = "/" + replicaDBName
	replicaURL := u.String()

	require.NoError(t, store.Migrate(primaryURL))
	require.NoError(t, store.Migrate(replicaURL))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	st, err := store.New(ctx, primaryURL, store.PoolSettings{
		ReplicaURL: replicaURL,
	}, nil)
	require.NoError(t, err)
	defer st.Close()

	// Clear primary just in case
	_, _ = pool.Exec(ctx, "DELETE FROM monitors")

	// 1. Write goes to primary
	m := &store.Monitor{Name: "Test Routing", Enabled: true}
	require.NoError(t, st.CreateMonitor(ctx, m))
	require.NotZero(t, m.ID)

	// 2. Strong read goes to primary
	fetched, err := st.GetMonitor(ctx, m.ID)
	require.NoError(t, err)
	require.Equal(t, m.ID, fetched.ID)

	// 3. Lagging replica read
	page, err := st.ListMonitorsPage(ctx, store.ListFilter{})
	require.NoError(t, err)
	require.Empty(t, page, "replica should be empty, proving ListMonitorsPage routed to it")

	stats, err := st.GetStats(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(0), stats.Monitors)
}
