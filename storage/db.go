package storage

import (
	"database/sql"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

var db *sql.DB

func InitDB(dbPath string) error {
	if dbPath == "" {
		home, _ := os.UserHomeDir()
		dbPath = filepath.Join(home, ".config", "nextcloud-gtk", "settings.db")
	}

	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return err
	}

	var err error
	// Use _journal_mode=WAL for better persistence and performance
	db, err = sql.Open("sqlite3", dbPath+"?_journal_mode=WAL")
	if err != nil {
		return err
	}

	return createTables()
}

func createTables() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS sync_folders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			remote_path TEXT,
			local_path TEXT,
			last_sync DATETIME
		)`,
	}

	for _, q := range queries {
		if _, err := db.Exec(q); err != nil {
			return err
		}
	}

	return nil
}

func GetSetting(key string) (string, error) {
	var value string
	err := db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

func SaveSetting(key, value string) error {
	_, err := db.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", key, value)
	if err == nil {
		// Force immediate flush to disk
		db.Exec("PRAGMA wal_checkpoint(FULL)")
	}
	return err
}

func Ping() error {
	return db.Ping()
}

func DeleteSetting(key string) error {
	_, err := db.Exec("DELETE FROM settings WHERE key = ?", key)
	return err
}

func ClearAuth() error {
	_, err := db.Exec("DELETE FROM settings WHERE key IN ('username', 'password')")
	return err
}

type SyncFolder struct {
	ID         int64
	RemotePath string
	LocalPath  string
}

func GetSyncFolders() ([]SyncFolder, error) {
	rows, err := db.Query("SELECT id, remote_path, local_path FROM sync_folders")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var folders []SyncFolder
	for rows.Next() {
		var f SyncFolder
		if err := rows.Scan(&f.ID, &f.RemotePath, &f.LocalPath); err != nil {
			return nil, err
		}
		folders = append(folders, f)
	}
	return folders, nil
}

func AddSyncFolder(remote, local string) error {
	_, err := db.Exec("INSERT INTO sync_folders (remote_path, local_path) VALUES (?, ?)", remote, local)
	return err
}

func RemoveSyncFolder(remotePath string) error {
	_, err := db.Exec("DELETE FROM sync_folders WHERE remote_path = ?", remotePath)
	return err
}
