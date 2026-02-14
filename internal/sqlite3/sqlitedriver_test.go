package sqlite3

import (
	"testing"

	"github.com/ncruces/go-sqlite3/driver"
)

func TestDriverOpen_SQLiteAndVecVersion(t *testing.T) {
	db, err := driver.Open(":memory:")
	if err != nil {
		t.Fatalf("driver.Open failed: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	var sqliteVersion, vecVersion string
	err = db.QueryRow(`SELECT sqlite_version(), vec_version()`).Scan(&sqliteVersion, &vecVersion)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if sqliteVersion == "" {
		t.Fatal("sqlite_version() returned empty value")
	}
	if vecVersion == "" {
		t.Fatal("vec_version() returned empty value")
	}

	t.Logf("sqlite_version=%s, vec_version=%s", sqliteVersion, vecVersion)
}
