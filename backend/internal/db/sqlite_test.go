package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestEveryConnectionHasSQLiteSettings(t *testing.T) {
	database, err := New(filepath.Join(t.TempDir(), "app #1 & data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	var connections []*sql.Conn
	defer func() {
		for _, conn := range connections {
			conn.Close()
		}
	}()
	// Holding these open forces the pool to create independent connections.
	for i := 0; i < 4; i++ {
		conn, err := database.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, conn)
		for pragma, want := range map[string]int{"busy_timeout": 5000, "foreign_keys": 1, "synchronous": 1, "cache_size": -20000, "temp_store": 2} {
			var got int
			if err := conn.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("connection %d: %s=%d, want %d", i, pragma, got, want)
			}
		}
	}
}
