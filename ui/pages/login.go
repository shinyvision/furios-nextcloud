package pages

import (
	"log"
	"nextcloud-gtk/internal/nextcloud"
	"nextcloud-gtk/storage"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

type LoginPage struct {
	Box        *gtk.Box
	errorLabel *gtk.Label
	loginBtn   *gtk.Button
}

func (p *LoginPage) Reset() {
	p.errorLabel.SetText("")
	p.loginBtn.SetSensitive(true)
}

func NewLoginPage(showPage func(string)) *LoginPage {
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

	titleLabel := gtk.NewLabel("Login to Nextcloud")
	titleLabel.AddCSSClass("welcome-label")
	titleLabel.SetMarginBottom(40)
	centerBox.Append(titleLabel)

	btnBox := gtk.NewBox(gtk.OrientationVertical, 10)
	btnBox.SetHAlign(gtk.AlignFill)
	centerBox.Append(btnBox)

	loginBtn := gtk.NewButtonWithLabel("Connect Account")
	loginBtn.AddCSSClass("suggested-action")
	loginBtn.SetHExpand(true)
	btnBox.Append(loginBtn)

	backBtn := gtk.NewButtonWithLabel("Back")
	backBtn.AddCSSClass("secondary-action")
	backBtn.SetHExpand(true)
	backBtn.ConnectClicked(func() {
		showPage("server")
	})
	btnBox.Append(backBtn)

	descLabel := gtk.NewLabel("We will open your browser to authenticate with Nextcloud.")
	descLabel.SetWrap(true)
	descLabel.SetJustify(gtk.JustifyCenter)
	descLabel.SetMarginTop(20)
	centerBox.Append(descLabel)

	errorLabel := gtk.NewLabel("")
	errorLabel.AddCSSClass("error-text")
	errorLabel.SetMarginTop(10)
	centerBox.Append(errorLabel)

	loginBtn.ConnectClicked(func() {
		url, _ := storage.GetSetting("server_url")
		if url == "" {
			errorLabel.SetText("Server URL missing")
			return
		}

		loginBtn.SetSensitive(false)
		errorLabel.SetText("Initiating...")

		go func() {
			lr, err := nextcloud.InitiateLogin(url)
			if err != nil {
				glib.IdleAdd(func() {
					errorLabel.SetText("Flow failed: " + err.Error())
					loginBtn.SetSensitive(true)
				})
				return
			}

			// Open browser
			openBrowser(lr.Login)

			glib.IdleAdd(func() {
				errorLabel.SetText("Please login in your browser...")
			})

			// Poll for results
			for {
				pr, err := nextcloud.PollLogin(lr.Poll.Endpoint, lr.Poll.Token)
				if err != nil {
					glib.IdleAdd(func() {
						errorLabel.SetText("Polling error: " + err.Error())
						loginBtn.SetSensitive(true)
					})
					return
				}

				if pr != nil {
					storage.SaveSetting("username", pr.Username)
					storage.SaveSetting("password", pr.Password)
					glib.IdleAdd(func() {
						showPage("files")
					})
					return
				}

				time.Sleep(2 * time.Second)
			}
		}()
	})

	// Footer at the bottom
	footerLabel := gtk.NewLabel("Powered by Nextcloud")
	footerLabel.AddCSSClass("powered-by")
	footerLabel.SetVAlign(gtk.AlignEnd)
	box.Append(footerLabel)

	return &LoginPage{
		Box:        box,
		errorLabel: errorLabel,
		loginBtn:   loginBtn,
	}
}

func openBrowser(url string) {
	var err error
	switch runtime.GOOS {
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	}
	if err != nil {
		log.Printf("Failed to open browser: %v", err)
	}
}
