package pages

import (
	"log"
	"math"
	"nextcloud-gtk/internal/nextcloud"
	"nextcloud-gtk/storage"
	"nextcloud-gtk/ui/components"
	"os"
	"path/filepath"
	"strings"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

func NewFilesPage(parentOverlay *gtk.Overlay, showPage func(string), openMenu func(), setBackHandler func(func(), bool)) *gtk.Box {
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

	refreshFiles = func(path string) {
		currentPath = path
		// Clear folder items map
		for k := range folderItems {
			delete(folderItems, k)
		}

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

				for _, f := range files {
					fileItem := gtk.NewBox(gtk.OrientationVertical, 5)
					fileItem.SetSizeRequest(80, 100)

					var iconWidget gtk.Widgetter
					if f.Type == "dir" {
						// Build full path for this folder
						var folderFullPath string
						if path == "/" {
							folderFullPath = "/" + f.Name
						} else {
							folderFullPath = path + "/" + f.Name
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
					} else {
						icon := gtk.NewImageFromIconName("text-x-generic")
						icon.SetPixelSize(48)
						fileItem.Append(icon)
					}

					nameLabel := gtk.NewLabel(f.Name)
					nameLabel.AddCSSClass("file-label")
					nameLabel.SetEllipsize(3)
					nameLabel.SetSizeRequest(80, -1)
					fileItem.Append(nameLabel)

					// Make folders clickable with touch-friendly interaction
					if f.Type == "dir" {
						fileItem.AddCSSClass("clickable-folder")
						folderName := f.Name
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

							// Calculate full remote path for this folder
							var folderFullPath string
							if path == "/" {
								folderFullPath = "/" + folderName
							} else {
								folderFullPath = path + "/" + folderName
							}

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
								deleteBtn.AddCSSClass("modal-btn-primary")
								deleteBtn.ConnectClicked(func() {
									modal.Hide()
									go func() {
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
								} else {
									createBtn("Sync to filesystem", "modal-btn-secondary", false, func() {
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
					}

					grid.Append(fileItem)
				}
			})
		}()
	}

	refreshFiles("/")

	return box
}
