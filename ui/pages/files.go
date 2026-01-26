package pages

import (
	"log"
	"nextcloud-gtk/internal/nextcloud"
	"nextcloud-gtk/storage"
	"os"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

func NewFilesPage(showPage func(string), openMenu func()) *gtk.Box {
	box := gtk.NewBox(gtk.OrientationVertical, 0)
	box.AddCSSClass("files-container")

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
	grid := gtk.NewFlowBox()
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
	box.Append(scrolled)

	folderPath := "assets/icons/ui/folder.svg"
	if _, err := os.Stat(folderPath); os.IsNotExist(err) {
		folderPath = "/app/share/nextcloud-gtk/assets/icons/ui/folder.svg"
	}

	// Load real data
	refreshFiles := func() {
		// Clear grid
		for {
			child := grid.ChildAtIndex(0)
			if child == nil {
				break
			}
			grid.Remove(child)
		}

		url, _ := storage.GetSetting("server_url")
		user, _ := storage.GetSetting("username")
		pass, _ := storage.GetSetting("password")

		if url == "" || user == "" {
			return
		}

		go func() {
			client := nextcloud.NewClient(url, user, pass)
			files, err := client.ListFiles("/")
			if err != nil {
				log.Printf("Failed to list files: %v", err)
				return
			}

			glib.IdleAdd(func() {
				for _, f := range files {
					fileItem := gtk.NewBox(gtk.OrientationVertical, 5)
					fileItem.SetSizeRequest(80, 100)

					iconName := "text-x-generic"
					if f.Type == "dir" {
						iconName = folderPath
					}

					var icon *gtk.Image
					if f.Type == "dir" {
						icon = gtk.NewImageFromFile(folderPath)
						icon.AddCSSClass("folder-icon")
					} else {
						icon = gtk.NewImageFromIconName(iconName)
					}
					icon.SetPixelSize(48)
					fileItem.Append(icon)

					nameLabel := gtk.NewLabel(f.Name)
					nameLabel.AddCSSClass("file-label")
					nameLabel.SetEllipsize(3) // End
					fileItem.Append(nameLabel)

					grid.Append(fileItem)
				}
			})
		}()
	}

	// Refresh when page becomes visible? 
	// For now just once
	refreshFiles()

	return box
}
