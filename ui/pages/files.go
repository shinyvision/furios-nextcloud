package pages

import (
	"log"
	"math"
	"nextcloud-gtk/internal/nextcloud"
	"nextcloud-gtk/storage"
	"nextcloud-gtk/ui/components"
	"os"
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

	// Search bar
	searchBox := gtk.NewBox(gtk.OrientationVertical, 10)
	searchBox.SetMarginStart(15)
	searchBox.SetMarginEnd(15)
	searchBox.SetMarginTop(15)
	searchBox.SetMarginBottom(15)
	box.Append(searchBox)

	searchEntry := gtk.NewEntry()
	searchEntry.SetPlaceholderText("Search files...")
	searchEntry.AddCSSClass("search-entry")
	searchBox.Append(searchEntry)

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

					var icon *gtk.Image
					if f.Type == "dir" {
						icon = gtk.NewImageFromFile(folderPath)
						icon.AddCSSClass("folder-icon")
					} else {
						icon = gtk.NewImageFromIconName("text-x-generic")
					}
					icon.SetPixelSize(48)
					fileItem.Append(icon)

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
							pressed = false // Reset logic

							// Double check distance logic just in case
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
							// Reset pressed state so we don't also navigate
							pressed = false
							fileItem.RemoveCSSClass("folder-pressed")

							// Open Modal
							modal := components.NewModal(parentOverlay)

							// Content
							content := gtk.NewBox(gtk.OrientationVertical, 5)

							// Title
							title := gtk.NewLabel(folderName)
							title.AddCSSClass("modal-title")
							title.SetMarginBottom(10)
							content.Append(title)

							createBtn := func(label string, cssClass string, closeOnAction bool, action func()) {
								btn := gtk.NewButton()

								// Label
								lbl := gtk.NewLabel(label)
								btn.SetChild(lbl)

								btn.AddCSSClass("modal-button") // Base style
								btn.AddCSSClass(cssClass)       // Color variant

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

							createBtn("Sync to filesystem", "modal-btn-secondary", false, func() {
								log.Println("Sync requested for:", folderName)
							})

							createBtn("Delete folder", "modal-btn-secondary", false, func() {
								log.Println("Delete requested for:", folderName)
							})

							createBtn("Cancel", "modal-btn-secondary", true, nil)

							modal.SetContent(content)
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
