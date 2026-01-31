package daemon

import (
	"fmt"
	"log"
	"nextcloud-gtk/internal/ipc"
	"nextcloud-gtk/internal/nextcloud"
	"nextcloud-gtk/storage"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
)

var debugMode bool
var syncManager *SyncManager

func SetDebugMode(enabled bool) {
	debugMode = enabled
	log.Printf("Daemon Debug Mode: %v", debugMode)
}

func Start(app *gio.Application) {
	log.Println("Daemon starting...")

	// Initialize sync manager
	url, _ := storage.GetSetting("server_url")
	user, _ := storage.GetSetting("username")
	pass, _ := storage.GetSetting("password")

	if url != "" && user != "" && pass != "" {
		client := nextcloud.NewClient(url, user, pass)
		syncManager = NewSyncManager(client)

		// Start sync manager
		syncManager.Start()

		// Listen for sync events and forward to UI
		go func() {
			eventChan := syncManager.GetEventChannel()
			for event := range eventChan {
				if event.Success {
					log.Printf("Sync event: %s %s", event.Operation, event.Path)
					// Notify UI of the change
					ipc.SendSignal("file_changed:" + event.Path)
				} else if event.Error != nil {
					log.Printf("Sync error for %s: %v", event.Path, event.Error)
				}
			}
		}()
	}

	// Start IPC listener for real-time triggers from UI
	err := ipc.StartListener(func(msg string) {
		log.Printf("Received IPC signal: %s", msg)
		handleIPCMessage(msg)
	})
	if err != nil {
		log.Printf("Failed to start IPC listener: %v", err)
	}
}

func handleIPCMessage(msg string) {
	switch msg {
	case "syncNow":
		if syncManager != nil {
			log.Println("Manual sync triggered via IPC")
			syncManager.SyncAllFolders()
		}
	case "stop":
		if syncManager != nil {
			log.Println("Stopping sync manager via IPC")
			syncManager.Stop()
		}
	default:
		if len(msg) > 13 && msg[:13] == "sync_folder:" {
			// Trigger sync for specific folder
			folderID := msg[13:]
			log.Printf("Sync triggered for folder: %s", folderID)
			// Parse folder ID and trigger sync
		} else if len(msg) > 14 && msg[:14] == "stop_watch_id:" {
			// Stop watching a specific folder by ID
			var folderID int64
			if _, err := fmt.Sscanf(msg[14:], "%d", &folderID); err == nil {
				if syncManager != nil {
					log.Printf("Stopping watcher for folder ID: %d", folderID)
					syncManager.StopWatchingFolder(folderID)
				}
			}
		}
	}
}

func Stop() {
	if syncManager != nil {
		syncManager.Stop()
	}
}

func IsSyncing() bool {
	if syncManager == nil {
		return false
	}
	return syncManager.IsRunning()
}

// TriggerSyncForFolder triggers a sync for a specific folder by ID
func TriggerSyncForFolder(folderID int64) {
	if syncManager != nil {
		syncManager.TriggerSyncForFolder(folderID)
	}
}
