package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log"
	"nextcloud-gtk/internal/nextcloud"
	"nextcloud-gtk/storage"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// SyncAction represents the action to take for a file
type SyncAction int

const (
	ActionNone SyncAction = iota
	ActionUpload
	ActionDownload
	ActionDeleteLocal
	ActionDeleteRemote
	ActionConflict
)

func (a SyncAction) String() string {
	switch a {
	case ActionNone:
		return "none"
	case ActionUpload:
		return "upload"
	case ActionDownload:
		return "download"
	case ActionDeleteLocal:
		return "delete_local"
	case ActionDeleteRemote:
		return "delete_remote"
	case ActionConflict:
		return "conflict"
	default:
		return "unknown"
	}
}

// SyncEvent represents a sync operation result
type SyncEvent struct {
	Path      string
	Operation string // "upload", "download", "delete_local", "delete_remote", "conflict"
	Success   bool
	Error     error
}

// localFileState holds info about a local file
type localFileState struct {
	hash    string
	modTime int64
	isDir   bool
}

// syncTask represents a single sync operation to execute
type syncTask struct {
	relPath    string
	action     SyncAction
	localState *localFileState
	remoteFile *nextcloud.FileInfo
}

// SyncManager handles file synchronization between local and remote
type SyncManager struct {
	client      *nextcloud.Client
	eventChan   chan SyncEvent
	stopChan    chan struct{}
	watchers    map[int64]*fsnotify.Watcher
	watchersMux sync.RWMutex
	isRunning   bool
	mux         sync.RWMutex
	// Track files currently being synced to avoid self-triggered loops
	syncingFiles map[string]bool
	syncingMux   sync.RWMutex
}

// NewSyncManager creates a new sync manager
func NewSyncManager(client *nextcloud.Client) *SyncManager {
	return &SyncManager{
		client:       client,
		eventChan:    make(chan SyncEvent, 100),
		stopChan:     make(chan struct{}),
		watchers:     make(map[int64]*fsnotify.Watcher),
		syncingFiles: make(map[string]bool),
	}
}

// isFileSyncing checks if a file is currently being synced
func (sm *SyncManager) isFileSyncing(relPath string) bool {
	sm.syncingMux.RLock()
	defer sm.syncingMux.RUnlock()
	return sm.syncingFiles[relPath]
}

// markFileSyncing marks a file as currently syncing or not syncing
func (sm *SyncManager) markFileSyncing(relPath string, syncing bool) {
	sm.syncingMux.Lock()
	defer sm.syncingMux.Unlock()
	if syncing {
		sm.syncingFiles[relPath] = true
	} else {
		delete(sm.syncingFiles, relPath)
	}
}

// withRetry executes an operation with exponential backoff retry
func (sm *SyncManager) withRetry(op func() error) error {
	backoff := 500 * time.Millisecond
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if err := op(); err == nil {
			return nil
		} else {
			lastErr = err
			log.Printf("Retry attempt %d failed: %v", attempt+1, err)
		}
		time.Sleep(backoff)
		backoff *= 2
	}
	return lastErr
}

// Start begins the sync process
func (sm *SyncManager) Start() {
	sm.mux.Lock()
	defer sm.mux.Unlock()

	if sm.isRunning {
		return
	}

	sm.isRunning = true
	log.Println("Starting sync manager")

	go sm.remotePollingLoop()
	go sm.tombstoneCleanupLoop()
	go sm.SyncAllFolders()
}

// Stop halts the sync process
func (sm *SyncManager) Stop() {
	sm.mux.Lock()
	defer sm.mux.Unlock()

	if !sm.isRunning {
		return
	}

	sm.isRunning = false
	close(sm.stopChan)

	sm.watchersMux.Lock()
	for _, watcher := range sm.watchers {
		watcher.Close()
	}
	sm.watchers = make(map[int64]*fsnotify.Watcher)
	sm.watchersMux.Unlock()

	log.Println("Sync manager stopped")
}

// GetEventChannel returns the channel for sync events
func (sm *SyncManager) GetEventChannel() <-chan SyncEvent {
	return sm.eventChan
}

// decideSyncAction determines what action to take for a file based on state
func decideSyncAction(localHash, remoteETag string, localExists, remoteExists bool, record *storage.SyncRecord) SyncAction {
	var knownLocalHash, knownRemoteETag string
	if record != nil && !record.Deleted {
		knownLocalHash = record.LocalHash
		knownRemoteETag = record.RemoteETag
	}

	localChanged := localExists && localHash != knownLocalHash
	remoteChanged := remoteExists && remoteETag != knownRemoteETag

	// Both deleted
	if !localExists && !remoteExists {
		return ActionNone
	}

	// Only remote exists
	if !localExists && remoteExists {
		if knownLocalHash == "" && knownRemoteETag == "" {
			return ActionDownload // New remote file
		}
		if remoteETag == knownRemoteETag {
			return ActionDeleteRemote // Local was deleted, propagate
		}
		return ActionDownload // Conflict: local deleted, remote modified - restore
	}

	// Only local exists
	if localExists && !remoteExists {
		if knownLocalHash == "" && knownRemoteETag == "" {
			return ActionUpload // New local file
		}
		if localHash == knownLocalHash {
			return ActionDeleteLocal // Remote was deleted, propagate
		}
		return ActionUpload // Conflict: remote deleted, local modified - upload
	}

	// Both exist
	if !localChanged && !remoteChanged {
		return ActionNone
	}
	if localChanged && !remoteChanged {
		return ActionUpload
	}
	if !localChanged && remoteChanged {
		return ActionDownload
	}
	return ActionConflict // Both changed
}

// computeFileHash computes SHA256 hash of file contents
func computeFileHash(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// computeHashForContent computes hash from byte slice
func computeHashForContent(content []byte) string {
	hash := sha256.Sum256(content)
	return hex.EncodeToString(hash[:])
}

// SyncAllFolders performs sync for all configured folders
func (sm *SyncManager) SyncAllFolders() {
	folders, err := storage.GetSyncFolders()
	if err != nil {
		log.Printf("Failed to get sync folders: %v", err)
		return
	}

	for _, folder := range folders {
		sm.watchersMux.RLock()
		_, alreadyWatching := sm.watchers[folder.ID]
		sm.watchersMux.RUnlock()

		if !alreadyWatching {
			go sm.setupFolderWatcher(folder)
		}

		sm.syncFolder(folder)
	}
}

// gatherLocalState walks the local directory and returns file states
func gatherLocalState(localPath string) (map[string]*localFileState, error) {
	files := make(map[string]*localFileState)

	err := filepath.Walk(localPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(localPath, path)
		if err != nil {
			return err
		}
		relPath = filepath.ToSlash(relPath)

		if relPath == "." {
			return nil
		}

		if isIgnoredFile(relPath) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		state := &localFileState{
			modTime: info.ModTime().Unix(),
			isDir:   info.IsDir(),
		}

		if !info.IsDir() {
			hash, err := computeFileHash(path)
			if err != nil {
				log.Printf("Failed to compute hash for %s: %v", path, err)
			} else {
				state.hash = hash
			}
		}

		files[relPath] = state
		return nil
	})

	return files, err
}

// gatherRemoteState fetches all remote files recursively
func (sm *SyncManager) gatherRemoteState(remotePath string) (map[string]nextcloud.FileInfo, map[string]bool, error) {
	files := make(map[string]nextcloud.FileInfo)
	dirs := make(map[string]bool)

	var gather func(rPath, prefix string) error
	gather = func(rPath, prefix string) error {
		fileList, err := sm.client.ListFiles(rPath)
		if err != nil {
			return err
		}

		for _, file := range fileList {
			relPath := filepath.ToSlash(filepath.Join(prefix, file.Name))

			if file.Type == "dir" {
				if relPath != "." && relPath != "" {
					dirs[relPath] = true
				}
				if err := gather(rPath+"/"+file.Name, relPath); err != nil {
					return err
				}
			} else {
				files[relPath] = file
			}
		}
		return nil
	}

	err := gather(remotePath, "")
	return files, dirs, err
}

// gatherSyncRecords loads all sync records for a folder into a map
func gatherSyncRecords(folderID int64) (map[string]*storage.SyncRecord, error) {
	records, err := storage.GetSyncRecordsForFolder(folderID)
	if err != nil {
		return nil, err
	}

	result := make(map[string]*storage.SyncRecord)
	for i := range records {
		result[records[i].RelativePath] = &records[i]
	}
	return result, nil
}

// syncFolder performs a full sync of a single folder using the 3-phase approach
func (sm *SyncManager) syncFolder(folder storage.SyncFolder) {
	log.Printf("Starting sync for folder: %s -> %s", folder.RemotePath, folder.LocalPath)

	// Phase 1: Gather all state
	localFiles, err := gatherLocalState(folder.LocalPath)
	if err != nil {
		log.Printf("Failed to gather local state for %s: %v", folder.LocalPath, err)
	}

	remoteFiles, remoteDirs, err := sm.gatherRemoteState(folder.RemotePath)
	if err != nil {
		log.Printf("Failed to gather remote state for %s: %v", folder.RemotePath, err)
		return
	}

	syncRecords, err := gatherSyncRecords(folder.ID)
	if err != nil {
		log.Printf("Failed to gather sync records for folder %d: %v", folder.ID, err)
		return
	}

	// Phase 2: Detect renames and compute actions
	renames := sm.detectRemoteRenames(folder, localFiles, remoteFiles, syncRecords)

	// Apply renames first
	for oldPath, newPath := range renames {
		oldRecord := syncRecords[oldPath]
		localHash := sm.applyLocalRename(folder, oldPath, newPath, remoteFiles[newPath], oldRecord)

		// Update local state after rename
		if state, exists := localFiles[oldPath]; exists {
			delete(localFiles, oldPath)
			localFiles[newPath] = state
		}

		// Update sync records map to reflect the rename
		delete(syncRecords, oldPath)
		syncRecords[newPath] = &storage.SyncRecord{
			FolderID:     folder.ID,
			RelativePath: newPath,
			LocalHash:    localHash,
			RemoteETag:   remoteFiles[newPath].ETag,
			Deleted:      false,
		}
	}

	// Build union of all paths
	allPaths := make(map[string]bool)
	for path := range localFiles {
		if localFiles[path] != nil && !localFiles[path].isDir {
			allPaths[path] = true
		}
	}
	for path := range remoteFiles {
		allPaths[path] = true
	}
	for path, record := range syncRecords {
		if !record.Deleted {
			allPaths[path] = true
		}
	}

	// Compute actions for each file
	var uploads, downloads, deleteLocals, deleteRemotes, conflicts []syncTask

	for relPath := range allPaths {
		if sm.isFileSyncing(relPath) {
			continue
		}

		// Skip if this was part of a rename
		if _, wasRenamed := renames[relPath]; wasRenamed {
			continue
		}

		var localHash string
		var localExists bool
		if state, ok := localFiles[relPath]; ok && state != nil {
			localHash = state.hash
			localExists = true
		}

		var remoteETag string
		var remoteExists bool
		remoteFile, ok := remoteFiles[relPath]
		if ok {
			remoteETag = remoteFile.ETag
			remoteExists = true
		}

		record := syncRecords[relPath]
		action := decideSyncAction(localHash, remoteETag, localExists, remoteExists, record)

		if action == ActionNone {
			continue
		}

		task := syncTask{
			relPath:    relPath,
			action:     action,
			remoteFile: &remoteFile,
		}
		if state, ok := localFiles[relPath]; ok {
			task.localState = state
		}

		switch action {
		case ActionUpload:
			uploads = append(uploads, task)
		case ActionDownload:
			downloads = append(downloads, task)
		case ActionDeleteLocal:
			deleteLocals = append(deleteLocals, task)
		case ActionDeleteRemote:
			deleteRemotes = append(deleteRemotes, task)
		case ActionConflict:
			conflicts = append(conflicts, task)
		}
	}

	log.Printf("Sync plan: %d uploads, %d downloads, %d local deletes, %d remote deletes, %d conflicts",
		len(uploads), len(downloads), len(deleteLocals), len(deleteRemotes), len(conflicts))

	// Phase 3: Execute actions
	sm.executeUploads(folder, uploads)
	sm.executeDownloads(folder, downloads)

	for _, task := range deleteLocals {
		sm.executeDeleteLocal(folder, task.relPath)
	}
	for _, task := range deleteRemotes {
		sm.executeDeleteRemote(folder, task.relPath)
	}
	for _, task := range conflicts {
		sm.executeConflictResolution(folder, task.relPath, task.remoteFile)
	}

	// Cleanup empty local directories
	localDirs := make(map[string]bool)
	for path, state := range localFiles {
		if state != nil && state.isDir {
			localDirs[path] = true
		}
	}
	sm.cleanupEmptyDirectories(folder, localDirs, remoteDirs)

	log.Printf("Completed sync for folder: %s", folder.RemotePath)
}

// detectRemoteRenames finds files that were renamed on remote
func (sm *SyncManager) detectRemoteRenames(folder storage.SyncFolder, localFiles map[string]*localFileState, remoteFiles map[string]nextcloud.FileInfo, syncRecords map[string]*storage.SyncRecord) map[string]string {
	renames := make(map[string]string)

	// Build ETag -> old path map for files that exist locally but not remotely
	etagToOldPath := make(map[string]string)
	for relPath, record := range syncRecords {
		if record.Deleted || record.RemoteETag == "" {
			continue
		}
		// Local exists but remote doesn't at this path
		if _, localExists := localFiles[relPath]; localExists {
			if _, remoteExists := remoteFiles[relPath]; !remoteExists {
				etagToOldPath[record.RemoteETag] = relPath
			}
		}
	}

	// Check if any remote file has matching ETag
	for remotePath, remoteFile := range remoteFiles {
		if remoteFile.ETag == "" {
			continue
		}
		// Skip if already tracked
		if record, exists := syncRecords[remotePath]; exists && !record.Deleted {
			continue
		}
		if oldPath, found := etagToOldPath[remoteFile.ETag]; found && oldPath != remotePath {
			renames[oldPath] = remotePath
			log.Printf("Detected remote rename: %s -> %s", oldPath, remotePath)
			delete(etagToOldPath, remoteFile.ETag)
		}
	}

	return renames
}

// applyLocalRename renames a local file to match a remote rename
// Returns the local hash of the renamed file for updating in-memory state
func (sm *SyncManager) applyLocalRename(folder storage.SyncFolder, oldPath, newPath string, remoteFile nextcloud.FileInfo, oldRecord *storage.SyncRecord) string {
	sm.markFileSyncing(oldPath, true)
	sm.markFileSyncing(newPath, true)
	defer sm.markFileSyncing(oldPath, false)
	defer sm.markFileSyncing(newPath, false)

	oldLocalPath := filepath.Join(folder.LocalPath, filepath.FromSlash(oldPath))
	newLocalPath := filepath.Join(folder.LocalPath, filepath.FromSlash(newPath))

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(newLocalPath), 0755); err != nil {
		log.Printf("Failed to create directory for rename %s: %v", newPath, err)
		return ""
	}

	if err := os.Rename(oldLocalPath, newLocalPath); err != nil {
		log.Printf("Failed to rename %s to %s: %v", oldPath, newPath, err)
		return ""
	}

	// Preserve local hash from old record, or compute it
	localHash := ""
	if oldRecord != nil {
		localHash = oldRecord.LocalHash
	}
	if localHash == "" {
		if h, err := computeFileHash(newLocalPath); err == nil {
			localHash = h
		}
	}

	// Update sync records
	storage.SaveSyncRecord(folder.ID, oldPath, "", "", time.Now().Unix(), true)
	storage.SaveSyncRecord(folder.ID, newPath, localHash, remoteFile.ETag, time.Now().Unix(), false)

	log.Printf("Applied local rename: %s -> %s", oldPath, newPath)
	return localHash
}

// executeUploads runs uploads concurrently with a worker pool
func (sm *SyncManager) executeUploads(folder storage.SyncFolder, tasks []syncTask) {
	if len(tasks) == 0 {
		return
	}

	const maxWorkers = 5
	var wg sync.WaitGroup
	taskChan := make(chan syncTask, len(tasks))

	numWorkers := maxWorkers
	if len(tasks) < numWorkers {
		numWorkers = len(tasks)
	}

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for task := range taskChan {
				sm.executeUpload(folder, task.relPath)
			}
		}(i)
	}

	for _, task := range tasks {
		taskChan <- task
	}
	close(taskChan)

	wg.Wait()
	log.Printf("Completed %d uploads", len(tasks))
}

// executeDownloads runs downloads concurrently with a worker pool
func (sm *SyncManager) executeDownloads(folder storage.SyncFolder, tasks []syncTask) {
	if len(tasks) == 0 {
		return
	}

	const maxWorkers = 5
	var wg sync.WaitGroup
	taskChan := make(chan syncTask, len(tasks))

	numWorkers := maxWorkers
	if len(tasks) < numWorkers {
		numWorkers = len(tasks)
	}

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for task := range taskChan {
				sm.executeDownload(folder, task.relPath, task.remoteFile)
			}
		}(i)
	}

	for _, task := range tasks {
		taskChan <- task
	}
	close(taskChan)

	wg.Wait()
	log.Printf("Completed %d downloads", len(tasks))
}

// executeUpload uploads a single file with retry
func (sm *SyncManager) executeUpload(folder storage.SyncFolder, relPath string) {
	localPath := filepath.Join(folder.LocalPath, filepath.FromSlash(relPath))
	remotePath := folder.RemotePath + "/" + relPath

	sm.markFileSyncing(relPath, true)
	defer sm.markFileSyncing(relPath, false)

	content, err := os.ReadFile(localPath)
	if err != nil {
		log.Printf("Failed to read local file %s: %v", localPath, err)
		sm.eventChan <- SyncEvent{Path: relPath, Operation: "upload", Success: false, Error: err}
		return
	}

	hash := computeHashForContent(content)

	// Ensure parent directory exists remotely
	dir := filepath.Dir(remotePath)
	if dir != "/" && dir != "." {
		sm.client.MkdirAll(dir)
	}

	err = sm.withRetry(func() error {
		return sm.client.UploadFile(remotePath, content)
	})
	if err != nil {
		log.Printf("Failed to upload file %s: %v", remotePath, err)
		sm.eventChan <- SyncEvent{Path: relPath, Operation: "upload", Success: false, Error: err}
		return
	}

	// Get the new remote ETag
	remoteFiles, _, _ := sm.gatherRemoteState(folder.RemotePath)
	var remoteETag string
	if rf, ok := remoteFiles[relPath]; ok {
		remoteETag = rf.ETag
	}

	info, _ := os.Stat(localPath)
	modTime := time.Now().Unix()
	if info != nil {
		modTime = info.ModTime().Unix()
	}

	storage.SaveSyncRecord(folder.ID, relPath, hash, remoteETag, modTime, false)

	log.Printf("Uploaded: %s", relPath)
	sm.eventChan <- SyncEvent{Path: relPath, Operation: "upload", Success: true}
}

// executeDownload downloads a single file with retry
func (sm *SyncManager) executeDownload(folder storage.SyncFolder, relPath string, remoteFile *nextcloud.FileInfo) {
	localPath := filepath.Join(folder.LocalPath, filepath.FromSlash(relPath))

	sm.markFileSyncing(relPath, true)
	defer sm.markFileSyncing(relPath, false)

	var content []byte
	err := sm.withRetry(func() error {
		var err error
		content, err = sm.client.DownloadFile(folder.RemotePath + "/" + relPath)
		return err
	})
	if err != nil {
		log.Printf("Failed to download file %s: %v", relPath, err)
		sm.eventChan <- SyncEvent{Path: relPath, Operation: "download", Success: false, Error: err}
		return
	}

	hash := computeHashForContent(content)

	// Ensure parent directory exists locally
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		log.Printf("Failed to create directory for %s: %v", localPath, err)
		sm.eventChan <- SyncEvent{Path: relPath, Operation: "download", Success: false, Error: err}
		return
	}

	if err := os.WriteFile(localPath, content, 0644); err != nil {
		log.Printf("Failed to write local file %s: %v", localPath, err)
		sm.eventChan <- SyncEvent{Path: relPath, Operation: "download", Success: false, Error: err}
		return
	}

	var etag string
	if remoteFile != nil {
		etag = remoteFile.ETag
	}
	storage.SaveSyncRecord(folder.ID, relPath, hash, etag, time.Now().Unix(), false)

	log.Printf("Downloaded: %s", relPath)
	sm.eventChan <- SyncEvent{Path: relPath, Operation: "download", Success: true}
}

// executeDeleteLocal deletes a local file
func (sm *SyncManager) executeDeleteLocal(folder storage.SyncFolder, relPath string) {
	localPath := filepath.Join(folder.LocalPath, filepath.FromSlash(relPath))

	sm.markFileSyncing(relPath, true)
	defer sm.markFileSyncing(relPath, false)

	if err := os.Remove(localPath); err != nil && !os.IsNotExist(err) {
		log.Printf("Failed to delete local file %s: %v", localPath, err)
		sm.eventChan <- SyncEvent{Path: relPath, Operation: "delete_local", Success: false, Error: err}
		return
	}

	storage.SaveSyncRecord(folder.ID, relPath, "", "", time.Now().Unix(), true)

	log.Printf("Deleted local: %s", relPath)
	sm.eventChan <- SyncEvent{Path: relPath, Operation: "delete_local", Success: true}
}

// executeDeleteRemote deletes a remote file
func (sm *SyncManager) executeDeleteRemote(folder storage.SyncFolder, relPath string) {
	remotePath := folder.RemotePath + "/" + relPath

	err := sm.withRetry(func() error {
		return sm.client.DeleteFile(remotePath)
	})
	if err != nil {
		log.Printf("Failed to delete remote file %s: %v", remotePath, err)
		sm.eventChan <- SyncEvent{Path: relPath, Operation: "delete_remote", Success: false, Error: err}
		return
	}

	storage.SaveSyncRecord(folder.ID, relPath, "", "", time.Now().Unix(), true)

	log.Printf("Deleted remote: %s", relPath)
	sm.eventChan <- SyncEvent{Path: relPath, Operation: "delete_remote", Success: true}
}

// executeConflictResolution resolves a conflict (currently: remote wins)
func (sm *SyncManager) executeConflictResolution(folder storage.SyncFolder, relPath string, remoteFile *nextcloud.FileInfo) {
	log.Printf("Resolving conflict by downloading remote version: %s", relPath)
	sm.executeDownload(folder, relPath, remoteFile)
	sm.eventChan <- SyncEvent{Path: relPath, Operation: "conflict", Success: true}
}

// cleanupEmptyDirectories removes local directories that are empty and don't exist remotely
func (sm *SyncManager) cleanupEmptyDirectories(folder storage.SyncFolder, localDirs, remoteDirs map[string]bool) {
	type dirInfo struct {
		path  string
		depth int
	}
	var dirs []dirInfo
	for dir := range localDirs {
		if remoteDirs[dir] {
			continue
		}
		depth := strings.Count(dir, "/")
		dirs = append(dirs, dirInfo{path: dir, depth: depth})
	}

	// Sort by depth descending (deepest first)
	for i := 0; i < len(dirs); i++ {
		for j := i + 1; j < len(dirs); j++ {
			if dirs[j].depth > dirs[i].depth {
				dirs[i], dirs[j] = dirs[j], dirs[i]
			}
		}
	}

	for _, d := range dirs {
		dirPath := filepath.Join(folder.LocalPath, filepath.FromSlash(d.path))
		entries, err := os.ReadDir(dirPath)
		if err != nil {
			continue
		}
		if len(entries) == 0 {
			if err := os.Remove(dirPath); err == nil {
				log.Printf("Deleted empty local directory: %s", d.path)
			}
		}
	}
}

// syncFileForLocalChange handles a local file create/write event
// This is conservative: it only uploads, never deletes local files
// This prevents the case where stale remote state causes incorrect deletions
func (sm *SyncManager) syncFileForLocalChange(folder storage.SyncFolder, relPath string, remoteFiles map[string]nextcloud.FileInfo) {
	if sm.isFileSyncing(relPath) {
		return
	}

	localPath := filepath.Join(folder.LocalPath, filepath.FromSlash(relPath))

	// Check if file exists
	info, err := os.Stat(localPath)
	if err != nil {
		log.Printf("File no longer exists, skipping: %s", relPath)
		return
	}

	// Check if it's a directory
	if info.IsDir() {
		return
	}

	// Get local hash
	localHash, err := computeFileHash(localPath)
	if err != nil {
		log.Printf("Failed to compute hash for %s: %v", relPath, err)
		return
	}

	// Get sync record to check if already synced with same content
	record, _ := storage.GetSyncRecord(folder.ID, relPath)
	if record != nil && !record.Deleted && record.LocalHash == localHash {
		// File hasn't changed from what we have recorded, skip
		log.Printf("File unchanged from record, skipping: %s", relPath)
		return
	}

	// For local changes, always upload if the file exists and has changed
	log.Printf("Uploading local change: %s", relPath)
	sm.executeUpload(folder, relPath)
}

// syncFile syncs a single file (used by full folder sync)
func (sm *SyncManager) syncFile(folder storage.SyncFolder, relPath string, remoteFiles map[string]nextcloud.FileInfo) {
	if sm.isFileSyncing(relPath) {
		return
	}

	localPath := filepath.Join(folder.LocalPath, filepath.FromSlash(relPath))

	// Check if it's a directory
	if info, err := os.Stat(localPath); err == nil && info.IsDir() {
		return
	}

	// Get local state
	var localHash string
	var localExists bool
	if _, err := os.Stat(localPath); err == nil {
		localExists = true
		if h, err := computeFileHash(localPath); err == nil {
			localHash = h
		}
	}

	// Get remote state
	var remoteETag string
	var remoteExists bool
	var remoteFile nextcloud.FileInfo
	if rf, ok := remoteFiles[relPath]; ok {
		remoteETag = rf.ETag
		remoteExists = true
		remoteFile = rf
	}

	// Get sync record
	record, _ := storage.GetSyncRecord(folder.ID, relPath)

	action := decideSyncAction(localHash, remoteETag, localExists, remoteExists, record)

	switch action {
	case ActionUpload:
		sm.executeUpload(folder, relPath)
	case ActionDownload:
		sm.executeDownload(folder, relPath, &remoteFile)
	case ActionDeleteLocal:
		sm.executeDeleteLocal(folder, relPath)
	case ActionDeleteRemote:
		sm.executeDeleteRemote(folder, relPath)
	case ActionConflict:
		sm.executeConflictResolution(folder, relPath, &remoteFile)
	}
}

// handleLocalDelete handles a local file deletion from the watcher
func (sm *SyncManager) handleLocalDelete(folder storage.SyncFolder, relPath string, remoteFiles map[string]nextcloud.FileInfo, remoteDirs map[string]bool) {
	log.Printf("Handling local deletion for: %s", relPath)

	// Check if it's a remote directory
	if remoteDirs[relPath] {
		log.Printf("Deleting remote directory: %s", relPath)
		remotePath := folder.RemotePath + "/" + relPath
		if err := sm.client.DeleteFile(remotePath); err != nil {
			log.Printf("Failed to delete remote directory %s: %v", relPath, err)
		} else {
			storage.SaveSyncRecord(folder.ID, relPath, "", "", time.Now().Unix(), true)
		}
		return
	}

	// Check if remote file exists
	if _, exists := remoteFiles[relPath]; !exists {
		log.Printf("Path %s deleted locally and doesn't exist remotely", relPath)
		return
	}

	// Check sync record
	record, _ := storage.GetSyncRecord(folder.ID, relPath)
	if record != nil && !record.Deleted {
		log.Printf("Propagating local deletion to remote: %s", relPath)
		sm.executeDeleteRemote(folder, relPath)
	}
}

// setupFolderWatcher sets up filesystem watcher for a folder
func (sm *SyncManager) setupFolderWatcher(folder storage.SyncFolder) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("Failed to create watcher for %s: %v", folder.LocalPath, err)
		return
	}

	sm.watchersMux.Lock()
	sm.watchers[folder.ID] = watcher
	sm.watchersMux.Unlock()

	// Add all directories to watcher
	err = filepath.Walk(folder.LocalPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return watcher.Add(path)
		}
		return nil
	})
	if err != nil {
		log.Printf("Failed to setup watcher for %s: %v", folder.LocalPath, err)
		return
	}

	log.Printf("Started watching folder: %s", folder.LocalPath)

	go sm.watcherLoop(folder, watcher)
}

// pendingOp represents a pending filesystem operation
type pendingOp struct {
	path     string
	isDelete bool
	isRename bool
	isCreate bool
	isWrite  bool
}

// watcherLoop handles filesystem events with proper debouncing
func (sm *SyncManager) watcherLoop(folder storage.SyncFolder, watcher *fsnotify.Watcher) {
	var pendingChanges []pendingOp
	var changesMux sync.Mutex
	var debounceTimer *time.Timer
	var timerMux sync.Mutex

	processChanges := func() {
		changesMux.Lock()
		changes := make([]pendingOp, len(pendingChanges))
		copy(changes, pendingChanges)
		pendingChanges = pendingChanges[:0]
		changesMux.Unlock()

		if len(changes) == 0 {
			return
		}

		// Deduplicate: keep only the latest event per path, merging flags
		pathToOp := make(map[string]*pendingOp)
		for _, op := range changes {
			if existing, ok := pathToOp[op.path]; ok {
				// Merge flags
				existing.isDelete = existing.isDelete || op.isDelete
				existing.isRename = existing.isRename || op.isRename
				existing.isCreate = existing.isCreate || op.isCreate
				existing.isWrite = existing.isWrite || op.isWrite
			} else {
				opCopy := op
				pathToOp[op.path] = &opCopy
			}
		}

		// Convert back to slice
		changes = make([]pendingOp, 0, len(pathToOp))
		for _, op := range pathToOp {
			// If both create and delete happened, check if file exists now
			if op.isCreate && op.isDelete {
				localPath := filepath.Join(folder.LocalPath, filepath.FromSlash(op.path))
				if _, err := os.Stat(localPath); err == nil {
					// File exists now, treat as create/modify only
					op.isDelete = false
				} else {
					// File doesn't exist, treat as delete only
					op.isCreate = false
				}
			}
			changes = append(changes, *op)
		}

		log.Printf("Processing %d local changes (after dedup)", len(changes))

		// Fetch current remote state once for all changes
		remoteFiles, remoteDirs, err := sm.gatherRemoteState(folder.RemotePath)
		if err != nil {
			log.Printf("Failed to fetch remote state: %v", err)
			return
		}

		// Detect local renames
		renameMap := sm.detectLocalRenamesFromChanges(folder, changes)

		// Process renames first
		for oldPath, newPath := range renameMap {
			sm.handleLocalRename(folder, oldPath, newPath)
		}

		// Build deleted paths set
		deletedPaths := make(map[string]bool)
		deletedDirs := make(map[string]bool)
		for _, op := range changes {
			if op.isDelete {
				deletedPaths[op.path] = true
			}
		}

		// Identify directories
		for path := range deletedPaths {
			for otherPath := range deletedPaths {
				if otherPath != path && strings.HasPrefix(otherPath, path+"/") {
					deletedDirs[path] = true
					break
				}
			}
		}

		// Process changes
		for _, op := range changes {
			// Skip if part of a rename
			if _, wasRenamed := renameMap[op.path]; wasRenamed {
				continue
			}
			isNewPath := false
			for _, newPath := range renameMap {
				if op.path == newPath {
					isNewPath = true
					break
				}
			}
			if isNewPath {
				continue
			}

			// Skip files inside deleted directories
			insideDeletedDir := false
			for dirPath := range deletedDirs {
				if strings.HasPrefix(op.path, dirPath+"/") {
					insideDeletedDir = true
					break
				}
			}
			if insideDeletedDir {
				continue
			}

			if op.isDelete {
				sm.handleLocalDelete(folder, op.path, remoteFiles, remoteDirs)
			} else {
				// For create/write events, only allow upload - never delete local
				sm.syncFileForLocalChange(folder, op.path, remoteFiles)
			}
		}
	}

	resetDebounce := func() {
		timerMux.Lock()
		defer timerMux.Unlock()
		if debounceTimer != nil {
			debounceTimer.Stop()
		}
		debounceTimer = time.AfterFunc(100*time.Millisecond, processChanges)
	}

	for {
		select {
		case <-sm.stopChan:
			watcher.Close()
			return

		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			// Add new directories to watcher
			if event.Op&fsnotify.Create == fsnotify.Create {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					watcher.Add(event.Name)
				}
			}

			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
				relPath, err := filepath.Rel(folder.LocalPath, event.Name)
				if err != nil || isIgnoredFile(relPath) {
					continue
				}

				// Skip directories for file events (except creates)
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					if event.Op&fsnotify.Create == fsnotify.Create {
						log.Printf("New directory created: %s", relPath)
						sm.client.MkdirAll(folder.RemotePath + "/" + relPath)
					}
					continue
				}

				// Skip self-triggered changes
				if sm.isFileSyncing(filepath.ToSlash(relPath)) {
					continue
				}

				changesMux.Lock()
				op := pendingOp{path: filepath.ToSlash(relPath)}
				if event.Op&fsnotify.Remove == fsnotify.Remove {
					op.isDelete = true
				}
				if event.Op&fsnotify.Rename == fsnotify.Rename {
					op.isRename = true
				}
				if event.Op&fsnotify.Create == fsnotify.Create {
					op.isCreate = true
				}
				if event.Op&fsnotify.Write == fsnotify.Write {
					op.isWrite = true
				}
				pendingChanges = append(pendingChanges, op)
				changesMux.Unlock()

				resetDebounce()
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("Watcher error: %v", err)
		}
	}
}

// detectLocalRenamesFromChanges detects renames from a batch of filesystem changes
func (sm *SyncManager) detectLocalRenamesFromChanges(folder storage.SyncFolder, changes []pendingOp) map[string]string {
	renames := make(map[string]string)

	var deleteOps, createOps []pendingOp
	for _, op := range changes {
		if op.isDelete || op.isRename {
			deleteOps = append(deleteOps, op)
		}
		if op.isCreate {
			createOps = append(createOps, op)
		}
	}

	for _, delOp := range deleteOps {
		record, err := storage.GetSyncRecord(folder.ID, delOp.path)
		if err != nil || record == nil || record.Deleted {
			continue
		}

		for _, createOp := range createOps {
			if _, exists := renames[delOp.path]; exists {
				continue
			}

			newLocalPath := filepath.Join(folder.LocalPath, filepath.FromSlash(createOp.path))
			if _, err := os.Stat(newLocalPath); err != nil {
				continue
			}

			newHash, err := computeFileHash(newLocalPath)
			if err != nil {
				continue
			}

			if newHash == record.LocalHash && newHash != "" {
				renames[delOp.path] = createOp.path
				log.Printf("Detected local rename: %s -> %s", delOp.path, createOp.path)
				break
			}
		}
	}

	return renames
}

// handleLocalRename handles a local file rename
func (sm *SyncManager) handleLocalRename(folder storage.SyncFolder, oldPath, newPath string) {
	log.Printf("Handling local rename: %s -> %s", oldPath, newPath)

	record, err := storage.GetSyncRecord(folder.ID, oldPath)
	if err != nil || record == nil {
		log.Printf("No sync record for %s, treating as separate operations", oldPath)
		return
	}

	// Move on remote
	oldRemotePath := folder.RemotePath + "/" + oldPath
	newRemotePath := folder.RemotePath + "/" + newPath

	if err := sm.client.MoveFile(oldRemotePath, newRemotePath); err != nil {
		log.Printf("Failed to rename remote %s to %s: %v", oldPath, newPath, err)
		return
	}

	// Update sync records
	storage.SaveSyncRecord(folder.ID, oldPath, "", "", time.Now().Unix(), true)
	storage.SaveSyncRecord(folder.ID, newPath, record.LocalHash, record.RemoteETag, time.Now().Unix(), false)

	log.Printf("Renamed remote: %s -> %s", oldPath, newPath)
}

// remotePollingLoop polls remote for changes periodically
func (sm *SyncManager) remotePollingLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-sm.stopChan:
			return
		case <-ticker.C:
			log.Println("Running periodic remote sync check")
			sm.SyncAllFolders()
		}
	}
}

// tombstoneCleanupLoop periodically cleans up old tombstones
func (sm *SyncManager) tombstoneCleanupLoop() {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-sm.stopChan:
			return
		case <-ticker.C:
			log.Println("Running tombstone cleanup")
			if err := storage.CleanupOldTombstones(30); err != nil {
				log.Printf("Failed to cleanup tombstones: %v", err)
			}
		}
	}
}

// isIgnoredFile checks if a file should be ignored
func isIgnoredFile(relPath string) bool {
	base := filepath.Base(relPath)
	if strings.HasPrefix(base, ".") {
		return true
	}
	if strings.HasSuffix(base, "~") || strings.HasSuffix(base, ".tmp") || strings.HasSuffix(base, ".temp") {
		return true
	}
	return false
}

// TriggerSyncForFolder manually triggers a sync for a specific folder
func (sm *SyncManager) TriggerSyncForFolder(folderID int64) {
	go func() {
		folders, err := storage.GetSyncFolders()
		if err != nil {
			log.Printf("Failed to get sync folders: %v", err)
			return
		}

		for _, folder := range folders {
			if folder.ID == folderID {
				sm.syncFolder(folder)
				return
			}
		}
	}()
}

// StopWatchingFolder stops watching a specific folder by its ID
func (sm *SyncManager) StopWatchingFolder(folderID int64) {
	sm.watchersMux.Lock()
	defer sm.watchersMux.Unlock()

	if watcher, exists := sm.watchers[folderID]; exists {
		watcher.Close()
		delete(sm.watchers, folderID)
		log.Printf("Stopped watching folder ID: %d", folderID)
	}
}

// StopWatchingFolderByRemotePath stops watching a folder by its remote path
func (sm *SyncManager) StopWatchingFolderByRemotePath(remotePath string) {
	// Get the folder ID from the database first
	folder, err := storage.GetSyncFolderByRemotePath(remotePath)
	if err != nil || folder == nil {
		log.Printf("Could not find folder for remote path: %s", remotePath)
		return
	}
	sm.StopWatchingFolder(folder.ID)
}

// IsRunning returns whether the sync manager is running
func (sm *SyncManager) IsRunning() bool {
	sm.mux.RLock()
	defer sm.mux.RUnlock()
	return sm.isRunning
}
