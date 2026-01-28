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
		`CREATE TABLE IF NOT EXISTS sync_records (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			folder_id INTEGER,
			relative_path TEXT,
			local_hash TEXT,
			remote_etag TEXT,
			modified_at INTEGER,
			deleted BOOLEAN DEFAULT 0,
			created_at INTEGER DEFAULT (strftime('%s', 'now')),
			FOREIGN KEY (folder_id) REFERENCES sync_folders(id) ON DELETE CASCADE,
			UNIQUE(folder_id, relative_path)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sync_records_folder ON sync_records(folder_id, relative_path)`,
		`CREATE INDEX IF NOT EXISTS idx_sync_records_deleted ON sync_records(deleted, created_at)`,
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

type SyncRecord struct {
	ID           int64
	FolderID     int64
	RelativePath string
	LocalHash    string // SHA256 hash of local file content
	RemoteETag   string // ETag from Nextcloud server
	ModifiedAt   int64
	Deleted      bool
	CreatedAt    int64
}

func GetSyncRecord(folderID int64, relativePath string) (*SyncRecord, error) {
	var r SyncRecord
	err := db.QueryRow(
		"SELECT id, folder_id, relative_path, local_hash, remote_etag, modified_at, deleted, created_at FROM sync_records WHERE folder_id = ? AND relative_path = ?",
		folderID, relativePath,
	).Scan(&r.ID, &r.FolderID, &r.RelativePath, &r.LocalHash, &r.RemoteETag, &r.ModifiedAt, &r.Deleted, &r.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func SaveSyncRecord(folderID int64, relativePath, localHash, remoteETag string, modifiedAt int64, deleted bool) error {
	_, err := db.Exec(
		`INSERT INTO sync_records (folder_id, relative_path, local_hash, remote_etag, modified_at, deleted) 
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(folder_id, relative_path) DO UPDATE SET
		 local_hash = excluded.local_hash,
		 remote_etag = excluded.remote_etag,
		 modified_at = excluded.modified_at,
		 deleted = excluded.deleted`,
		folderID, relativePath, localHash, remoteETag, modifiedAt, deleted,
	)
	return err
}

func GetSyncRecordsForFolder(folderID int64) ([]SyncRecord, error) {
	rows, err := db.Query(
		"SELECT id, folder_id, relative_path, local_hash, remote_etag, modified_at, deleted, created_at FROM sync_records WHERE folder_id = ?",
		folderID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []SyncRecord
	for rows.Next() {
		var r SyncRecord
		if err := rows.Scan(&r.ID, &r.FolderID, &r.RelativePath, &r.LocalHash, &r.RemoteETag, &r.ModifiedAt, &r.Deleted, &r.CreatedAt); err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return records, nil
}

func CleanupOldTombstones(retentionDays int) error {
	_, err := db.Exec(
		"DELETE FROM sync_records WHERE deleted = 1 AND created_at < strftime('%s', 'now', '-? days')",
		retentionDays,
	)
	return err
}

func GetSyncFolderByRemotePath(remotePath string) (*SyncFolder, error) {
	var f SyncFolder
	err := db.QueryRow(
		"SELECT id, remote_path, local_path FROM sync_folders WHERE remote_path = ?",
		remotePath,
	).Scan(&f.ID, &f.RemotePath, &f.LocalPath)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}
