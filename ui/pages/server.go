package pages

import (
	"nextcloud-gtk/storage"
	"os"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

func NewServerPage(showPage func(string)) *gtk.Box {
	box := gtk.NewBox(gtk.OrientationVertical, 0)
	box.SetVExpand(true)
	box.SetHExpand(true)

	// Logo at the top
	logoPath := "assets/icons/ui/logo-blue.svg"
	if _, err := os.Stat(logoPath); os.IsNotExist(err) {
		logoPath = "/app/share/nextcloud-gtk/assets/icons/ui/logo-blue.svg"
	}
	logoIcon := gtk.NewImageFromFile(logoPath)
	logoIcon.SetPixelSize(100)
	logoIcon.SetHAlign(gtk.AlignCenter)
	logoIcon.SetMarginTop(40)
	box.Append(logoIcon)

	// Middle container
	centerBox := gtk.NewBox(gtk.OrientationVertical, 0)
	centerBox.SetVAlign(gtk.AlignCenter)
	centerBox.SetHAlign(gtk.AlignFill)
	centerBox.SetVExpand(true)
	centerBox.SetMarginStart(30)
	centerBox.SetMarginEnd(30)
	box.Append(centerBox)

	titleLabel := gtk.NewLabel("Connect to your\nNextcloud")
	titleLabel.AddCSSClass("welcome-label")
	titleLabel.SetJustify(gtk.JustifyCenter)
	titleLabel.SetMarginBottom(40)
	centerBox.Append(titleLabel)

	entry := gtk.NewEntry()
	entry.SetPlaceholderText("Server URL (e.g, cloud.example.com)")
	entry.AddCSSClass("search-entry")
	entry.SetHExpand(true)
	if url, err := storage.GetSetting("server_url"); err == nil && url != "" {
		entry.SetText(url)
	}
	centerBox.Append(entry)

	btnBox := gtk.NewBox(gtk.OrientationHorizontal, 0)
	btnBox.SetHAlign(gtk.AlignCenter)
	btnBox.SetMarginTop(40)
	centerBox.Append(btnBox)

	button := gtk.NewButtonWithLabel("Next")
	button.AddCSSClass("suggested-action")
	button.ConnectClicked(func() {
		url := entry.Text()
		if url != "" {
			storage.SaveSetting("server_url", url)
			showPage("login")
		}
	})
	btnBox.Append(button)

	// Footer at the bottom
	footerLabel := gtk.NewLabel("Powered by Nextcloud")
	footerLabel.AddCSSClass("powered-by")
	footerLabel.SetVAlign(gtk.AlignEnd)
	box.Append(footerLabel)

	return box
}
