package store

import (
	"database/sql"
	"time"

	_ "modernc.org/sqlite"
)

// NewManagementDB opens and initializes the management SQLite database.
func NewManagementDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	// Optimize SQLite for performance (WAL mode, synchronous NORMAL, etc.)
	if _, err := db.Exec(`
		PRAGMA journal_mode = WAL;
		PRAGMA synchronous = NORMAL;
		PRAGMA cache_size = -2000;
		PRAGMA temp_store = MEMORY;
	`); err != nil {
		db.Close()
		return nil, err
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(time.Hour)

	// Initialize tables via the stores
	pStore := new(projectStore{db: db})
	if err := pStore.init(); err != nil {
		db.Close()
		return nil, err
	}

	uStore := new(sqliteUserStore{db: db})
	if err := uStore.init(); err != nil {
		db.Close()
		return nil, err
	}

	sStore := new(SQLiteSetting{db: db})
	if err := sStore.init(); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}
