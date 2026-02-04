package pages

import (
	"log"
	"nextcloud-gtk/storage"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// SettingsPage holds the state and implements BackHandler for the settings page.
type SettingsPage struct {
	Box      *gtk.Box
	showPage func(string)
}

// HandleBack handles the back button press - always goes back to files.
func (p *SettingsPage) HandleBack() bool {
	p.showPage("files")
	return true
}

// ShowBackButton returns true - settings page always shows back button.
func (p *SettingsPage) ShowBackButton() bool {
	return true
}

func NewSettingsPage(showPage func(string)) *SettingsPage {
	page := &SettingsPage{
		showPage: showPage,
	}

	box := gtk.NewBox(gtk.OrientationVertical, 0)
	box.AddCSSClass("files-container") // Re-use light background class
	page.Box = box

	content := gtk.NewBox(gtk.OrientationVertical, 20)
	content.SetMarginTop(20)
	content.SetMarginStart(20)
	content.SetMarginEnd(20)
	box.Append(content)

	label := gtk.NewLabel("Sync Settings")
	label.AddCSSClass("welcome-label")
	label.SetHAlign(gtk.AlignStart)
	content.Append(label)

	// List of synced folders
	listLabel := gtk.NewLabel("Synced Folders")
	listLabel.AddCSSClass("file-label")
	listLabel.SetHAlign(gtk.AlignStart)
	content.Append(listLabel)

	listBox := gtk.NewListBox()
	listBox.AddCSSClass("search-entry") // Use same rounded border style
	listBox.SetSelectionMode(gtk.SelectionNone)
	content.Append(listBox)

	refreshList := func() {
		// Clear existing rows
		for {
			row := listBox.RowAtIndex(0)
			if row == nil {
				break
			}
			listBox.Remove(row)
		}

		folders, _ := storage.GetSyncFolders()
		for _, f := range folders {
			row := gtk.NewBox(gtk.OrientationHorizontal, 10)
			row.SetMarginTop(10)
			row.SetMarginBottom(10)
			row.SetMarginStart(10)
			
			folderLabel := gtk.NewLabel(f.RemotePath + " → " + f.LocalPath)
			folderLabel.AddCSSClass("file-label")
			row.Append(folderLabel)
			
			listBox.Append(row)
		}
	}

	refreshList()

	// Form to add a new sync folder
	addBox := gtk.NewBox(gtk.OrientationVertical, 10)
	addBox.SetMarginTop(20)
	content.Append(addBox)

	remoteEntry := gtk.NewEntry()
	remoteEntry.SetPlaceholderText("Remote Path (e.g. /Documents)")
	remoteEntry.AddCSSClass("search-entry")
	addBox.Append(remoteEntry)

	localEntry := gtk.NewEntry()
	localEntry.SetPlaceholderText("Local Path (e.g. /home/rachel/Documents)")
	localEntry.AddCSSClass("search-entry")
	addBox.Append(localEntry)

	addBtn := gtk.NewButtonWithLabel("Add Sync Folder")
	addBtn.AddCSSClass("suggested-action")
	addBtn.ConnectClicked(func() {
		remote := remoteEntry.Text()
		local := localEntry.Text()
		if remote != "" && local != "" {
			if err := storage.AddSyncFolder(remote, local); err != nil {
				log.Printf("Failed to add sync folder: %v", err)
			} else {
				remoteEntry.SetText("")
				localEntry.SetText("")
				refreshList()
			}
		}
	})
	addBox.Append(addBtn)

	return page
}
