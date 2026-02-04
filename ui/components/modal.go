package components

import (
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// activeModals tracks all currently visible modals for back button handling
var activeModals []*Modal

// CloseTopModal closes the topmost modal if any are open.
// Returns true if a modal was closed, false if no modals were open.
func CloseTopModal() bool {
	if len(activeModals) == 0 {
		return false
	}
	// Close the most recently opened modal (last in slice)
	top := activeModals[len(activeModals)-1]
	top.Hide()
	return true
}

// HasActiveModal returns true if any modal is currently shown
func HasActiveModal() bool {
	return len(activeModals) > 0
}

type Modal struct {
	parentOverlay *gtk.Overlay
	contentBox    *gtk.Box
	container     *gtk.Box
	isShown       bool
}

// NewModal creates a new modal that can be attached to the given parent overlay.
// The parentOverlay should be the root window overlay to cover everything.
func NewModal(parentOverlay *gtk.Overlay) *Modal {
	m := &Modal{
		parentOverlay: parentOverlay,
	}

	// Container - touches edges, handles dimmer background
	m.container = gtk.NewBox(gtk.OrientationVertical, 0)
	m.container.SetHAlign(gtk.AlignFill)
	m.container.SetVAlign(gtk.AlignFill)
	m.container.AddCSSClass("modal-dimmer")

	// Center alignment wrapper
	centerBox := gtk.NewBox(gtk.OrientationVertical, 0)
	centerBox.SetHAlign(gtk.AlignCenter)
	centerBox.SetVAlign(gtk.AlignCenter)
	centerBox.SetHExpand(true)
	centerBox.SetVExpand(true)
	// Add margins so modal never touches screen edges
	centerBox.SetMarginStart(24)
	centerBox.SetMarginEnd(24)
	m.container.Append(centerBox)

	// Content Box - actual modal content
	m.contentBox = gtk.NewBox(gtk.OrientationVertical, 0)
	m.contentBox.AddCSSClass("modal-content")
	centerBox.Append(m.contentBox)

	return m
}

// SetContent sets the widget to be displayed inside the modal
func (m *Modal) SetContent(widget gtk.Widgetter) {
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

	// Track this modal for back button handling
	activeModals = append(activeModals, m)

	// Reset animation classes
	m.contentBox.AddCSSClass("modal-animate-in")
	m.contentBox.RemoveCSSClass("modal-animate-out")

	// Add to overlay
	m.parentOverlay.AddOverlay(m.container)

	// Trigger animations - use IdleAdd to ensure widget is in the tree first
	glib.IdleAdd(func() {
		m.container.AddCSSClass("modal-visible")
	})
}

func (m *Modal) Hide() {
	if !m.isShown {
		return
	}
	m.isShown = false

	// Remove from active modals tracking
	for i, modal := range activeModals {
		if modal == m {
			activeModals = append(activeModals[:i], activeModals[i+1:]...)
			break
		}
	}

	// Trigger close animations
	m.container.RemoveCSSClass("modal-visible")
	m.contentBox.RemoveCSSClass("modal-animate-in")
	m.contentBox.AddCSSClass("modal-animate-out")

	// Wait for animation to finish before removing
	glib.TimeoutAdd(200, func() bool {
		m.parentOverlay.RemoveOverlay(m.container)
		return false
	})
}
