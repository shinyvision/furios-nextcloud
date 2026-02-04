package main

import (
	"bytes"
	"image"
	"image/png"
	"log"
	"nextcloud-gtk/internal/nextcloud"
	"nextcloud-gtk/storage"
	"nextcloud-gtk/ui/pages"
	"os"
	"path/filepath"
	"strings"

	_ "image/jpeg"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
	"golang.org/x/image/draw"
)

func NewWindow(app *gtk.Application, debugMode bool) *gtk.ApplicationWindow {
	window := gtk.NewApplicationWindow(app)
	window.SetTitle("Nextcloud")
	window.SetDefaultSize(360, 720)
	window.SetDecorated(false)

	overlay := gtk.NewOverlay()
	window.SetChild(overlay)

	mainBox := gtk.NewBox(gtk.OrientationVertical, 0)
	mainBox.AddCSSClass("main-window")
	overlay.SetChild(mainBox)

	// Persistent Header
	header := gtk.NewBox(gtk.OrientationHorizontal, 0)
	header.AddCSSClass("header-bar")
	mainBox.Append(header)

	// Back button (initially hidden, used for navigation)
	backIconPath := "assets/icons/ui/back.svg"
	if _, err := os.Stat(backIconPath); os.IsNotExist(err) {
		backIconPath = "/app/share/nextcloud-gtk/assets/icons/ui/back.svg"
	}
	backIcon := gtk.NewImageFromFile(backIconPath)
	backIcon.SetPixelSize(24)
	backBtn := gtk.NewButton()
	backBtn.SetChild(backIcon)
	backBtn.AddCSSClass("flat")
	backBtn.AddCSSClass("header-back-btn")
	backBtn.SetVisible(false)
	header.Append(backBtn)

	logoPath := "assets/icons/ui/logo.svg"
	if _, err := os.Stat(logoPath); os.IsNotExist(err) {
		logoPath = "/app/share/nextcloud-gtk/assets/icons/ui/logo.svg"
	}
	appIcon := gtk.NewImageFromFile(logoPath)
	appIcon.SetPixelSize(32)
	header.Append(appIcon)

	// Breadcrumb wrapper - SetSizeRequest(0, -1) prevents breadcrumb from affecting window min-width
	// while SetHExpand(true) allows it to expand to fill available space
	breadcrumbWrapper := gtk.NewBox(gtk.OrientationHorizontal, 0)
	breadcrumbWrapper.SetHExpand(true)
	breadcrumbWrapper.SetSizeRequest(0, -1) // Zero minimum width, natural height
	header.Append(breadcrumbWrapper)

	// Breadcrumb container
	breadcrumbBox := gtk.NewBox(gtk.OrientationHorizontal, 4)
	breadcrumbBox.SetMarginStart(8)
	breadcrumbBox.SetVisible(false)
	breadcrumbWrapper.Append(breadcrumbBox)

	// Home icon path
	homeIconPath := "assets/icons/ui/home.svg"
	if _, err := os.Stat(homeIconPath); os.IsNotExist(err) {
		homeIconPath = "/app/share/nextcloud-gtk/assets/icons/ui/home.svg"
	}

	// Function to update breadcrumb - will be set after navigateTo is defined
	var updateBreadcrumb func(path string)

	// Forward declare stack for closures
	var stack *gtk.Stack

	// Track pages that implement BackHandler
	var filesPage *pages.FilesPage
	var settingsPage *pages.SettingsPage

	// Get the active BackHandler based on visible page
	getActiveBackHandler := func() pages.BackHandler {
		visibleName := stack.VisibleChildName()
		switch visibleName {
		case "files":
			return filesPage
		case "settings":
			return settingsPage
		default:
			return nil
		}
	}

	// Update back button visibility based on current page
	updateBackButton := func() {
		handler := getActiveBackHandler()
		if handler != nil && handler.ShowBackButton() {
			backBtn.SetVisible(true)
			appIcon.SetVisible(false)
		} else {
			backBtn.SetVisible(false)
			appIcon.SetVisible(true)
		}
	}

	backBtn.ConnectClicked(func() {
		handler := getActiveBackHandler()
		if handler != nil {
			handler.HandleBack()
			updateBackButton()
		}
	})

	// Plus button for creating files/folders (only visible on files page)
	plusIconPath := "assets/icons/ui/plus.svg"
	if _, err := os.Stat(plusIconPath); os.IsNotExist(err) {
		plusIconPath = "/app/share/nextcloud-gtk/assets/icons/ui/plus.svg"
	}
	plusIcon := gtk.NewImageFromFile(plusIconPath)
	plusIcon.SetPixelSize(24)
	plusBtn := gtk.NewButton()
	plusBtn.SetChild(plusIcon)
	plusBtn.AddCSSClass("flat")
	plusBtn.AddCSSClass("header-plus-btn")
	plusBtn.SetVisible(false)
	header.Append(plusBtn)

	revealer := gtk.NewRevealer()
	dimmer := gtk.NewBox(gtk.OrientationVertical, 0)

	toggleMenu := func(open bool) {
		revealer.SetRevealChild(open)
		dimmer.SetVisible(open)
	}

	menuIconPath := "assets/icons/ui/menu.svg"
	if _, err := os.Stat(menuIconPath); os.IsNotExist(err) {
		menuIconPath = "/app/share/nextcloud-gtk/assets/icons/ui/menu.svg"
	}
	menuIcon := gtk.NewImageFromFile(menuIconPath)
	menuIcon.SetPixelSize(24)
	menuBtn := gtk.NewButton()
	menuBtn.SetHasFrame(false)
	menuBtn.SetChild(menuIcon)
	menuBtn.ConnectClicked(func() { toggleMenu(true) })
	header.Append(menuBtn)

	// Page Stack
	stack = gtk.NewStack()
	stack.SetTransitionType(gtk.StackTransitionTypeSlideLeftRight)
	mainBox.Append(stack)

	// Menu Dimmer
	dimmer.AddCSSClass("menu-overlay")
	dimmer.SetVisible(false)
	overlay.AddOverlay(dimmer)

	// Sidebar Menu (Upper overlay)
	menuBox := gtk.NewBox(gtk.OrientationVertical, 0)
	menuBox.AddCSSClass("sidebar-menu")
	menuBox.SetSizeRequest(250, -1)
	menuBox.SetHAlign(gtk.AlignEnd)

	revealer.SetTransitionType(gtk.RevealerTransitionTypeSlideLeft)
	revealer.SetChild(menuBox)
	revealer.SetHAlign(gtk.AlignEnd)
	overlay.AddOverlay(revealer)

	// Menu Content - User Profile Header
	profileBox := gtk.NewBox(gtk.OrientationHorizontal, 12)
	profileBox.AddCSSClass("menu-profile")
	profileBox.SetMarginStart(20)
	profileBox.SetMarginEnd(20)
	profileBox.SetMarginTop(20)
	profileBox.SetMarginBottom(16)

	// Avatar placeholder (will be updated when we have credentials)
	avatarFrame := gtk.NewBox(gtk.OrientationVertical, 0)
	avatarFrame.AddCSSClass("menu-avatar")
	avatarFrame.SetSizeRequest(48, 48)

	avatarImage := gtk.NewImage()
	avatarImage.SetPixelSize(48)
	avatarFrame.Append(avatarImage)
	profileBox.Append(avatarFrame)

	// Display name
	displayNameLabel := gtk.NewLabel("")
	displayNameLabel.AddCSSClass("menu-display-name")
	displayNameLabel.SetHAlign(gtk.AlignStart)
	displayNameLabel.SetEllipsize(3) // PANGO_ELLIPSIZE_END = 3
	displayNameLabel.SetMaxWidthChars(15)
	displayNameLabel.SetHExpand(true)
	profileBox.Append(displayNameLabel)

	menuBox.Append(profileBox)

	// Separator after profile
	separator := gtk.NewSeparator(gtk.OrientationHorizontal)
	separator.AddCSSClass("menu-separator")
	menuBox.Append(separator)

	addMenuBtn := func(label string, iconFile string, action func()) {
		btn := gtk.NewButton()
		btnBox := gtk.NewBox(gtk.OrientationHorizontal, 15)

		iconPath := "assets/icons/ui/" + iconFile
		if _, err := os.Stat(iconPath); os.IsNotExist(err) {
			iconPath = "/app/share/nextcloud-gtk/assets/icons/ui/" + iconFile
		}
		img := gtk.NewImageFromFile(iconPath)
		img.SetPixelSize(20)
		btnBox.Append(img)

		lbl := gtk.NewLabel(label)
		lbl.SetHAlign(gtk.AlignStart)
		btnBox.Append(lbl)

		btn.SetChild(btnBox)
		btn.ConnectClicked(action)
		menuBox.Append(btn)
	}

	var showPage func(string)

	addMenuBtn("Files", "menu-folder.svg", func() {
		toggleMenu(false)
		showPage("files")
	})

	addMenuBtn("Settings", "menu-settings.svg", func() {
		toggleMenu(false)
		showPage("settings")
	})

	// navigateTo will be set by the files page
	var navigateTo func(string)

	showPage = func(name string) {
		stack.SetVisibleChildName(name)
		header.SetVisible(name != "server" && name != "login")
		plusBtn.SetVisible(name == "files")
		if name != "files" {
			breadcrumbBox.SetVisible(false)
			appIcon.SetVisible(true)
		}

		if name == "files" && navigateTo != nil {
			navigateTo("/")
		}

		glib.IdleAdd(updateBackButton)
	}

	serverPage := pages.NewServerPage(showPage)
	stack.AddNamed(serverPage, "server")

	loginPage := pages.NewLoginPage(showPage)
	stack.AddNamed(loginPage.Box, "login")

	// Current breadcrumb path - stored for recalculation on resize
	var currentBreadcrumbPath string

	// Helper to build breadcrumb widgets based on available width
	buildBreadcrumb := func(path string, availableWidth int) {
		// Clear existing breadcrumb
		for {
			child := breadcrumbBox.FirstChild()
			if child == nil {
				break
			}
			breadcrumbBox.Remove(child)
		}

		// Don't show breadcrumb at root or empty path
		if path == "/" || path == "" {
			breadcrumbBox.SetVisible(false)
			appIcon.SetVisible(true)
			return
		}

		breadcrumbBox.SetVisible(true)
		appIcon.SetVisible(false)

		// Add home button
		homeBtn := gtk.NewButton()
		homeIcon := gtk.NewImageFromFile(homeIconPath)
		homeIcon.SetPixelSize(18)
		homeBtn.SetChild(homeIcon)
		homeBtn.AddCSSClass("flat")
		homeBtn.AddCSSClass("breadcrumb-btn")
		homeBtn.ConnectClicked(func() {
			if navigateTo != nil {
				navigateTo("/")
			}
		})
		breadcrumbBox.Append(homeBtn)

		// Split path into parts
		parts := strings.Split(strings.Trim(path, "/"), "/")

		// Estimate how many parts we can show based on available width
		// Home button ~28px, separator ~10px, each char ~7px, button padding ~14px
		homeWidth := 28
		sepWidth := 10
		charWidth := 7
		btnPadding := 14
		ellipsisWidth := 25 // "…" label width

		// Calculate total path length in estimated pixels
		totalPathWidth := homeWidth
		partWidths := make([]int, len(parts))
		for i, part := range parts {
			partWidths[i] = sepWidth + len(part)*charWidth + btnPadding
			totalPathWidth += partWidths[i]
		}

		// If everything fits, show all
		if availableWidth <= 0 || totalPathWidth <= availableWidth {
			for i, part := range parts {
				sep := gtk.NewLabel("/")
				sep.AddCSSClass("breadcrumb-sep")
				breadcrumbBox.Append(sep)

				partPath := "/" + strings.Join(parts[:i+1], "/")
				btn := gtk.NewButton()
				btn.SetLabel(part)
				btn.AddCSSClass("flat")
				btn.AddCSSClass("breadcrumb-btn")
				capturedPath := partPath
				btn.ConnectClicked(func() {
					if navigateTo != nil {
						navigateTo(capturedPath)
					}
				})
				breadcrumbBox.Append(btn)
			}
			return
		}

		// Need to truncate - always show first and last, add ellipsis in middle
		// Minimum fallback: home + ... + last (with ellipsized last)
		if len(parts) <= 2 {
			// For 1-2 parts, show: home + ... + ellipsized last
			sep := gtk.NewLabel("/")
			sep.AddCSSClass("breadcrumb-sep")
			breadcrumbBox.Append(sep)

			ellipsis := gtk.NewLabel("…")
			ellipsis.AddCSSClass("breadcrumb-sep")
			breadcrumbBox.Append(ellipsis)

			sep2 := gtk.NewLabel("/")
			sep2.AddCSSClass("breadcrumb-sep")
			breadcrumbBox.Append(sep2)

			lastIdx := len(parts) - 1
			lastPath := "/" + strings.Join(parts, "/")
			lastBtn := gtk.NewButton()
			lastLabel := gtk.NewLabel(parts[lastIdx])
			lastLabel.SetEllipsize(pango.EllipsizeEnd)
			lastLabel.SetMaxWidthChars(12)
			lastBtn.SetChild(lastLabel)
			lastBtn.AddCSSClass("flat")
			lastBtn.AddCSSClass("breadcrumb-btn")
			lastBtn.ConnectClicked(func() {
				if navigateTo != nil {
					navigateTo(lastPath)
				}
			})
			breadcrumbBox.Append(lastBtn)
			return
		}

		// Calculate minimum width needed: home + first + ellipsis + last
		minWidth := homeWidth + partWidths[0] + sepWidth + ellipsisWidth + partWidths[len(parts)-1]

		// If even minimum doesn't fit, use ultra-minimal: home + ... + ellipsized-last
		if minWidth > availableWidth {
			sep := gtk.NewLabel("/")
			sep.AddCSSClass("breadcrumb-sep")
			breadcrumbBox.Append(sep)

			ellipsis := gtk.NewLabel("…")
			ellipsis.AddCSSClass("breadcrumb-sep")
			breadcrumbBox.Append(ellipsis)

			sep2 := gtk.NewLabel("/")
			sep2.AddCSSClass("breadcrumb-sep")
			breadcrumbBox.Append(sep2)

			lastIdx := len(parts) - 1
			lastPath := "/" + strings.Join(parts, "/")
			lastBtn := gtk.NewButton()
			lastLabel := gtk.NewLabel(parts[lastIdx])
			lastLabel.SetEllipsize(pango.EllipsizeEnd)
			lastLabel.SetMaxWidthChars(10)
			lastBtn.SetChild(lastLabel)
			lastBtn.AddCSSClass("flat")
			lastBtn.AddCSSClass("breadcrumb-btn")
			lastBtn.ConnectClicked(func() {
				if navigateTo != nil {
					navigateTo(lastPath)
				}
			})
			breadcrumbBox.Append(lastBtn)
			return
		}

		// Determine how many middle parts we can show
		remainingWidth := availableWidth - minWidth
		middleParts := []int{} // indices of middle parts to show

		// Try to add parts from the end (closer to current location)
		for i := len(parts) - 2; i > 0 && remainingWidth > 0; i-- {
			if remainingWidth >= partWidths[i] {
				middleParts = append([]int{i}, middleParts...)
				remainingWidth -= partWidths[i]
			} else {
				break
			}
		}

		// Build the breadcrumb: first part
		sep1 := gtk.NewLabel("/")
		sep1.AddCSSClass("breadcrumb-sep")
		breadcrumbBox.Append(sep1)

		firstPath := "/" + parts[0]
		firstBtn := gtk.NewButton()
		firstBtn.SetLabel(parts[0])
		firstBtn.AddCSSClass("flat")
		firstBtn.AddCSSClass("breadcrumb-btn")
		firstBtn.ConnectClicked(func() {
			if navigateTo != nil {
				navigateTo(firstPath)
			}
		})
		breadcrumbBox.Append(firstBtn)

		// Ellipsis (only if we're skipping parts)
		if len(middleParts) == 0 || middleParts[0] > 1 {
			sep2 := gtk.NewLabel("/")
			sep2.AddCSSClass("breadcrumb-sep")
			breadcrumbBox.Append(sep2)

			ellipsis := gtk.NewLabel("…")
			ellipsis.AddCSSClass("breadcrumb-sep")
			breadcrumbBox.Append(ellipsis)
		}

		// Middle parts (if any)
		for _, i := range middleParts {
			sep := gtk.NewLabel("/")
			sep.AddCSSClass("breadcrumb-sep")
			breadcrumbBox.Append(sep)

			partPath := "/" + strings.Join(parts[:i+1], "/")
			btn := gtk.NewButton()
			btn.SetLabel(parts[i])
			btn.AddCSSClass("flat")
			btn.AddCSSClass("breadcrumb-btn")
			capturedPath := partPath
			btn.ConnectClicked(func() {
				if navigateTo != nil {
					navigateTo(capturedPath)
				}
			})
			breadcrumbBox.Append(btn)
		}

		// Last part - use ellipsized label
		sep3 := gtk.NewLabel("/")
		sep3.AddCSSClass("breadcrumb-sep")
		breadcrumbBox.Append(sep3)

		lastIdx := len(parts) - 1
		lastPath := "/" + strings.Join(parts, "/")
		lastBtn := gtk.NewButton()
		// Use a label with ellipsization for the last button
		lastLabel := gtk.NewLabel(parts[lastIdx])
		lastLabel.SetEllipsize(pango.EllipsizeEnd)
		lastLabel.SetMaxWidthChars(15) // Limit max width, will ellipsize if longer
		lastBtn.SetChild(lastLabel)
		lastBtn.AddCSSClass("flat")
		lastBtn.AddCSSClass("breadcrumb-btn")
		lastBtn.ConnectClicked(func() {
			if navigateTo != nil {
				navigateTo(lastPath)
			}
		})
		breadcrumbBox.Append(lastBtn)
	}

	// Fixed header element widths (back button ~48px when visible, plus btn ~48px, menu btn ~48px, header padding ~24px)
	const fixedHeaderWidth = 48 + 48 + 48 + 24 // plus, menu, back/logo, padding

	recalcBreadcrumb := func() {
		if currentBreadcrumbPath == "" || currentBreadcrumbPath == "/" {
			breadcrumbBox.SetVisible(false)
			appIcon.SetVisible(true)
			return
		}
		headerWidth := header.Width()
		availableWidth := headerWidth - fixedHeaderWidth
		if availableWidth < 0 {
			availableWidth = 0
		}
		buildBreadcrumb(currentBreadcrumbPath, availableWidth)
	}

	// Update breadcrumb based on current path
	updateBreadcrumb = func(path string) {
		currentBreadcrumbPath = path
		glib.IdleAdd(recalcBreadcrumb)
		glib.IdleAdd(updateBackButton)
	}

	var lastHeaderWidth int

	// Check for resize whenever the window becomes active or gets focus
	checkForResize := func() {
		currentWidth := header.Width()
		if currentWidth != lastHeaderWidth && currentWidth > 0 {
			lastHeaderWidth = currentWidth
			glib.IdleAdd(recalcBreadcrumb)
		}
	}

	// Connect to window state changes that indicate resize may have occurred
	window.Connect("map", func() {
		log.Printf("Window mapped")
		glib.IdleAdd(checkForResize)
	})

	// Poll for resize periodically (every 100ms) to catch window resize events
	glib.TimeoutAdd(100, func() bool {
		checkForResize()
		return true // Keep polling
	})

	filesPage = pages.NewFilesPage(overlay, showPage, func() { toggleMenu(true) }, plusBtn, updateBreadcrumb)
	navigateTo = filesPage.NavigateTo
	stack.AddNamed(filesPage.Box, "files")

	settingsPage = pages.NewSettingsPage(showPage)
	stack.AddNamed(settingsPage.Box, "settings")

	addMenuBtn("Logout", "menu-logout.svg", func() {
		toggleMenu(false)
		storage.ClearAuth()
		loginPage.Reset()
		showPage("server")
	})

	addMenuBtn("Close", "menu-close.svg", func() {
		toggleMenu(false)
	})

	// Check if already logged in
	storage.Ping()
	user, err1 := storage.GetSetting("username")
	pass, err2 := storage.GetSetting("password")
	serverURL, _ := storage.GetSetting("server_url")

	// Load user profile into menu
	loadUserProfile := func() {
		if user == "" || pass == "" || serverURL == "" {
			displayNameLabel.SetText("Not logged in")
			return
		}

		client := nextcloud.NewClient(serverURL, user, pass)

		// Load display name
		go func() {
			displayName, err := client.GetDisplayName()
			if err != nil {
				displayName = user
			}
			glib.IdleAdd(func() {
				displayNameLabel.SetText(displayName)
			})
		}()

		// Load avatar
		go func() {
			avatarData, err := client.GetAvatar(96)
			if err != nil {
				log.Printf("Failed to load avatar: %v", err)
				return
			}

			// Decode the image
			img, _, err := image.Decode(bytes.NewReader(avatarData))
			if err != nil {
				log.Printf("Failed to decode avatar: %v", err)
				return
			}

			// Scale to 48x48
			size := 48
			scaled := image.NewRGBA(image.Rect(0, 0, size, size))
			draw.CatmullRom.Scale(scaled, scaled.Bounds(), img, img.Bounds(), draw.Over, nil)

			// Create circular version
			circular := image.NewRGBA(image.Rect(0, 0, size, size))
			centerX, centerY := float64(size)/2, float64(size)/2
			radius := float64(size) / 2

			for y := 0; y < size; y++ {
				for x := 0; x < size; x++ {
					dx := float64(x) - centerX + 0.5
					dy := float64(y) - centerY + 0.5
					if dx*dx+dy*dy <= radius*radius {
						circular.Set(x, y, scaled.At(x, y))
					}
				}
			}

			// Save circular avatar to temp file
			tmpFile := filepath.Join(os.TempDir(), "nextcloud-gtk-avatar.png")
			f, err := os.Create(tmpFile)
			if err != nil {
				log.Printf("Failed to create avatar file: %v", err)
				return
			}
			if err := png.Encode(f, circular); err != nil {
				f.Close()
				log.Printf("Failed to encode avatar: %v", err)
				return
			}
			f.Close()

			glib.IdleAdd(func() {
				avatarImage.SetFromFile(tmpFile)
			})
		}()
	}

	if err1 == nil && err2 == nil && user != "" && pass != "" {
		loadUserProfile()
		showPage("files")
	} else {
		showPage("server")
	}

	keyController := gtk.NewEventControllerKey()
	keyController.ConnectKeyPressed(func(keyval, keycode uint, state gdk.ModifierType) bool {
		if keyval == gdk.KEY_Back {
			// First, close sidebar menu if open
			if revealer.RevealChild() {
				toggleMenu(false)
				return true
			}
			handler := getActiveBackHandler()
			if handler != nil && handler.HandleBack() {
				updateBackButton()
				return true
			}
		}
		return false
	})
	window.AddController(keyController)

	dimmerClick := gtk.NewGestureClick()
	dimmerClick.ConnectReleased(func(int, float64, float64) {
		toggleMenu(false)
	})
	dimmer.AddController(dimmerClick)

	// Prevent destruction on close - just hide
	window.ConnectCloseRequest(func() bool {
		log.Println("Window close requested - Hiding window")
		window.SetVisible(false)
		return true
	})

	return window
}
