package main

import (
	"nextcloud-gtk/storage"
	"nextcloud-gtk/ui/pages"
	"os"

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
	}

	serverPage := pages.NewServerPage(showPage)
	stack.AddNamed(serverPage, "server")

	loginPage := pages.NewLoginPage(showPage)
	stack.AddNamed(loginPage.Box, "login")

	filesPage := pages.NewFilesPage(overlay, showPage, func() { toggleMenu(true) }, setBackHandler)
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
