package metadata

import (
	"compress/gzip"
	"database/sql"
	"fmt"
	"io"
	"os"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Item struct {
	Service    string
	User       string
	ObjectKey  string
	ItemPath   string
	ItemID     string
	Size       int64
	Checksum   string
	ModifiedAt time.Time
}

type DB struct {
	db *sql.DB
}

func New(path string) (*DB, error) {
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000&parseTime=true")
	if err != nil {
		return nil, fmt.Errorf("opening db: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("pinging db: %w", err)
	}
	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("migrating db: %w", err)
	}
	return &DB{db: db}, nil
}

func (d *DB) Close() error {
	return d.db.Close()
}

func migrate(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS items (
		service    TEXT NOT NULL,
		user_email TEXT NOT NULL,
		object_key TEXT NOT NULL,
		item_path  TEXT NOT NULL DEFAULT '',
		item_id    TEXT NOT NULL,
		size       INTEGER NOT NULL DEFAULT 0,
		checksum   TEXT NOT NULL DEFAULT '',
		modified_at DATETIME NOT NULL,
		PRIMARY KEY (service, user_email, item_id)
	);
	CREATE TABLE IF NOT EXISTS backup_runs (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		service    TEXT NOT NULL,
		user_email TEXT NOT NULL,
		run_type   TEXT NOT NULL,
		started_at DATETIME NOT NULL,
		completed_at DATETIME
	);
	`
	if _, err := db.Exec(schema); err != nil {
		return err
	}

	row := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('items') WHERE name='item_path'")
	var count int
	if err := row.Scan(&count); err != nil {
		return fmt.Errorf("checking schema: %w", err)
	}
	if count == 0 {
		if _, err := db.Exec("ALTER TABLE items ADD COLUMN item_path TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("adding item_path: %w", err)
		}
	}
	return nil
}

func (d *DB) TrackItem(item *Item) error {
	_, err := d.db.Exec(`
		INSERT OR REPLACE INTO items (service, user_email, object_key, item_path, item_id, size, checksum, modified_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		item.Service, item.User, item.ObjectKey, item.ItemPath, item.ItemID, item.Size, item.Checksum, item.ModifiedAt,
	)
	return err
}

func (d *DB) GetItem(service, user, itemID string) (*Item, error) {
	row := d.db.QueryRow(`
		SELECT service, user_email, object_key, item_path, item_id, size, checksum, modified_at
		FROM items WHERE service = ? AND user_email = ? AND item_id = ?`,
		service, user, itemID,
	)
	item := &Item{}
	err := row.Scan(&item.Service, &item.User, &item.ObjectKey, &item.ItemPath, &item.ItemID, &item.Size, &item.Checksum, &item.ModifiedAt)
	if err != nil {
		return nil, err
	}
	return item, nil
}

func (d *DB) IsModified(service, user, itemID, checksum string) (bool, error) {
	existing, err := d.GetItem(service, user, itemID)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return existing.Checksum != checksum, nil
}

func (d *DB) LastBackupTime(service, user string) (time.Time, error) {
	row := d.db.QueryRow(`
		SELECT MAX(completed_at) FROM backup_runs
		WHERE service = ? AND user_email = ?`,
		service, user,
	)
	var s sql.NullString
	err := row.Scan(&s)
	if err != nil {
		return time.Time{}, err
	}
	if !s.Valid || s.String == "" {
		return time.Time{}, sql.ErrNoRows
	}
	t, err := time.Parse("2006-01-02 15:04:05.999999999-07:00", s.String)
	if err != nil {
		return time.Time{}, err
	}
	return t, nil
}

func (d *DB) RecordBackup(service, user, runType string) error {
	_, err := d.db.Exec(`
		INSERT INTO backup_runs (service, user_email, run_type, started_at, completed_at)
		VALUES (?, ?, ?, ?, ?)`,
		service, user, runType, time.Now(), time.Now(),
	)
	return err
}

func (d *DB) ItemsByService(service, user string) ([]*Item, error) {
	rows, err := d.db.Query(`
		SELECT service, user_email, object_key, item_path, item_id, size, checksum, modified_at
		FROM items WHERE service = ? AND user_email = ?`,
		service, user,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []*Item
	for rows.Next() {
		item := &Item{}
		if err := rows.Scan(&item.Service, &item.User, &item.ObjectKey, &item.ItemPath, &item.ItemID, &item.Size, &item.Checksum, &item.ModifiedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func BackupDB(sourcePath, destPath string) error {
	src, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("opening source: %w", err)
	}
	defer src.Close()

	dst, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("creating dest: %w", err)
	}

	gw := gzip.NewWriter(dst)
	if _, err := io.Copy(gw, src); err != nil {
		gw.Close()
		dst.Close()
		os.Remove(destPath)
		return fmt.Errorf("compressing: %w", err)
	}
	if err := gw.Close(); err != nil {
		dst.Close()
		os.Remove(destPath)
		return fmt.Errorf("closing gzip writer: %w", err)
	}
	if err := dst.Sync(); err != nil {
		dst.Close()
		os.Remove(destPath)
		return fmt.Errorf("syncing dest: %w", err)
	}
	if err := dst.Close(); err != nil {
		os.Remove(destPath)
		return fmt.Errorf("closing dest: %w", err)
	}
	return nil
}

func RestoreDB(sourcePath, destPath string) error {
	src, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("opening source: %w", err)
	}
	defer src.Close()

	gr, err := gzip.NewReader(src)
	if err != nil {
		return fmt.Errorf("creating gzip reader: %w", err)
	}
	defer gr.Close()

	tmpPath := destPath + ".tmp"
	tmp, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("creating temp: %w", err)
	}
	if _, err := io.Copy(tmp, gr); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("decompressing: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("syncing temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("closing temp: %w", err)
	}

	if _, err := os.Stat(destPath); err == nil {
		if err := os.Rename(destPath, destPath+".bak"); err != nil {
			os.Remove(tmpPath)
			return fmt.Errorf("backing up existing db: %w", err)
		}
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("installing restored db: %w", err)
	}
	return nil
}
