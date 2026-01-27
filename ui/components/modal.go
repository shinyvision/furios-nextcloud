package components

import (
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

type Modal struct {
	overlay       *gtk.Overlay
	parentOverlay *gtk.Overlay
	dimmer        *gtk.Box
	contentBox    *gtk.Box
	scaleRevealer *gtk.Revealer // Not using Revealer for scale, but could use for opacity.
	// For scale, we need custom CSS or Fixed container interactions.
	// To keep it simple and robust with GTK4, we'll use CSS transitions on a class.

	container *gtk.Box
	isShown   bool
}

// NewModal creates a new modal that can be attached to the given parent overlay.
// The parentOverlay should be the root window overlay to cover everything.
func NewModal(parentOverlay *gtk.Overlay) *Modal {
	m := &Modal{
		parentOverlay: parentOverlay,
	}

	// 1. Container - touches edges, handles dimmer background
	m.container = gtk.NewBox(gtk.OrientationVertical, 0)
	m.container.SetHAlign(gtk.AlignFill)
	m.container.SetVAlign(gtk.AlignFill)
	m.container.AddCSSClass("modal-dimmer") // Background color + opacity transition

	// Click on dimmer to close
	clickGesture := gtk.NewGestureClick()
	clickGesture.Connect("pressed", func(n int, x, y float64) {
		// Check if click is inside contentBox
		// Translate coordinates from container (dimmer) to contentBox
		tx, ty, ok := m.container.TranslateCoordinates(m.contentBox, x, y)
		if ok {
			// Check if transformed coordinates are within contentBox bounds
			if tx >= 0 && tx < float64(m.contentBox.Width()) &&
				ty >= 0 && ty < float64(m.contentBox.Height()) {
				// Click was inside content, do nothing (let button handle it or just ignore)
				return
			}
		}

		// Click was outside (on dimmer), close modal
		m.Hide()
	})
	m.container.AddController(clickGesture)

	// 2. Center alignment wrapper
	centerBox := gtk.NewBox(gtk.OrientationVertical, 0)
	centerBox.SetHAlign(gtk.AlignCenter)
	centerBox.SetVAlign(gtk.AlignCenter)
	centerBox.SetHExpand(true)
	centerBox.SetVExpand(true)
	m.container.Append(centerBox)

	// 3. Content Box - actual modal content
	m.contentBox = gtk.NewBox(gtk.OrientationVertical, 0)
	m.contentBox.AddCSSClass("modal-content")

	centerBox.Append(m.contentBox)

	return m
}

// SetContent sets the widget to be displayed inside the modal
func (m *Modal) SetContent(widget gtk.Widgetter) {
	// Remove existing children if any
	child := m.contentBox.FirstChild()
	for child != nil {
		m.contentBox.Remove(child)
		child = m.contentBox.FirstChild()
	}
	m.contentBox.Append(widget)
}

func (m *Modal) Show() {
	if m.isShown {
		return
	}
	m.isShown = true

	m.parentOverlay.AddOverlay(m.container)

	// Trigger CSS transitions
	// We need a small delay to allow the widget to be mapped/rendered for transition to work
	glib.IdleAdd(func() {
		m.container.AddCSSClass("modal-visible")
		m.contentBox.AddCSSClass("modal-scale-in")
	})
}

func (m *Modal) Hide() {
	if !m.isShown {
		return
	}
	m.isShown = false

	m.container.RemoveCSSClass("modal-visible")
	m.contentBox.RemoveCSSClass("modal-scale-in")

	// Wait for animation to finish before removing
	glib.TimeoutAdd(150, func() bool {
		m.parentOverlay.RemoveOverlay(m.container)
		return false
	})
}
