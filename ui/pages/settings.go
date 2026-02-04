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
	box.AddCSSClass("files-container")
	page.Box = box

	// Scrollable content
	scrolled := gtk.NewScrolledWindow()
	scrolled.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scrolled.SetVExpand(true)
	box.Append(scrolled)

	content := gtk.NewBox(gtk.OrientationVertical, 24)
	content.SetMarginTop(24)
	content.SetMarginBottom(24)
	content.SetMarginStart(20)
	content.SetMarginEnd(20)
	scrolled.SetChild(content)

	// === SYNC SECTION ===
	syncSectionLabel := gtk.NewLabel("Sync")
	syncSectionLabel.AddCSSClass("settings-section-title")
	syncSectionLabel.SetHAlign(gtk.AlignStart)
	content.Append(syncSectionLabel)

	syncSection := gtk.NewBox(gtk.OrientationVertical, 0)
	syncSection.AddCSSClass("settings-section")
	content.Append(syncSection)

	// --- Sync Interval Row ---
	syncIntervalRow := gtk.NewBox(gtk.OrientationHorizontal, 12)
	syncIntervalRow.AddCSSClass("settings-row")

	syncIntervalTextBox := gtk.NewBox(gtk.OrientationVertical, 0)
	syncIntervalTextBox.SetHExpand(true)

	syncIntervalTitle := gtk.NewLabel("Sync interval")
	syncIntervalTitle.AddCSSClass("settings-row-title")
	syncIntervalTitle.SetHAlign(gtk.AlignStart)
	syncIntervalTextBox.Append(syncIntervalTitle)

	syncIntervalSubtitle := gtk.NewLabel("How often to check for changes")
	syncIntervalSubtitle.AddCSSClass("settings-row-subtitle")
	syncIntervalSubtitle.SetHAlign(gtk.AlignStart)
	syncIntervalTextBox.Append(syncIntervalSubtitle)

	syncIntervalRow.Append(syncIntervalTextBox)

	// Dropdown for sync interval
	intervalOptions := []string{"30 seconds", "1 minute", "15 minutes", "30 minutes", "1 hour"}
	intervalValues := []string{"30", "60", "900", "1800", "3600"}

	intervalDropdown := gtk.NewDropDownFromStrings(intervalOptions)
	intervalDropdown.AddCSSClass("settings-dropdown")
	intervalDropdown.SetVAlign(gtk.AlignCenter)

	// Load saved value
	savedInterval, _ := storage.GetSetting("sync_interval")
	selectedIndex := uint(1) // Default to 1 minute
	for i, val := range intervalValues {
		if val == savedInterval {
			selectedIndex = uint(i)
			break
		}
	}
	intervalDropdown.SetSelected(selectedIndex)

	// Save on change
	intervalDropdown.Connect("notify::selected", func() {
		selected := intervalDropdown.Selected()
		if selected < uint(len(intervalValues)) {
			if err := storage.SaveSetting("sync_interval", intervalValues[selected]); err != nil {
				log.Printf("Failed to save sync interval: %v", err)
			} else {
				log.Printf("Sync interval set to %s seconds", intervalValues[selected])
			}
		}
	})

	syncIntervalRow.Append(intervalDropdown)
	syncSection.Append(syncIntervalRow)

	// --- WiFi Only Row ---
	wifiOnlyRow := gtk.NewBox(gtk.OrientationHorizontal, 12)
	wifiOnlyRow.AddCSSClass("settings-row")
	wifiOnlyRow.AddCSSClass("settings-row-clickable")

	wifiOnlyTextBox := gtk.NewBox(gtk.OrientationVertical, 0)
	wifiOnlyTextBox.SetHExpand(true)

	wifiOnlyTitle := gtk.NewLabel("Sync only on WiFi")
	wifiOnlyTitle.AddCSSClass("settings-row-title")
	wifiOnlyTitle.SetHAlign(gtk.AlignStart)
	wifiOnlyTextBox.Append(wifiOnlyTitle)

	wifiOnlySubtitle := gtk.NewLabel("Pause sync when using mobile data")
	wifiOnlySubtitle.AddCSSClass("settings-row-subtitle")
	wifiOnlySubtitle.SetHAlign(gtk.AlignStart)
	wifiOnlyTextBox.Append(wifiOnlySubtitle)

	wifiOnlyRow.Append(wifiOnlyTextBox)

	wifiOnlySwitch := gtk.NewSwitch()
	wifiOnlySwitch.AddCSSClass("settings-switch")
	wifiOnlySwitch.SetVAlign(gtk.AlignCenter)

	// Load saved value (default to true)
	savedWifiOnly, _ := storage.GetSetting("sync_wifi_only")
	if savedWifiOnly == "" || savedWifiOnly == "true" {
		wifiOnlySwitch.SetActive(true)
	} else {
		wifiOnlySwitch.SetActive(false)
	}

	wifiOnlySwitch.ConnectStateSet(func(state bool) bool {
		val := "false"
		if state {
			val = "true"
		}
		if err := storage.SaveSetting("sync_wifi_only", val); err != nil {
			log.Printf("Failed to save wifi only setting: %v", err)
		} else {
			log.Printf("Sync WiFi only set to %s", val)
		}
		return false // Let GTK handle the state change
	})

	wifiOnlyRow.Append(wifiOnlySwitch)

	// Make entire row clickable - use Activate for proper animation
	wifiOnlyGesture := gtk.NewGestureClick()
	wifiOnlyGesture.ConnectReleased(func(nPress int, x, y float64) {
		wifiOnlySwitch.Activate()
	})
	wifiOnlyRow.AddController(wifiOnlyGesture)

	syncSection.Append(wifiOnlyRow)

	// === NOTIFICATIONS SECTION ===
	notifSectionLabel := gtk.NewLabel("Notifications")
	notifSectionLabel.AddCSSClass("settings-section-title")
	notifSectionLabel.SetHAlign(gtk.AlignStart)
	notifSectionLabel.SetMarginTop(8)
	content.Append(notifSectionLabel)

	notifSection := gtk.NewBox(gtk.OrientationVertical, 0)
	notifSection.AddCSSClass("settings-section")
	content.Append(notifSection)

	// --- Notify on Sync Row ---
	notifyRow := gtk.NewBox(gtk.OrientationHorizontal, 12)
	notifyRow.AddCSSClass("settings-row")
	notifyRow.AddCSSClass("settings-row-clickable")

	notifyTextBox := gtk.NewBox(gtk.OrientationVertical, 0)
	notifyTextBox.SetHExpand(true)

	notifyTitle := gtk.NewLabel("Notify when files sync")
	notifyTitle.AddCSSClass("settings-row-title")
	notifyTitle.SetHAlign(gtk.AlignStart)
	notifyTextBox.Append(notifyTitle)

	notifySubtitle := gtk.NewLabel("Show a notification when changes are synced")
	notifySubtitle.AddCSSClass("settings-row-subtitle")
	notifySubtitle.SetHAlign(gtk.AlignStart)
	notifyTextBox.Append(notifySubtitle)

	notifyRow.Append(notifyTextBox)

	notifySwitch := gtk.NewSwitch()
	notifySwitch.AddCSSClass("settings-switch")
	notifySwitch.SetVAlign(gtk.AlignCenter)

	// Load saved value (default to false)
	savedNotify, _ := storage.GetSetting("sync_notify")
	notifySwitch.SetActive(savedNotify == "true")

	notifySwitch.ConnectStateSet(func(state bool) bool {
		val := "false"
		if state {
			val = "true"
		}
		if err := storage.SaveSetting("sync_notify", val); err != nil {
			log.Printf("Failed to save notify setting: %v", err)
		} else {
			log.Printf("Sync notifications set to %s", val)
		}
		return false
	})

	notifyRow.Append(notifySwitch)

	// Make entire row clickable - use Activate for proper animation
	notifyGesture := gtk.NewGestureClick()
	notifyGesture.ConnectReleased(func(nPress int, x, y float64) {
		notifySwitch.Activate()
	})
	notifyRow.AddController(notifyGesture)

	notifyRow.Append(notifySwitch)
	notifSection.Append(notifyRow)

	return page
}
