package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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

// SyncEvent represents a sync operation result
type SyncEvent struct {
	Path      string
	Operation string // "upload", "download", "delete_local", "delete_remote", "conflict"
	Success   bool
	Error     error
}

// pendingOp represents a pending filesystem operation detected by the watcher
type pendingOp struct {
	path     string
	isDelete bool
	isRename bool
	isCreate bool
}

// SyncManager handles file synchronization between local and remote
type SyncManager struct {
	client      *nextcloud.Client
	eventChan   chan SyncEvent
	stopChan    chan struct{}
	watcher     *fsnotify.Watcher
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

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
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

	// Start remote polling goroutine
	go sm.remotePollingLoop()

	// Start tombstone cleanup goroutine
	go sm.tombstoneCleanupLoop()

	// Start initial sync for all folders
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

	// Close all watchers
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

// getLocalFileInfo returns file info and hash for a local file
func getLocalFileInfo(path string) (hash string, modTime int64, exists bool) {
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, false
	}

	if info.IsDir() {
		return "", info.ModTime().Unix(), true
	}

	hash, err = computeFileHash(path)
	if err != nil {
		log.Printf("Failed to compute hash for %s: %v", path, err)
		return "", info.ModTime().Unix(), true
	}

	return hash, info.ModTime().Unix(), true
}

// SyncAllFolders performs sync for all configured folders
func (sm *SyncManager) SyncAllFolders() {
	folders, err := storage.GetSyncFolders()
	if err != nil {
		log.Printf("Failed to get sync folders: %v", err)
		return
	}

	for _, folder := range folders {
		// Setup filesystem watcher for this folder (only if not already watching)
		sm.watchersMux.RLock()
		_, alreadyWatching := sm.watchers[folder.ID]
		sm.watchersMux.RUnlock()

		if !alreadyWatching {
			go sm.setupFolderWatcher(folder)
		}

		// Perform initial sync
		sm.syncFolder(folder)
	}
}

// uploadTask represents a file that needs to be uploaded
type uploadTask struct {
	folder  storage.SyncFolder
	relPath string
}

// syncFolder performs a full sync of a single folder
func (sm *SyncManager) syncFolder(folder storage.SyncFolder) {
	log.Printf("Starting sync for folder: %s -> %s", folder.RemotePath, folder.LocalPath)

	// Get all files and directories in local directory
	localFiles := make(map[string]bool)
	localDirs := make(map[string]bool)
	err := filepath.Walk(folder.LocalPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(folder.LocalPath, path)
		if err != nil {
			return err
		}
		relPath = filepath.ToSlash(relPath)

		if info.IsDir() {
			// Skip the root directory itself
			if relPath != "." {
				localDirs[relPath] = true
			}
			return nil
		}

		localFiles[relPath] = true
		return nil
	})
	if err != nil {
		log.Printf("Failed to walk local directory %s: %v", folder.LocalPath, err)
	}

	// Get all files and directories from remote
	remoteFiles, remoteDirs, err := sm.listRemoteFilesRecursive(folder.RemotePath)
	if err != nil {
		log.Printf("Failed to list remote files for %s: %v", folder.RemotePath, err)
		return
	}

	// Union of all paths
	allPaths := make(map[string]bool)
	for path := range localFiles {
		allPaths[path] = true
	}
	for path := range remoteFiles {
		allPaths[path] = true
	}

	// First pass: detect and handle remote renames before any deletions
	sm.handleRemoteRenames(folder, localFiles, remoteFiles)

	// Refresh local files list after renames
	localFiles = make(map[string]bool)
	filepath.Walk(folder.LocalPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		relPath, _ := filepath.Rel(folder.LocalPath, path)
		localFiles[filepath.ToSlash(relPath)] = true
		return nil
	})

	// Collect uploads and process other operations
	var uploadTasks []uploadTask

	// Sync each file
	for relPath := range allPaths {
		needsUpload := sm.checkAndPrepareSync(folder, relPath, &uploadTasks)
		if !needsUpload {
			// Process non-upload operations immediately
			sm.syncFile(folder, relPath)
		}
	}

	// Process uploads concurrently (up to 5 at a time)
	if len(uploadTasks) > 0 {
		log.Printf("Processing %d uploads concurrently (max 5 at a time)", len(uploadTasks))
		sm.processUploadsConcurrently(uploadTasks)
	}

	// Delete empty local directories that don't exist remotely
	sm.cleanupEmptyDirectories(folder, localDirs, remoteDirs)

	log.Printf("Completed sync for folder: %s", folder.RemotePath)
}

// checkAndPrepareSync checks if a file needs upload and adds it to the task list
// Returns true if the file needs upload (and was added to tasks), false otherwise
func (sm *SyncManager) checkAndPrepareSync(folder storage.SyncFolder, relPath string, tasks *[]uploadTask) bool {
	// Skip if already being synced
	if sm.isFileSyncing(relPath) {
		return false
	}

	localPath := filepath.Join(folder.LocalPath, filepath.FromSlash(relPath))

	// Skip directories
	info, err := os.Stat(localPath)
	if err == nil && info.IsDir() {
		return false
	}

	// Get local file info
	localHash, _, localExists := getLocalFileInfo(localPath)
	if !localExists {
		return false
	}

	// Get remote file info
	remoteFile, remoteExists := sm.getRemoteFileInfo(folder.RemotePath, relPath)
	var remoteHash string
	if remoteExists {
		remoteHash = remoteFile.ETag
	}

	// Get sync record
	syncRecord, err := storage.GetSyncRecord(folder.ID, relPath)
	if err != nil {
		return false
	}

	var knownLocalHash, knownRemoteETag string
	if syncRecord != nil && !syncRecord.Deleted {
		knownLocalHash = syncRecord.LocalHash
		knownRemoteETag = syncRecord.RemoteETag
	}

	// Decision matrix - compare separately
	localChanged := localExists && localHash != knownLocalHash
	remoteChanged := remoteExists && remoteHash != knownRemoteETag

	// Check if upload is needed
	if localExists && !remoteExists {
		if knownLocalHash == "" && knownRemoteETag == "" {
			// New local file - upload
			*tasks = append(*tasks, uploadTask{folder: folder, relPath: relPath})
			return true
		} else if localHash != knownLocalHash {
			// Conflict: remote deleted, local modified - upload
			*tasks = append(*tasks, uploadTask{folder: folder, relPath: relPath})
			return true
		}
	}

	// Both exist - check if upload needed
	if localChanged && !remoteChanged {
		// Upload local → remote
		*tasks = append(*tasks, uploadTask{folder: folder, relPath: relPath})
		return true
	}

	if localChanged && remoteChanged {
		// Conflict - upload
		*tasks = append(*tasks, uploadTask{folder: folder, relPath: relPath})
		return true
	}

	return false
}

// processUploadsConcurrently processes upload tasks concurrently with max 5 workers
func (sm *SyncManager) processUploadsConcurrently(tasks []uploadTask) {
	const maxWorkers = 5

	var wg sync.WaitGroup
	taskChan := make(chan uploadTask, len(tasks))

	// Start workers
	numWorkers := maxWorkers
	if len(tasks) < numWorkers {
		numWorkers = len(tasks)
	}

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for task := range taskChan {
				log.Printf("Worker %d: Uploading %s", workerID, task.relPath)
				sm.uploadFile(task.folder, task.relPath)
			}
		}(i)
	}

	// Queue tasks
	for _, task := range tasks {
		taskChan <- task
	}
	close(taskChan)

	// Wait for all uploads to complete
	wg.Wait()
	log.Printf("Completed %d uploads", len(tasks))
}

// listRemoteFilesRecursive recursively lists all files and directories in a remote directory
func (sm *SyncManager) listRemoteFilesRecursive(remotePath string) (map[string]nextcloud.FileInfo, map[string]bool, error) {
	files := make(map[string]nextcloud.FileInfo)
	dirs := make(map[string]bool)
	err := sm.listRemoteFilesRecursiveHelper(remotePath, "", files, dirs)
	return files, dirs, err
}

func (sm *SyncManager) listRemoteFilesRecursiveHelper(remotePath, relPrefix string, files map[string]nextcloud.FileInfo, dirs map[string]bool) error {
	fileList, err := sm.client.ListFiles(remotePath)
	if err != nil {
		return err
	}

	for _, file := range fileList {
		relPath := filepath.ToSlash(filepath.Join(relPrefix, file.Name))

		if file.Type == "dir" {
			// Track the directory
			if relPath != "." && relPath != "" {
				dirs[relPath] = true
			}
			err := sm.listRemoteFilesRecursiveHelper(
				remotePath+"/"+file.Name,
				relPath,
				files,
				dirs,
			)
			if err != nil {
				return err
			}
		} else {
			files[relPath] = file
		}
	}

	return nil
}

// syncFile syncs a single file based on the decision matrix
func (sm *SyncManager) syncFile(folder storage.SyncFolder, relPath string) {
	// Skip if already being synced
	if sm.isFileSyncing(relPath) {
		return
	}

	localPath := filepath.Join(folder.LocalPath, filepath.FromSlash(relPath))

	// Skip directories
	info, err := os.Stat(localPath)
	if err == nil && info.IsDir() {
		return
	}

	// Get local file info
	localHash, _, localExists := getLocalFileInfo(localPath)

	// Get remote file info
	remoteFile, remoteExists := sm.getRemoteFileInfo(folder.RemotePath, relPath)
	var remoteHash string
	if remoteExists {
		remoteHash = remoteFile.ETag // Using ETag as hash
	}

	// Get sync record
	syncRecord, err := storage.GetSyncRecord(folder.ID, relPath)
	if err != nil {
		log.Printf("Failed to get sync record for %s: %v", relPath, err)
		return
	}

	var knownLocalHash, knownRemoteETag string
	if syncRecord != nil && !syncRecord.Deleted {
		knownLocalHash = syncRecord.LocalHash
		knownRemoteETag = syncRecord.RemoteETag
	}

	// Decision matrix - compare separately
	localChanged := localExists && localHash != knownLocalHash
	remoteChanged := remoteExists && remoteHash != knownRemoteETag

	log.Printf("Sync check for %s: local=%v (hash=%s, known=%s), remote=%v (etag=%s, known=%s)",
		relPath, localExists, localHash[:min(8, len(localHash))], knownLocalHash[:min(8, len(knownLocalHash))],
		remoteExists, remoteHash[:min(8, len(remoteHash))], knownRemoteETag[:min(8, len(knownRemoteETag))])

	// Handle deletions
	if !localExists && !remoteExists {
		// Both deleted - clean up record if it exists
		if syncRecord != nil {
			storage.SaveSyncRecord(folder.ID, relPath, "", "", time.Now().Unix(), true)
		}
		return
	}

	if !localExists && remoteExists {
		if knownLocalHash == "" && knownRemoteETag == "" {
			// New remote file - download
			log.Printf("Downloading new remote file: %s", relPath)
			sm.downloadFile(folder, relPath, remoteFile)
		} else if remoteHash == knownRemoteETag {
			// Local deletion - propagate to remote
			log.Printf("Local deletion detected, deleting remote: %s", relPath)
			sm.deleteRemoteFile(folder, relPath)
		} else {
			// Conflict: local deleted, remote modified
			log.Printf("Conflict (local deleted, remote modified): %s", relPath)
			// Default to restoring remote version
			sm.downloadFile(folder, relPath, remoteFile)
		}
		return
	}

	if localExists && !remoteExists {
		if knownLocalHash == "" && knownRemoteETag == "" {
			// New local file - upload
			log.Printf("Uploading new local file: %s", relPath)
			sm.uploadFile(folder, relPath)
		} else if localHash == knownLocalHash {
			// Remote deletion - check if it was renamed before deleting
			if !sm.wasFileRenamed(folder, relPath, knownRemoteETag) {
				log.Printf("Remote deletion detected, deleting local: %s", relPath)
				sm.deleteLocalFile(folder, relPath)
			} else {
				log.Printf("File %s was renamed remotely, skipping deletion", relPath)
			}
		} else {
			// Conflict: remote deleted, local modified
			log.Printf("Conflict (remote deleted, local modified): %s", relPath)
			// Default to uploading local version
			sm.uploadFile(folder, relPath)
		}
		return
	}

	// Both exist - apply decision matrix
	if !localChanged && !remoteChanged {
		// No changes
		return
	}

	if localChanged && !remoteChanged {
		// Upload local → remote
		log.Printf("Uploading modified local file: %s", relPath)
		sm.uploadFile(folder, relPath)
		return
	}

	if !localChanged && remoteChanged {
		// Download remote → local
		log.Printf("Downloading modified remote file: %s", relPath)
		sm.downloadFile(folder, relPath, remoteFile)
		return
	}

	// Both changed - conflict
	log.Printf("Conflict detected for %s (local and remote both modified)", relPath)
	sm.handleConflict(folder, relPath, localPath, remoteFile)
}

// wasFileRenamed checks if a file with the given ETag exists at a different remote path
func (sm *SyncManager) wasFileRenamed(folder storage.SyncFolder, relPath string, oldETag string) bool {
	if oldETag == "" {
		return false
	}

	// List all remote files and check if any have the same ETag
	remoteFiles, _, err := sm.listRemoteFilesRecursive(folder.RemotePath)
	if err != nil {
		return false
	}

	for path, remoteFile := range remoteFiles {
		if path != relPath && remoteFile.ETag == oldETag {
			log.Printf("Found file %s with matching ETag %s... at remote path %s", relPath, oldETag[:min(8, len(oldETag))], path)
			return true
		}
	}

	return false
}

// handleRemoteRenames detects and handles remote file renames
// Before deleting a local file because it no longer exists remotely,
// check if it was renamed by looking for the same ETag at a different path
func (sm *SyncManager) handleRemoteRenames(folder storage.SyncFolder, localFiles map[string]bool, remoteFiles map[string]nextcloud.FileInfo) {
	log.Printf("Checking for remote renames in folder %s", folder.RemotePath)

	// Get all sync records for this folder to check ETags
	records, err := storage.GetSyncRecordsForFolder(folder.ID)
	if err != nil {
		log.Printf("Failed to get sync records for rename detection: %v", err)
		return
	}

	log.Printf("Found %d sync records for rename detection", len(records))

	// Build a map of ETag to sync record for files that exist locally but not remotely
	etagToOldPath := make(map[string]string)
	for _, record := range records {
		if record.Deleted || record.RemoteETag == "" {
			continue
		}
		// Check if local file exists but remote doesn't
		localPath := filepath.Join(folder.LocalPath, filepath.FromSlash(record.RelativePath))
		if _, err := os.Stat(localPath); err == nil {
			// Local file exists
			if _, remoteExists := remoteFiles[record.RelativePath]; !remoteExists {
				// But not remotely - might be a rename
				etagToOldPath[record.RemoteETag] = record.RelativePath
				log.Printf("Rename candidate: %s (ETag: %s...)", record.RelativePath, record.RemoteETag[:min(8, len(record.RemoteETag))])
			}
		}
	}

	log.Printf("Found %d rename candidates", len(etagToOldPath))

	// Now check if any remote file has an ETag matching a missing local file
	for remotePath, remoteFile := range remoteFiles {
		// Skip if this path already has a sync record (not a rename candidate)
		existingRecord, _ := storage.GetSyncRecord(folder.ID, remotePath)
		if existingRecord != nil && !existingRecord.Deleted {
			continue // Already tracked, not a rename
		}

		if remoteFile.ETag == "" {
			continue
		}

		// Check if this ETag matches a file that no longer exists remotely
		if oldPath, found := etagToOldPath[remoteFile.ETag]; found && oldPath != remotePath {
			log.Printf("ETag match found! Old: %s, New: %s, ETag: %s...", oldPath, remotePath, remoteFile.ETag[:min(8, len(remoteFile.ETag))])

			// Mark both paths as syncing to prevent filesystem watcher from triggering
			sm.markFileSyncing(oldPath, true)
			sm.markFileSyncing(remotePath, true)
			defer sm.markFileSyncing(oldPath, false)
			defer sm.markFileSyncing(remotePath, false)

			// Found a match! Rename local file instead of deleting/redownloading
			oldLocalPath := filepath.Join(folder.LocalPath, filepath.FromSlash(oldPath))
			newLocalPath := filepath.Join(folder.LocalPath, filepath.FromSlash(remotePath))

			// Ensure parent directory exists
			newDir := filepath.Dir(newLocalPath)
			if err := os.MkdirAll(newDir, 0755); err != nil {
				log.Printf("Failed to create directory for rename %s: %v", newDir, err)
				continue
			}

			// Perform the rename
			if err := os.Rename(oldLocalPath, newLocalPath); err != nil {
				log.Printf("Failed to rename %s to %s: %v", oldPath, remotePath, err)
				continue
			}

			// Update sync record: delete old, create new
			storage.SaveSyncRecord(folder.ID, oldPath, "", "", time.Now().Unix(), true)
			storage.SaveSyncRecord(folder.ID, remotePath, "", remoteFile.ETag, time.Now().Unix(), false)

			log.Printf("Remote rename detected: %s -> %s (ETag: %s)", oldPath, remotePath, remoteFile.ETag[:min(8, len(remoteFile.ETag))])

			// Remove from etag map so we don't process it again
			delete(etagToOldPath, remoteFile.ETag)
		}
	}
}

// getRemoteFileInfo retrieves file info from remote
func (sm *SyncManager) getRemoteFileInfo(folderRemotePath, relPath string) (nextcloud.FileInfo, bool) {
	files, _, err := sm.listRemoteFilesRecursive(folderRemotePath)
	if err != nil {
		return nextcloud.FileInfo{}, false
	}

	file, exists := files[relPath]
	return file, exists
}

// remotePathExists checks if a path (file or directory) exists remotely
func (sm *SyncManager) remotePathExists(folderRemotePath, relPath string) (isFile bool, isDir bool) {
	files, dirs, err := sm.listRemoteFilesRecursive(folderRemotePath)
	if err != nil {
		return false, false
	}

	// Check if it's a file
	if _, exists := files[relPath]; exists {
		return true, false
	}

	// Check if it's a directory
	if dirs[relPath] {
		return false, true
	}

	return false, false
}

// uploadFile uploads a local file to remote
func (sm *SyncManager) uploadFile(folder storage.SyncFolder, relPath string) {
	localPath := filepath.Join(folder.LocalPath, filepath.FromSlash(relPath))
	remotePath := folder.RemotePath + "/" + relPath

	// Mark file as syncing to ignore self-triggered filesystem events
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

	err = sm.client.UploadFile(remotePath, content)
	if err != nil {
		log.Printf("Failed to upload file %s: %v", remotePath, err)
		sm.eventChan <- SyncEvent{Path: relPath, Operation: "upload", Success: false, Error: err}
		return
	}

	// Get the remote file info to obtain the ETag
	remoteFile, remoteExists := sm.getRemoteFileInfo(folder.RemotePath, relPath)
	var remoteETag string
	if remoteExists && remoteFile.Name != "" {
		remoteETag = remoteFile.ETag
	}

	// Update sync record with both local hash and remote ETag
	info, _ := os.Stat(localPath)
	modTime := time.Now().Unix()
	if info != nil {
		modTime = info.ModTime().Unix()
	}

	err = storage.SaveSyncRecord(folder.ID, relPath, hash, remoteETag, modTime, false)
	if err != nil {
		log.Printf("Failed to save sync record for %s: %v", relPath, err)
	}

	log.Printf("Successfully uploaded: %s (local_hash=%s..., remote_etag=%s...)", relPath, hash[:min(8, len(hash))], remoteETag[:min(8, len(remoteETag))])
	sm.eventChan <- SyncEvent{Path: relPath, Operation: "upload", Success: true}
}

// downloadFile downloads a remote file to local
func (sm *SyncManager) downloadFile(folder storage.SyncFolder, relPath string, remoteFile nextcloud.FileInfo) {
	localPath := filepath.Join(folder.LocalPath, filepath.FromSlash(relPath))

	// Mark file as syncing to ignore self-triggered filesystem events
	sm.markFileSyncing(relPath, true)
	defer sm.markFileSyncing(relPath, false)

	content, err := sm.client.DownloadFile(folder.RemotePath + "/" + relPath)
	if err != nil {
		log.Printf("Failed to download file %s: %v", relPath, err)
		sm.eventChan <- SyncEvent{Path: relPath, Operation: "download", Success: false, Error: err}
		return
	}

	hash := computeHashForContent(content)

	// Ensure parent directory exists locally
	dir := filepath.Dir(localPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("Failed to create directory %s: %v", dir, err)
		sm.eventChan <- SyncEvent{Path: relPath, Operation: "download", Success: false, Error: err}
		return
	}

	err = os.WriteFile(localPath, content, 0644)
	if err != nil {
		log.Printf("Failed to write local file %s: %v", localPath, err)
		sm.eventChan <- SyncEvent{Path: relPath, Operation: "download", Success: false, Error: err}
		return
	}

	// Update sync record with both local hash and remote ETag
	err = storage.SaveSyncRecord(folder.ID, relPath, hash, remoteFile.ETag, time.Now().Unix(), false)
	if err != nil {
		log.Printf("Failed to save sync record for %s: %v", relPath, err)
	}

	log.Printf("Successfully downloaded: %s (local_hash=%s..., remote_etag=%s...)", relPath, hash[:min(8, len(hash))], remoteFile.ETag[:min(8, len(remoteFile.ETag))])
	sm.eventChan <- SyncEvent{Path: relPath, Operation: "download", Success: true}
}

// deleteLocalFile deletes a local file
func (sm *SyncManager) deleteLocalFile(folder storage.SyncFolder, relPath string) {
	localPath := filepath.Join(folder.LocalPath, filepath.FromSlash(relPath))

	// Mark as syncing to prevent filesystem watcher from triggering
	sm.markFileSyncing(relPath, true)
	defer sm.markFileSyncing(relPath, false)

	err := os.Remove(localPath)
	if err != nil && !os.IsNotExist(err) {
		log.Printf("Failed to delete local file %s: %v", localPath, err)
		sm.eventChan <- SyncEvent{Path: relPath, Operation: "delete_local", Success: false, Error: err}
		return
	}

	// Update sync record as tombstone
	err = storage.SaveSyncRecord(folder.ID, relPath, "", "", time.Now().Unix(), true)
	if err != nil {
		log.Printf("Failed to save sync record for %s: %v", relPath, err)
	}

	log.Printf("Deleted local file: %s", relPath)
	sm.eventChan <- SyncEvent{Path: relPath, Operation: "delete_local", Success: true}
}

// createRemoteDir creates a directory on the remote server
func (sm *SyncManager) createRemoteDir(folder storage.SyncFolder, relPath string) {
	remotePath := folder.RemotePath + "/" + relPath
	log.Printf("Creating remote directory: %s", relPath)

	err := sm.client.MkdirAll(remotePath)
	if err != nil {
		log.Printf("Failed to create remote directory %s: %v", remotePath, err)
		return
	}

	log.Printf("Successfully created remote directory: %s", relPath)
}

// cleanupEmptyDirectories removes local directories that are empty and don't exist remotely
func (sm *SyncManager) cleanupEmptyDirectories(folder storage.SyncFolder, localDirs, remoteDirs map[string]bool) {
	// Sort directories by depth (deepest first) so we delete children before parents
	type dirInfo struct {
		path  string
		depth int
	}
	var dirs []dirInfo
	for dir := range localDirs {
		// Check if directory exists remotely
		if remoteDirs[dir] {
			continue
		}
		depth := strings.Count(dir, "/")
		dirs = append(dirs, dirInfo{path: dir, depth: depth})
	}

	// Sort by depth descending
	for i := 0; i < len(dirs); i++ {
		for j := i + 1; j < len(dirs); j++ {
			if dirs[j].depth > dirs[i].depth {
				dirs[i], dirs[j] = dirs[j], dirs[i]
			}
		}
	}

	// Try to delete empty directories
	for _, dirInfo := range dirs {
		dirPath := filepath.Join(folder.LocalPath, filepath.FromSlash(dirInfo.path))

		// Check if directory is empty
		entries, err := os.ReadDir(dirPath)
		if err != nil {
			continue
		}

		// Only delete if empty
		if len(entries) == 0 {
			if err := os.Remove(dirPath); err == nil {
				log.Printf("Deleted empty local directory: %s", dirInfo.path)
				// Mark as syncing to prevent filesystem watcher issues
				sm.markFileSyncing(dirInfo.path, true)
				defer sm.markFileSyncing(dirInfo.path, false)
			}
		}
	}
}

// deleteRemoteFile deletes a remote file
func (sm *SyncManager) deleteRemoteFile(folder storage.SyncFolder, relPath string) {
	remotePath := folder.RemotePath + "/" + relPath

	err := sm.client.DeleteFile(remotePath)
	if err != nil {
		log.Printf("Failed to delete remote file %s: %v", remotePath, err)
		sm.eventChan <- SyncEvent{Path: relPath, Operation: "delete_remote", Success: false, Error: err}
		return
	}

	// Update sync record as tombstone
	err = storage.SaveSyncRecord(folder.ID, relPath, "", "", time.Now().Unix(), true)
	if err != nil {
		log.Printf("Failed to save sync record for %s: %v", relPath, err)
	}

	log.Printf("Deleted remote file: %s", relPath)
	sm.eventChan <- SyncEvent{Path: relPath, Operation: "delete_remote", Success: true}
}

// deleteRemoteDirectory deletes a remote directory recursively with a single WebDAV call
func (sm *SyncManager) deleteRemoteDirectory(folder storage.SyncFolder, relPath string) {
	remotePath := folder.RemotePath + "/" + relPath

	// Mark as syncing to prevent filesystem watcher issues
	sm.markFileSyncing(relPath, true)
	defer sm.markFileSyncing(relPath, false)

	log.Printf("Deleting remote directory recursively: %s", relPath)

	// WebDAV DELETE on a directory recursively deletes all contents
	err := sm.client.DeleteFile(remotePath)
	if err != nil {
		log.Printf("Failed to delete remote directory %s: %v", remotePath, err)
		sm.eventChan <- SyncEvent{Path: relPath, Operation: "delete_remote", Success: false, Error: err}
		return
	}

	// Update sync record as tombstone
	err = storage.SaveSyncRecord(folder.ID, relPath, "", "", time.Now().Unix(), true)
	if err != nil {
		log.Printf("Failed to save sync record for %s: %v", relPath, err)
	}

	log.Printf("Deleted remote directory: %s", relPath)
	sm.eventChan <- SyncEvent{Path: relPath, Operation: "delete_remote", Success: true}
}

// handleConflict handles sync conflicts
func (sm *SyncManager) handleConflict(folder storage.SyncFolder, relPath, localPath string, remoteFile nextcloud.FileInfo) {
	// For now, use "last writer wins" strategy with remote priority
	// TODO: Implement proper conflict resolution UI
	log.Printf("Resolving conflict by downloading remote version: %s", relPath)
	sm.downloadFile(folder, relPath, remoteFile)
	sm.eventChan <- SyncEvent{Path: relPath, Operation: "conflict", Success: true}
}

// handleLocalDelete handles a file or directory deletion detected by the filesystem watcher
func (sm *SyncManager) handleLocalDelete(folder storage.SyncFolder, relPath string) {
	log.Printf("Handling local deletion for: %s", relPath)

	// Check if remote path exists (file or directory)
	isRemoteFile, isRemoteDir := sm.remotePathExists(folder.RemotePath, relPath)
	remoteExists := isRemoteFile || isRemoteDir

	if !remoteExists {
		log.Printf("Path %s deleted locally and doesn't exist remotely - nothing to do", relPath)
		return
	}

	// Path exists remotely - check sync record
	syncRecord, err := storage.GetSyncRecord(folder.ID, relPath)
	if err != nil {
		log.Printf("Failed to get sync record for %s: %v", relPath, err)
		return
	}

	// Check if this was a previously synced item
	wasSynced := syncRecord != nil && !syncRecord.Deleted

	if isRemoteDir {
		// Handle directory deletion - just do a recursive delete
		log.Printf("Propagating local directory deletion to remote: %s", relPath)
		sm.deleteRemoteDirectory(folder, relPath)
	} else {
		// Handle file deletion
		if wasSynced {
			log.Printf("Propagating local file deletion to remote: %s", relPath)
			sm.deleteRemoteFile(folder, relPath)
		} else {
			log.Printf("Remote file %s exists but was not synced - creating tombstone", relPath)
			storage.SaveSyncRecord(folder.ID, relPath, "", "", time.Now().Unix(), true)
		}
	}
}

// detectLocalRenames checks if any of the pending changes represent a local rename
// Returns a map of oldPath -> newPath for detected renames
func (sm *SyncManager) detectLocalRenames(folder storage.SyncFolder, changes []pendingOp) map[string]string {
	renames := make(map[string]string)

	// Look for patterns: a delete/rename followed by a create
	var deleteOps []pendingOp
	var createOps []pendingOp

	for _, op := range changes {
		if op.isDelete || op.isRename {
			deleteOps = append(deleteOps, op)
		}
		if op.isCreate {
			createOps = append(createOps, op)
		}
	}

	// For each deleted file, check if there's a new file with the same content hash
	for _, delOp := range deleteOps {
		syncRecord, err := storage.GetSyncRecord(folder.ID, delOp.path)
		if err != nil || syncRecord == nil || syncRecord.Deleted {
			continue
		}

		// Check if any newly created file has the same hash
		for _, createOp := range createOps {
			// Skip if already mapped
			if _, exists := renames[delOp.path]; exists {
				continue
			}

			// Check if new file exists and has same hash
			newLocalPath := filepath.Join(folder.LocalPath, filepath.FromSlash(createOp.path))
			if _, err := os.Stat(newLocalPath); err != nil {
				continue
			}

			newHash, _, _ := getLocalFileInfo(newLocalPath)
			if newHash == syncRecord.LocalHash && newHash != "" {
				// Found a match! This is a rename
				renames[delOp.path] = createOp.path
				log.Printf("Detected local rename: %s -> %s (hash: %s...)",
					delOp.path, createOp.path, newHash[:min(8, len(newHash))])
				break
			}
		}
	}

	return renames
}

// handleLocalRename handles a local file rename by renaming the file on remote
func (sm *SyncManager) handleLocalRename(folder storage.SyncFolder, oldPath, newPath string) {
	log.Printf("Handling local rename: %s -> %s", oldPath, newPath)

	// Get sync record for old path
	syncRecord, err := storage.GetSyncRecord(folder.ID, oldPath)
	if err != nil || syncRecord == nil {
		log.Printf("No sync record found for old path %s, treating as separate operations", oldPath)
		return
	}

	// Check if remote file exists at old path
	_, remoteExists := sm.getRemoteFileInfo(folder.RemotePath, oldPath)
	if !remoteExists {
		log.Printf("Remote file %s doesn't exist, treating as separate operations", oldPath)
		return
	}

	// Perform remote rename using WebDAV MOVE
	oldRemotePath := folder.RemotePath + "/" + oldPath
	newRemotePath := folder.RemotePath + "/" + newPath

	// Move on remote
	if err := sm.client.MoveFile(oldRemotePath, newRemotePath); err != nil {
		log.Printf("Failed to rename remote file %s to %s: %v", oldPath, newPath, err)
		// Fall back to separate operations
		return
	}

	// Update sync records
	storage.SaveSyncRecord(folder.ID, oldPath, "", "", time.Now().Unix(), true)
	storage.SaveSyncRecord(folder.ID, newPath, syncRecord.LocalHash, syncRecord.RemoteETag, time.Now().Unix(), false)

	log.Printf("Successfully renamed remote: %s -> %s", oldPath, newPath)
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

	// Walk directory and add all subdirectories to watcher
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

	// Process events
	go func() {
		debounceTimer := time.NewTimer(0)
		<-debounceTimer.C

		var pendingChanges []pendingOp
		var mux sync.Mutex

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
					info, err := os.Stat(event.Name)
					if err == nil && info.IsDir() {
						watcher.Add(event.Name)
					}
				}

				if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
					relPath, err := filepath.Rel(folder.LocalPath, event.Name)
					if err == nil && !isIgnoredFile(relPath) {
						// Check if it's a directory
						info, err := os.Stat(event.Name)
						if err == nil && info.IsDir() {
							// Handle directory creation separately
							if event.Op&fsnotify.Create == fsnotify.Create {
								watcher.Add(event.Name)
								log.Printf("Added new directory to watcher: %s", relPath)
								// Create directory on remote
								go sm.createRemoteDir(folder, relPath)
							}
							continue
						}

						// Skip files that are currently being synced by us
						if sm.isFileSyncing(filepath.ToSlash(relPath)) {
							log.Printf("Skipping self-triggered change for: %s", relPath)
							continue
						}

						mux.Lock()
						op := pendingOp{path: filepath.ToSlash(relPath)}
						if event.Op&fsnotify.Remove == fsnotify.Remove {
							op.isDelete = true
							log.Printf("Detected local file deletion: %s", relPath)
						}
						if event.Op&fsnotify.Rename == fsnotify.Rename {
							op.isRename = true
							log.Printf("Detected local file rename: %s", relPath)
						}
						if event.Op&fsnotify.Create == fsnotify.Create {
							op.isCreate = true
							log.Printf("Detected local file create: %s", relPath)
						}
						pendingChanges = append(pendingChanges, op)
						mux.Unlock()
						debounceTimer.Reset(100 * time.Millisecond)
					}
				}

			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Printf("Watcher error: %v", err)

			case <-debounceTimer.C:
				mux.Lock()
				changes := make([]pendingOp, len(pendingChanges))
				copy(changes, pendingChanges)
				pendingChanges = pendingChanges[:0]
				mux.Unlock()

				if len(changes) > 0 {
					log.Printf("Processing %d local changes", len(changes))

					// First pass: detect local renames
					renameMap := sm.detectLocalRenames(folder, changes)

					// Process renames first
					for oldPath, newPath := range renameMap {
						sm.handleLocalRename(folder, oldPath, newPath)
					}

					// Build a set of all deleted paths and identify directories
					deletedPaths := make(map[string]bool)
					for _, op := range changes {
						if op.isDelete {
							deletedPaths[op.path] = true
						}
					}

					// Identify which deleted paths are directories vs files
					// A path is a directory if:
					// 1. It has no "/" in it (top-level), OR
					// 2. It's a parent of another deleted path
					deletedDirs := make(map[string]bool)
					for path := range deletedPaths {
						// Check if this path is a parent of any other deleted path
						isDir := false
						for otherPath := range deletedPaths {
							if otherPath != path && strings.HasPrefix(otherPath, path+"/") {
								isDir = true
								break
							}
						}
						// Also check if it's a directory by trying to stat it (might fail if already deleted)
						if !isDir {
							fullPath := filepath.Join(folder.LocalPath, filepath.FromSlash(path))
							if info, err := os.Stat(fullPath); err == nil && info.IsDir() {
								isDir = true
							}
						}
						if isDir {
							deletedDirs[path] = true
						}
					}

					// Sort changes: process directory deletions first, then files
					// Skip files that are inside a deleted directory
					var dirDeletions, fileDeletions, otherChanges []pendingOp
					for _, op := range changes {
						// Skip if this was part of a rename
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

						// Check if this path is inside a deleted directory
						insideDeletedDir := false
						for dirPath := range deletedDirs {
							if strings.HasPrefix(op.path, dirPath+"/") {
								insideDeletedDir = true
								log.Printf("Skipping %s as it's inside deleted directory %s", op.path, dirPath)
								break
							}
						}
						if insideDeletedDir {
							continue
						}

						if op.isDelete {
							if deletedDirs[op.path] {
								dirDeletions = append(dirDeletions, op)
							} else {
								fileDeletions = append(fileDeletions, op)
							}
						} else {
							otherChanges = append(otherChanges, op)
						}
					}

					// Process directory deletions first (depth-first, deepest first)
					for i := 0; i < len(dirDeletions); i++ {
						for j := i + 1; j < len(dirDeletions); j++ {
							if strings.Count(dirDeletions[j].path, "/") > strings.Count(dirDeletions[i].path, "/") {
								dirDeletions[i], dirDeletions[j] = dirDeletions[j], dirDeletions[i]
							}
						}
					}

					for _, op := range dirDeletions {
						log.Printf("Processing directory deletion: %s", op.path)
						sm.handleLocalDelete(folder, op.path)
					}

					// Process file deletions
					for _, op := range fileDeletions {
						sm.handleLocalDelete(folder, op.path)
					}

					// Process other changes (creates, writes)
					for _, op := range otherChanges {
						sm.syncFile(folder, op.path)
					}
				}
			}
		}
	}()
}

// remotePollingLoop polls remote for changes every 60 seconds
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
	ticker := time.NewTicker(24 * time.Hour) // Run once per day
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
	// Ignore hidden files and common temp files
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

// IsRunning returns whether the sync manager is running
func (sm *SyncManager) IsRunning() bool {
	sm.mux.RLock()
	defer sm.mux.RUnlock()
	return sm.isRunning
}

// SyncFolderStats holds statistics for a folder sync operation
type SyncFolderStats struct {
	FolderID        int64
	RemotePath      string
	LocalPath       string
	FilesSynced     int
	FilesUploaded   int
	FilesDownloaded int
	Conflicts       int
	Errors          int
}

// GetFolderStats returns sync statistics for a folder
func (sm *SyncManager) GetFolderStats(folderID int64) (*SyncFolderStats, error) {
	// This is a placeholder - in a real implementation, you'd track these stats
	return &SyncFolderStats{
		FolderID: folderID,
	}, fmt.Errorf("not implemented")
}
