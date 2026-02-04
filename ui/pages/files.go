package pages

import (
	"fmt"
	"log"
	"math"
	"nextcloud-gtk/internal/ipc"
	"nextcloud-gtk/internal/nextcloud"
	"nextcloud-gtk/storage"
	"nextcloud-gtk/ui/components"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
)

// truncateFileName truncates a filename to maxLen characters, adding an ellipsis
// and the file extension. Example: "my_very_long_file_name.png" -> "my_very_long_file_na…png"
func truncateFileName(name string, maxLen int) string {
	runes := []rune(name)
	if len(runes) <= maxLen {
		return name
	}

	ext := filepath.Ext(name)
	truncated := string(runes[:maxLen])

	if len(ext) > 1 {
		return truncated + "…" + ext[1:] // ext[1:] removes the leading dot
	}
	return truncated + "…"
}

func NewFilesPage(parentOverlay *gtk.Overlay, showPage func(string), openMenu func(), setBackHandler func(func(), bool), plusBtn *gtk.Button, updateBreadcrumb func(string)) (*gtk.Box, func(string)) {
	box := gtk.NewBox(gtk.OrientationVertical, 0)
	box.AddCSSClass("files-container")

	// State variables
	var currentPath = "/"
	var grid *gtk.FlowBox
	var refreshFiles func(string)
	var spinner *gtk.Box

	// Track folder items and their sync status
	type FolderItem struct {
		widget     *gtk.Box
		iconWidget gtk.Widgetter
		isSynced   bool
		folderPath string
	}
	folderItems := make(map[string]*FolderItem)

	// Track all items in the grid for in-place insertion
	type GridItem struct {
		name  string
		isDir bool
	}
	var gridItems []GridItem

	// Track if we're inside a synced folder (subfolders shouldn't show sync option)
	var isInsideSyncedFolder bool

	// Grid for files
	grid = gtk.NewFlowBox()
	grid.SetSelectionMode(gtk.SelectionNone)
	grid.SetVAlign(gtk.AlignStart)
	grid.SetHAlign(gtk.AlignFill)
	grid.SetHomogeneous(true)
	grid.SetMinChildrenPerLine(1)
	grid.SetMaxChildrenPerLine(20)
	grid.SetColumnSpacing(10)
	grid.SetRowSpacing(10)

	scrolled := gtk.NewScrolledWindow()
	scrolled.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scrolled.SetVExpand(true)
	scrolled.SetChild(grid)

	// Spinner overlay
	overlay := gtk.NewOverlay()
	overlay.SetChild(scrolled)
	overlay.SetVExpand(true)

	spinner = components.NewSpinner()
	spinner.SetVisible(false)
	overlay.AddOverlay(spinner)

	box.Append(overlay)

	folderPath := "assets/icons/ui/folder.svg"
	if _, err := os.Stat(folderPath); os.IsNotExist(err) {
		folderPath = "/app/share/nextcloud-gtk/assets/icons/ui/folder.svg"
	}

	cloudPath := "assets/icons/ui/cloud.svg"
	if _, err := os.Stat(cloudPath); os.IsNotExist(err) {
		cloudPath = "/app/share/nextcloud-gtk/assets/icons/ui/cloud.svg"
	}

	// Helper function to update sync indicator for a folder
	updateSyncIndicator := func(item *FolderItem, isSynced bool) {
		if item.isSynced == isSynced {
			return // No change needed
		}
		item.isSynced = isSynced

		// Remove current icon widget
		item.widget.Remove(item.iconWidget)

		if isSynced {
			// Create overlay with sync indicator
			overlayContainer := gtk.NewOverlay()
			overlayContainer.SetSizeRequest(48, 48)

			icon := gtk.NewImageFromFile(folderPath)
			icon.AddCSSClass("folder-icon")
			icon.SetPixelSize(48)
			overlayContainer.SetChild(icon)

			bubble := gtk.NewBox(gtk.OrientationHorizontal, 0)
			bubble.AddCSSClass("sync-bubble")
			bubble.SetHAlign(gtk.AlignEnd)
			bubble.SetVAlign(gtk.AlignStart)
			bubble.SetSizeRequest(14, 14)

			overlayContainer.AddOverlay(bubble)
			item.widget.Prepend(overlayContainer)
			item.iconWidget = overlayContainer
		} else {
			// Simple folder icon without indicator
			icon := gtk.NewImageFromFile(folderPath)
			icon.AddCSSClass("folder-icon")
			icon.SetPixelSize(48)
			item.widget.Prepend(icon)
			item.iconWidget = icon
		}
	}

	navigateUp := func() {
		if currentPath == "/" {
			return
		}
		path := strings.TrimSuffix(currentPath, "/")
		lastSlash := strings.LastIndex(path, "/")
		if lastSlash <= 0 {
			refreshFiles("/")
		} else {
			refreshFiles(path[:lastSlash])
		}
	}

	// Forward declaration for createFileItemWidget
	var createFileItemWidget func(name string, isDir bool, path string, syncedSet map[string]bool) *gtk.Box

	// createFileItemWidget creates a file or folder widget with all gesture handlers
	createFileItemWidget = func(name string, isDir bool, path string, syncedSet map[string]bool) *gtk.Box {
		fileItem := gtk.NewBox(gtk.OrientationVertical, 5)
		fileItem.SetSizeRequest(80, 100)

		var iconWidget gtk.Widgetter
		if isDir {
			// Build full path for this folder
			var folderFullPath string
			if path == "/" {
				folderFullPath = "/" + name
			} else {
				folderFullPath = path + "/" + name
			}

			// Check if this folder is synced
			isSynced := syncedSet[folderFullPath]

			if isSynced {
				// Create an overlay for synced indicator
				overlayContainer := gtk.NewOverlay()
				overlayContainer.SetSizeRequest(48, 48)

				icon := gtk.NewImageFromFile(folderPath)
				icon.AddCSSClass("folder-icon")
				icon.SetPixelSize(48)
				overlayContainer.SetChild(icon)

				bubble := gtk.NewBox(gtk.OrientationHorizontal, 0)
				bubble.AddCSSClass("sync-bubble")
				bubble.SetHAlign(gtk.AlignEnd)
				bubble.SetVAlign(gtk.AlignStart)
				bubble.SetSizeRequest(14, 14)

				overlayContainer.AddOverlay(bubble)
				fileItem.Append(overlayContainer)
				iconWidget = overlayContainer
			} else {
				icon := gtk.NewImageFromFile(folderPath)
				icon.AddCSSClass("folder-icon")
				icon.SetPixelSize(48)
				fileItem.Append(icon)
				iconWidget = icon
			}

			// Store folder item reference
			folderItems[folderFullPath] = &FolderItem{
				widget:     fileItem,
				iconWidget: iconWidget,
				isSynced:   isSynced,
				folderPath: folderFullPath,
			}

			// Make folder clickable with touch-friendly interaction
			fileItem.AddCSSClass("clickable-folder")
			folderName := name
			pressed := false

			// Drag gesture for press/release navigation with threshold
			dragGesture := gtk.NewGestureDrag()
			dragGesture.SetButton(uint(gdk.BUTTON_PRIMARY))
			dragGesture.Connect("drag-begin", func(startX, startY float64) {
				pressed = true
				fileItem.AddCSSClass("folder-pressed")
			})

			dragGesture.Connect("drag-update", func(offsetX, offsetY float64) {
				if !pressed {
					return
				}
				dist := math.Hypot(offsetX, offsetY)
				if dist > 45 {
					pressed = false
					fileItem.RemoveCSSClass("folder-pressed")
				}
			})

			dragGesture.Connect("drag-end", func(offsetX, offsetY float64) {
				fileItem.RemoveCSSClass("folder-pressed")
				if !pressed {
					return
				}
				pressed = false

				dist := math.Hypot(offsetX, offsetY)
				if dist > 45 {
					return
				}

				var newPath string
				if path == "/" {
					newPath = "/" + folderName
				} else {
					newPath = path + "/" + folderName
				}
				refreshFiles(newPath)
			})

			fileItem.AddController(dragGesture)

			// Long press for context menu
			longPress := gtk.NewGestureLongPress()
			longPress.SetTouchOnly(false)
			longPress.Connect("pressed", func(x, y float64) {
				pressed = false
				fileItem.RemoveCSSClass("folder-pressed")

				// Create a single modal - reuse it for all content
				modal := components.NewModal(parentOverlay)

				// Forward declare for mutual recursion
				var showFolderContent func()
				var showDeleteConfirmation func()

				// Build delete confirmation content
				showDeleteConfirmation = func() {
					confirmContent := gtk.NewBox(gtk.OrientationVertical, 10)

					confirmTitle := gtk.NewLabel("Are you sure?")
					confirmTitle.AddCSSClass("modal-title")
					confirmContent.Append(confirmTitle)

					confirmMsg := gtk.NewLabel("Deleting this folder will also delete everything inside.")
					confirmMsg.AddCSSClass("modal-message")
					confirmMsg.SetWrap(true)
					confirmMsg.SetMarginBottom(10)
					confirmContent.Append(confirmMsg)

					buttonBox := gtk.NewBox(gtk.OrientationHorizontal, 10)
					buttonBox.SetHomogeneous(true)

					cancelBtn := gtk.NewButton()
					cancelBtn.SetChild(gtk.NewLabel("Cancel"))
					cancelBtn.AddCSSClass("modal-button")
					cancelBtn.AddCSSClass("modal-btn-secondary")
					cancelBtn.ConnectClicked(func() {
						showFolderContent()
					})

					deleteBtn := gtk.NewButton()
					deleteBtn.SetChild(gtk.NewLabel("Delete"))
					deleteBtn.AddCSSClass("modal-button")
					deleteBtn.AddCSSClass("modal-btn-danger")
					deleteBtn.ConnectClicked(func() {
						modal.Hide()
						go func() {
							// Check if this folder is currently synced
							if syncFolder, err := storage.GetSyncFolderByRemotePath(folderFullPath); err == nil && syncFolder != nil {
								log.Printf("Deleting synced folder, removing sync config: %s", folderFullPath)
								// Stop the watcher via IPC
								ipc.SendSignal(fmt.Sprintf("stop_watch_id:%d", syncFolder.ID))
								// Remove from database
								if err := storage.RemoveSyncFolder(folderFullPath); err != nil {
									log.Printf("Failed to remove sync folder: %v", err)
								}
							}

							url, _ := storage.GetSetting("server_url")
							user, _ := storage.GetSetting("username")
							pass, _ := storage.GetSetting("password")
							client := nextcloud.NewClient(url, user, pass)
							err := client.DeleteFile(folderFullPath)
							if err != nil {
								log.Printf("Failed to delete folder: %v", err)
								return
							}
							log.Printf("Deleted folder: %s", folderFullPath)
							glib.IdleAdd(func() {
								if fi := folderItems[folderFullPath]; fi != nil {
									grid.Remove(fi.widget.Parent())
									delete(folderItems, folderFullPath)
								}
							})
						}()
					})

					buttonBox.Append(cancelBtn)
					buttonBox.Append(deleteBtn)
					confirmContent.Append(buttonBox)

					modal.SetContent(confirmContent)
				}

				// Build folder options content
				showFolderContent = func() {
					content := gtk.NewBox(gtk.OrientationVertical, 5)

					title := gtk.NewLabel(folderName)
					title.AddCSSClass("modal-title")
					title.SetEllipsize(pango.EllipsizeEnd)
					title.SetMarginBottom(10)
					content.Append(title)

					folderItem := folderItems[folderFullPath]
					isSynced := folderItem != nil && folderItem.isSynced

					createBtn := func(label string, cssClass string, closeOnAction bool, action func()) {
						btn := gtk.NewButton()
						lbl := gtk.NewLabel(label)
						btn.SetChild(lbl)
						btn.AddCSSClass("modal-button")
						btn.AddCSSClass(cssClass)
						btn.SetHAlign(gtk.AlignFill)
						btn.ConnectClicked(func() {
							if closeOnAction {
								modal.Hide()
							}
							if action != nil {
								action()
							}
						})
						content.Append(btn)
					}

					if isSynced {
						createBtn("Stop sync", "modal-btn-secondary", true, func() {
							err := storage.RemoveSyncFolder(folderFullPath)
							if err != nil {
								log.Printf("Failed to remove sync folder mapping: %v", err)
							} else {
								log.Printf("Removed sync mapping for: %s", folderFullPath)
								if folderItem != nil {
									updateSyncIndicator(folderItem, false)
								}
							}
						})
					} else if !isInsideSyncedFolder {
						// Only show sync option if we're not inside a synced folder
						createBtn("Sync to filesystem", "modal-btn-secondary", false, func() {
							// Check if there are synced subfolders
							allSyncedFolders, _ := storage.GetSyncFolders()
							type subfolderInfo struct {
								remotePath string
								id         int64
							}
							var syncedSubfolders []subfolderInfo
							for _, sf := range allSyncedFolders {
								if strings.HasPrefix(sf.RemotePath, folderFullPath+"/") {
									syncedSubfolders = append(syncedSubfolders, subfolderInfo{
										remotePath: sf.RemotePath,
										id:         sf.ID,
									})
								}
							}

							// Function to open file chooser and perform sync
							openFileChooser := func() {
								dialog := gtk.NewFileChooserNative(
									"Select Local Directory",
									nil,
									gtk.FileChooserActionSelectFolder,
									"_Select",
									"_Cancel",
								)

								dialog.ConnectResponse(func(responseId int) {
									if responseId == int(gtk.ResponseAccept) {
										file := dialog.File()
										if file != nil {
											localPath := file.Path()
											// Remove synced subfolders first
											for _, sub := range syncedSubfolders {
												// Stop the watcher before removing from database
												ipc.SendSignal(fmt.Sprintf("stop_watch_id:%d", sub.id))
												storage.RemoveSyncFolder(sub.remotePath)
												log.Printf("Removed subfolder sync: %s (id=%d)", sub.remotePath, sub.id)
											}
											// Add the new sync
											err := storage.AddSyncFolder(folderFullPath, localPath)
											if err != nil {
												log.Printf("Failed to save sync folder mapping: %v", err)
											} else {
												log.Printf("Saved sync mapping: %s -> %s", folderFullPath, localPath)
												if folderItem != nil {
													updateSyncIndicator(folderItem, true)
												}
											}
										}
									}
									dialog.Destroy()
									modal.Hide()
								})

								dialog.Show()
							}

							// If there are synced subfolders, show warning first
							if len(syncedSubfolders) > 0 {
								warningContent := gtk.NewBox(gtk.OrientationVertical, 10)

								warningTitle := gtk.NewLabel("Warning")
								warningTitle.AddCSSClass("modal-title")
								warningContent.Append(warningTitle)

								warningMsg := gtk.NewLabel("This folder contains subfolders that are already synced.\nSyncing this folder will cause the subfolders to be detached.")
								warningMsg.AddCSSClass("modal-message")
								warningMsg.SetWrap(true)
								warningMsg.SetMarginBottom(10)
								warningContent.Append(warningMsg)

								buttonBox := gtk.NewBox(gtk.OrientationHorizontal, 10)
								buttonBox.SetHomogeneous(true)

								cancelBtn := gtk.NewButton()
								cancelBtn.SetChild(gtk.NewLabel("Cancel"))
								cancelBtn.AddCSSClass("modal-button")
								cancelBtn.AddCSSClass("modal-btn-secondary")
								cancelBtn.ConnectClicked(func() {
									showFolderContent()
								})

								proceedBtn := gtk.NewButton()
								proceedBtn.SetChild(gtk.NewLabel("Proceed"))
								proceedBtn.AddCSSClass("modal-button")
								proceedBtn.AddCSSClass("modal-btn-primary")
								proceedBtn.ConnectClicked(func() {
									openFileChooser()
								})

								buttonBox.Append(cancelBtn)
								buttonBox.Append(proceedBtn)
								warningContent.Append(buttonBox)

								modal.SetContent(warningContent)
							} else {
								openFileChooser()
							}
						})
					}

					createBtn("Delete folder", "modal-btn-secondary", false, func() {
						showDeleteConfirmation()
					})

					createBtn("Cancel", "modal-btn-secondary", true, nil)

					modal.SetContent(content)
				}

				// Start with folder options
				showFolderContent()
				modal.Show()
			})
			fileItem.AddController(longPress)
		} else {
			icon := gtk.NewImageFromIconName("text-x-generic")
			icon.SetPixelSize(48)
			fileItem.Append(icon)

			// Build full path for this file
			var fileFullPath string
			if path == "/" {
				fileFullPath = "/" + name
			} else {
				fileFullPath = path + "/" + name
			}

			fileName := name
			fileItem.AddCSSClass("clickable-folder") // Reuse folder styling for pressed state
			pressed := false

			// Drag gesture for press visual feedback
			dragGesture := gtk.NewGestureDrag()
			dragGesture.SetButton(uint(gdk.BUTTON_PRIMARY))
			dragGesture.Connect("drag-begin", func(startX, startY float64) {
				pressed = true
				fileItem.AddCSSClass("folder-pressed")
			})
			dragGesture.Connect("drag-update", func(offsetX, offsetY float64) {
				if !pressed {
					return
				}
				dist := math.Hypot(offsetX, offsetY)
				if dist > 45 {
					pressed = false
					fileItem.RemoveCSSClass("folder-pressed")
				}
			})
			dragGesture.Connect("drag-end", func(offsetX, offsetY float64) {
				fileItem.RemoveCSSClass("folder-pressed")
				pressed = false
			})
			fileItem.AddController(dragGesture)

			// Long press for file context menu
			longPress := gtk.NewGestureLongPress()
			longPress.SetTouchOnly(false)
			longPress.Connect("pressed", func(x, y float64) {
				pressed = false
				fileItem.RemoveCSSClass("folder-pressed")

				modal := components.NewModal(parentOverlay)

				var showFileMenu func()
				var showDeleteConfirmation func()

				showDeleteConfirmation = func() {
					confirmContent := gtk.NewBox(gtk.OrientationVertical, 10)

					confirmTitle := gtk.NewLabel("Are you sure?")
					confirmTitle.AddCSSClass("modal-title")
					confirmContent.Append(confirmTitle)

					confirmMsg := gtk.NewLabel("This file will be permanently deleted.")
					confirmMsg.AddCSSClass("modal-message")
					confirmMsg.SetWrap(true)
					confirmMsg.SetMarginBottom(10)
					confirmContent.Append(confirmMsg)

					buttonBox := gtk.NewBox(gtk.OrientationHorizontal, 10)
					buttonBox.SetHomogeneous(true)

					cancelBtn := gtk.NewButton()
					cancelBtn.SetChild(gtk.NewLabel("Cancel"))
					cancelBtn.AddCSSClass("modal-button")
					cancelBtn.AddCSSClass("modal-btn-secondary")
					cancelBtn.ConnectClicked(func() {
						showFileMenu()
					})

					deleteBtn := gtk.NewButton()
					deleteBtn.SetChild(gtk.NewLabel("Delete"))
					deleteBtn.AddCSSClass("modal-button")
					deleteBtn.AddCSSClass("modal-btn-danger")
					deleteBtn.ConnectClicked(func() {
						modal.Hide()
						go func() {
							url, _ := storage.GetSetting("server_url")
							user, _ := storage.GetSetting("username")
							pass, _ := storage.GetSetting("password")
							client := nextcloud.NewClient(url, user, pass)
							err := client.DeleteFile(fileFullPath)
							if err != nil {
								log.Printf("Failed to delete file: %v", err)
								return
							}
							log.Printf("Deleted file: %s", fileFullPath)
							glib.IdleAdd(func() {
								// Remove from grid
								grid.Remove(fileItem.Parent())
								// Remove from gridItems
								for i, item := range gridItems {
									if item.name == fileName && !item.isDir {
										gridItems = append(gridItems[:i], gridItems[i+1:]...)
										break
									}
								}
							})
						}()
					})

					buttonBox.Append(cancelBtn)
					buttonBox.Append(deleteBtn)
					confirmContent.Append(buttonBox)

					modal.SetContent(confirmContent)
				}

				showFileMenu = func() {
					content := gtk.NewBox(gtk.OrientationVertical, 5)

					title := gtk.NewLabel(fileName)
					title.AddCSSClass("modal-title")
					title.SetEllipsize(pango.EllipsizeEnd)
					title.SetMarginBottom(10)
					content.Append(title)

					// Delete file button
					deleteBtn := gtk.NewButton()
					deleteBtn.SetChild(gtk.NewLabel("Delete file"))
					deleteBtn.AddCSSClass("modal-button")
					deleteBtn.AddCSSClass("modal-btn-secondary")
					deleteBtn.SetHAlign(gtk.AlignFill)
					deleteBtn.ConnectClicked(func() {
						showDeleteConfirmation()
					})
					content.Append(deleteBtn)

					// Cancel button
					cancelBtn := gtk.NewButton()
					cancelBtn.SetChild(gtk.NewLabel("Cancel"))
					cancelBtn.AddCSSClass("modal-button")
					cancelBtn.AddCSSClass("modal-btn-secondary")
					cancelBtn.SetHAlign(gtk.AlignFill)
					cancelBtn.ConnectClicked(func() {
						modal.Hide()
					})
					content.Append(cancelBtn)

					modal.SetContent(content)
				}

				showFileMenu()
				modal.Show()
			})
			fileItem.AddController(longPress)
		}

		nameLabel := gtk.NewLabel(truncateFileName(name, 20))
		nameLabel.AddCSSClass("file-label")
		nameLabel.SetSizeRequest(80, -1)
		fileItem.Append(nameLabel)

		return fileItem
	}

	// Find the correct insertion position for a new item respecting the sort order
	// Sort order: folders first, then files, alphabetically within each group
	findInsertPosition := func(name string, isDir bool) int {
		lowerName := strings.ToLower(name)
		for i, item := range gridItems {
			// If we're inserting a folder
			if isDir {
				// If current item is a file, insert before it (folders come first)
				if !item.isDir {
					return i
				}
				// Both are folders, compare alphabetically
				if lowerName < strings.ToLower(item.name) {
					return i
				}
			} else {
				// We're inserting a file
				// If current item is a folder, continue (folders come first)
				if item.isDir {
					continue
				}
				// Both are files, compare alphabetically
				if lowerName < strings.ToLower(item.name) {
					return i
				}
			}
		}
		// Insert at the end
		return len(gridItems)
	}

	// Add an item to the grid in-place at the correct sorted position
	addItemInPlace := func(name string, isDir bool, syncedSet map[string]bool) {
		pos := findInsertPosition(name, isDir)
		widget := createFileItemWidget(name, isDir, currentPath, syncedSet)
		grid.Insert(widget, pos)

		// Insert into gridItems at the same position
		newItem := GridItem{name: name, isDir: isDir}
		if pos >= len(gridItems) {
			gridItems = append(gridItems, newItem)
		} else {
			gridItems = append(gridItems[:pos+1], gridItems[pos:]...)
			gridItems[pos] = newItem
		}
	}

	// Check if a name (file or folder) already exists in the current directory
	nameExists := func(name string) bool {
		lowerName := strings.ToLower(name)
		for _, item := range gridItems {
			if strings.ToLower(item.name) == lowerName {
				return true
			}
		}
		return false
	}

	// Generate a unique filename by appending (2), (3), etc. before the extension
	generateUniqueFileName := func(name string) string {
		if !nameExists(name) {
			return name
		}

		ext := filepath.Ext(name)
		baseName := strings.TrimSuffix(name, ext)

		for i := 2; ; i++ {
			newName := fmt.Sprintf("%s (%d)%s", baseName, i, ext)
			if !nameExists(newName) {
				return newName
			}
		}
	}

	refreshFiles = func(path string) {
		currentPath = path
		// Update breadcrumb
		if updateBreadcrumb != nil {
			updateBreadcrumb(currentPath)
		}
		// Clear folder items map
		for k := range folderItems {
			delete(folderItems, k)
		}
		// Clear grid items tracking
		gridItems = nil

		for {
			child := grid.FirstChild()
			if child == nil {
				break
			}
			grid.Remove(child)
		}

		// Update back button visibility
		if currentPath == "/" {
			setBackHandler(nil, false)
		} else {
			setBackHandler(navigateUp, true)
		}

		url, _ := storage.GetSetting("server_url")
		user, _ := storage.GetSetting("username")
		pass, _ := storage.GetSetting("password")

		if url == "" || user == "" {
			return
		}

		// Fetch synced folders for this path
		syncedFolders, err := storage.GetSyncFolders()
		if err != nil {
			log.Printf("Failed to get synced folders: %v", err)
		}
		// Build a set of synced folder paths for quick lookup
		syncedSet := make(map[string]bool)
		// Check if current path is inside a synced folder
		isInsideSyncedFolder = false
		for _, sf := range syncedFolders {
			parent := filepath.Dir(sf.RemotePath)
			if parent == "." {
				parent = "/"
			}
			name := filepath.Base(sf.RemotePath)
			key := parent + "/" + name
			if parent == "/" {
				key = "/" + name
			}
			syncedSet[key] = true

			// Check if currentPath starts with this synced folder path
			if strings.HasPrefix(path+"/", sf.RemotePath+"/") {
				isInsideSyncedFolder = true
			}
		}

		spinner.SetVisible(true)

		go func() {
			client := nextcloud.NewClient(url, user, pass)
			files, err := client.ListFiles(path)
			if err != nil {
				log.Printf("Failed to list files: %v", err)
				glib.IdleAdd(func() {
					spinner.SetVisible(false)
				})
				return
			}

			glib.IdleAdd(func() {
				spinner.SetVisible(false)

				// Sort: folders first, then files, alphabetically within each group
				sort.Slice(files, func(i, j int) bool {
					if files[i].Type != files[j].Type {
						return files[i].Type == "dir" // dirs come first
					}
					return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name)
				})

				for _, f := range files {
					isDir := f.Type == "dir"
					fileItem := createFileItemWidget(f.Name, isDir, path, syncedSet)
					grid.Append(fileItem)
					gridItems = append(gridItems, GridItem{name: f.Name, isDir: isDir})
				}
			})
		}()
	}

	// refreshFiles("/")

	// Plus button click handler for create modal
	plusBtn.ConnectClicked(func() {
		modal := components.NewModal(parentOverlay)

		var showCreateMenu func()
		var showNewFolderInput func()

		showCreateMenu = func() {
			content := gtk.NewBox(gtk.OrientationVertical, 5)

			title := gtk.NewLabel("Create")
			title.AddCSSClass("modal-title")
			title.SetMarginBottom(10)
			content.Append(title)

			createBtn := func(label string, action func()) {
				btn := gtk.NewButton()
				btn.SetChild(gtk.NewLabel(label))
				btn.AddCSSClass("modal-button")
				btn.AddCSSClass("modal-btn-secondary")
				btn.SetHAlign(gtk.AlignFill)
				btn.ConnectClicked(action)
				content.Append(btn)
			}

			createBtn("Upload file", func() {
				modal.Hide()
				dialog := gtk.NewFileChooserNative(
					"Select File to Upload",
					nil,
					gtk.FileChooserActionOpen,
					"_Upload",
					"_Cancel",
				)

				dialog.ConnectResponse(func(responseId int) {
					if responseId == int(gtk.ResponseAccept) {
						file := dialog.File()
						if file != nil {
							localPath := file.Path()
							originalFileName := filepath.Base(localPath)

							// Generate unique filename if name already exists
							fileName := generateUniqueFileName(originalFileName)

							go func() {
								// Read file content
								content, err := os.ReadFile(localPath)
								if err != nil {
									log.Printf("Failed to read file: %v", err)
									return
								}

								// Build remote path
								var remotePath string
								if currentPath == "/" {
									remotePath = "/" + fileName
								} else {
									remotePath = currentPath + "/" + fileName
								}

								url, _ := storage.GetSetting("server_url")
								user, _ := storage.GetSetting("username")
								pass, _ := storage.GetSetting("password")
								client := nextcloud.NewClient(url, user, pass)

								err = client.UploadFile(remotePath, content)
								if err != nil {
									log.Printf("Failed to upload file: %v", err)
									return
								}

								log.Printf("Uploaded file: %s", remotePath)
								glib.IdleAdd(func() {
									// Add file in-place with proper sorting (files are never synced)
									addItemInPlace(fileName, false, nil)
								})
							}()
						}
					}
					dialog.Destroy()
				})

				dialog.Show()
			})

			createBtn("New folder", func() {
				showNewFolderInput()
			})

			createBtn("Cancel", func() {
				modal.Hide()
			})

			modal.SetContent(content)
		}

		showNewFolderInput = func() {
			content := gtk.NewBox(gtk.OrientationVertical, 10)

			title := gtk.NewLabel("New folder")
			title.AddCSSClass("modal-title")
			content.Append(title)

			entry := gtk.NewEntry()
			entry.SetPlaceholderText("Folder name")
			entry.AddCSSClass("modal-entry")
			content.Append(entry)

			// Error label (hidden initially)
			errorLabel := gtk.NewLabel("That name already exists")
			errorLabel.AddCSSClass("modal-error-text")
			errorLabel.SetVisible(false)
			content.Append(errorLabel)

			// Clear error when user types
			entry.ConnectChanged(func() {
				entry.RemoveCSSClass("entry-error")
				errorLabel.SetVisible(false)
			})

			buttonBox := gtk.NewBox(gtk.OrientationHorizontal, 10)
			buttonBox.SetHomogeneous(true)
			buttonBox.SetMarginTop(10)

			cancelBtn := gtk.NewButton()
			cancelBtn.SetChild(gtk.NewLabel("Cancel"))
			cancelBtn.AddCSSClass("modal-button")
			cancelBtn.AddCSSClass("modal-btn-secondary")
			cancelBtn.ConnectClicked(func() {
				showCreateMenu()
			})

			addBtn := gtk.NewButton()
			addBtn.SetChild(gtk.NewLabel("Add"))
			addBtn.AddCSSClass("modal-button")
			addBtn.AddCSSClass("modal-btn-primary")
			addBtn.ConnectClicked(func() {
				folderName := entry.Text()
				if folderName == "" {
					return
				}

				// Check if name already exists
				if nameExists(folderName) {
					entry.AddCSSClass("entry-error")
					errorLabel.SetVisible(true)
					return
				}

				modal.Hide()

				go func() {
					var remotePath string
					if currentPath == "/" {
						remotePath = "/" + folderName
					} else {
						remotePath = currentPath + "/" + folderName
					}

					url, _ := storage.GetSetting("server_url")
					user, _ := storage.GetSetting("username")
					pass, _ := storage.GetSetting("password")
					client := nextcloud.NewClient(url, user, pass)

					err := client.MkdirAll(remotePath)
					if err != nil {
						log.Printf("Failed to create folder: %v", err)
						return
					}

					log.Printf("Created folder: %s", remotePath)
					glib.IdleAdd(func() {
						// Add folder in-place with proper sorting (new folders are not synced)
						addItemInPlace(folderName, true, nil)
					})
				}()
			})

			buttonBox.Append(cancelBtn)
			buttonBox.Append(addBtn)
			content.Append(buttonBox)

			modal.SetContent(content)
		}

		showCreateMenu()
		modal.Show()
	})

	return box, refreshFiles
}
