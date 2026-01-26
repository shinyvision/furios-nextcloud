package daemon

import (
	"log"
	"nextcloud-gtk/internal/ipc"
	"nextcloud-gtk/storage"
	"time"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
)

var debugMode bool

func SetDebugMode(enabled bool) {
	debugMode = enabled
	log.Printf("Daemon Debug Mode: %v", debugMode)
}

func Start(app *gio.Application) {
	log.Println("Daemon starting...")
	
	// Start IPC listener for real-time triggers from UI
	err := ipc.StartListener(func(msg string) {
		log.Printf("Received IPC signal: %s", msg)
		if msg == "syncNow" {
			syncFiles()
		}
	})
	if err != nil {
		log.Printf("Failed to start IPC listener: %v", err)
	}

	// Background sync loop
	go func() {
		for {
			syncFiles()
			time.Sleep(30 * time.Second) // Check for changes every 30 seconds
		}
	}()
}

func syncFiles() {
	user, _ := storage.GetSetting("username")
	if user == "" {
		return
	}
	
	if debugMode {
		log.Printf("Checking for file changes for user: %s", user)
	}
	
	folders, err := storage.GetSyncFolders()
	if err != nil {
		log.Printf("Error getting sync folders: %v", err)
		return
	}

	for _, f := range folders {
		if debugMode {
			log.Printf("Syncing %s <-> %s", f.RemotePath, f.LocalPath)
		}
		// TODO: Implement actual WebDAV <-> Local FS sync logic
	}
}
