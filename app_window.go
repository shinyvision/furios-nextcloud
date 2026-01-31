package main

import (
	"nextcloud-gtk/storage"
	"nextcloud-gtk/ui/pages"
	"os"
	"strings"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

func NewWindow(app *gtk.Application, debugMode bool) *gtk.ApplicationWindow {
	window := gtk.NewApplicationWindow(app)
	window.SetTitle("Nextcloud")
	window.SetDefaultSize(360, 720)

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

	// Breadcrumb container
	breadcrumbBox := gtk.NewBox(gtk.OrientationHorizontal, 4)
	breadcrumbBox.SetMarginStart(8)
	breadcrumbBox.SetVisible(false)
	header.Append(breadcrumbBox)

	// Home icon path
	homeIconPath := "assets/icons/ui/home.svg"
	if _, err := os.Stat(homeIconPath); os.IsNotExist(err) {
		homeIconPath = "/app/share/nextcloud-gtk/assets/icons/ui/home.svg"
	}

	// Function to update breadcrumb - will be set after navigateTo is defined
	var updateBreadcrumb func(path string)

	// Back button handler system
	var currentBackHandler func()
	setBackHandler := func(handler func(), visible bool) {
		currentBackHandler = handler
		backBtn.SetVisible(visible)
		appIcon.SetVisible(!visible)
	}
	backBtn.ConnectClicked(func() {
		if currentBackHandler != nil {
			currentBackHandler()
		}
	})

	// Spacer to push menu button to the right
	spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
	spacer.SetHExpand(true)
	header.Append(spacer)

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

	menuBtn := gtk.NewButton()
	menuBtn.SetHasFrame(false)
	menuBtn.SetChild(gtk.NewImageFromIconName("open-menu-symbolic"))
	menuBtn.ConnectClicked(func() { toggleMenu(true) })
	header.Append(menuBtn)

	// Page Stack
	stack := gtk.NewStack()
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

	// Menu Content
	titleBox := gtk.NewBox(gtk.OrientationHorizontal, 0)
	titleBox.SetMarginStart(20)
	titleBox.SetMarginTop(20)
	titleBox.SetMarginBottom(10)

	menuLabel := gtk.NewLabel("Menu")
	menuLabel.AddCSSClass("welcome-label")
	titleBox.Append(menuLabel)
	menuBox.Append(titleBox)

	addMenuBtn := func(label string, icon string, action func()) {
		btn := gtk.NewButton()
		btnBox := gtk.NewBox(gtk.OrientationHorizontal, 15)

		img := gtk.NewImageFromIconName(icon)
		btnBox.Append(img)

		lbl := gtk.NewLabel(label)
		lbl.SetHAlign(gtk.AlignStart)
		btnBox.Append(lbl)

		btn.SetChild(btnBox)
		btn.ConnectClicked(action)
		menuBox.Append(btn)
	}

	addMenuBtn("Files", "folder-symbolic", func() {
		toggleMenu(false)
		stack.SetVisibleChildName("files")
	})

	addMenuBtn("Settings", "settings-symbolic", func() {
		toggleMenu(false)
		stack.SetVisibleChildName("settings")
	})

	// Pages
	showPage := func(name string) {
		stack.SetVisibleChildName(name)
		// Hide header on first two pages
		header.SetVisible(name != "server" && name != "login")
		// Show plus button only on files page
		plusBtn.SetVisible(name == "files")
		// Show breadcrumb only on files page
		breadcrumbBox.SetVisible(name == "files")
	}

	serverPage := pages.NewServerPage(showPage)
	stack.AddNamed(serverPage, "server")

	loginPage := pages.NewLoginPage(showPage)
	stack.AddNamed(loginPage.Box, "login")

	// navigateTo will be set by the files page
	var navigateTo func(string)

	// Update breadcrumb based on current path
	updateBreadcrumb = func(path string) {
		// Clear existing breadcrumb
		for {
			child := breadcrumbBox.FirstChild()
			if child == nil {
				break
			}
			breadcrumbBox.Remove(child)
		}

		// Don't show breadcrumb at root
		if path == "/" {
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

		// Calculate how many parts we can show (rough estimate based on available space)
		maxParts := 4 // Show at most 4 parts before truncating

		if len(parts) <= maxParts {
			// Show all parts
			for i, part := range parts {
				// Add separator
				sep := gtk.NewLabel("/")
				sep.AddCSSClass("breadcrumb-sep")
				breadcrumbBox.Append(sep)

				// Build path up to this part
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
		} else {
			// Truncate from middle: show first part, ..., last two parts
			// First part
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

			// Ellipsis
			sep2 := gtk.NewLabel("/")
			sep2.AddCSSClass("breadcrumb-sep")
			breadcrumbBox.Append(sep2)

			ellipsis := gtk.NewLabel("...")
			ellipsis.AddCSSClass("breadcrumb-sep")
			breadcrumbBox.Append(ellipsis)

			// Last two parts
			for i := len(parts) - 2; i < len(parts); i++ {
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
		}
	}

	filesPage, setNavigateTo := pages.NewFilesPage(overlay, showPage, func() { toggleMenu(true) }, setBackHandler, plusBtn, updateBreadcrumb)
	navigateTo = setNavigateTo
	stack.AddNamed(filesPage, "files")

	settingsPage := pages.NewSettingsPage(showPage, setBackHandler)
	stack.AddNamed(settingsPage, "settings")

	addMenuBtn("Logout", "system-log-out-symbolic", func() {
		toggleMenu(false)
		storage.ClearAuth()
		loginPage.Reset()
		showPage("server")
	})

	addMenuBtn("Close", "window-close-symbolic", func() {
		toggleMenu(false)
	})

	// Check if already logged in
	storage.Ping()
	user, err1 := storage.GetSetting("username")
	pass, err2 := storage.GetSetting("password")
	if err1 == nil && err2 == nil && user != "" && pass != "" {
		showPage("files")
	} else {
		showPage("server")
	}

	// Dimmer click
	clickGesture := gtk.NewGestureClick()
	clickGesture.ConnectPressed(func(int, float64, float64) {
		toggleMenu(false)
	})
	dimmer.AddController(clickGesture)

	return window
}
