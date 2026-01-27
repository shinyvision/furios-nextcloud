package components

import (
	"math"

	"github.com/diamondburned/gotk4/pkg/cairo"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

const (
	spinnerSize      = 48
	strokeWidth      = 4.0
	rotationDuration = 1333 // ms for one full rotation
	arcDuration      = 1333 // ms for arc expand/contract cycle
	minArc           = 0.05 // minimum arc length (fraction of circle)
	maxArc           = 0.75 // maximum arc length (fraction of circle)
)

// MaterialSpinner is a custom spinner widget that mimics Google's Material Design
// indeterminate circular progress indicator.
type MaterialSpinner struct {
	*gtk.DrawingArea
	startTime int64
	running   bool
}

// NewSpinner creates a centered Material Design loading spinner.
// The arc expands to catch its tail, then contracts as the tail catches up.
func NewSpinner() *gtk.Box {
	container := gtk.NewBox(gtk.OrientationVertical, 0)
	container.SetHAlign(gtk.AlignCenter)
	container.SetVAlign(gtk.AlignCenter)
	container.SetVExpand(true)
	container.SetHExpand(true)

	spinner := newMaterialSpinner()
	container.Append(spinner.DrawingArea)

	return container
}

// NewSpinnerWithSize creates a spinner with custom size.
func NewSpinnerWithSize(size int) *gtk.Box {
	container := gtk.NewBox(gtk.OrientationVertical, 0)
	container.SetHAlign(gtk.AlignCenter)
	container.SetVAlign(gtk.AlignCenter)
	container.SetVExpand(true)
	container.SetHExpand(true)

	spinner := newMaterialSpinnerWithSize(size)
	container.Append(spinner.DrawingArea)

	return container
}

func newMaterialSpinner() *MaterialSpinner {
	return newMaterialSpinnerWithSize(spinnerSize)
}

func newMaterialSpinnerWithSize(size int) *MaterialSpinner {
	s := &MaterialSpinner{
		DrawingArea: gtk.NewDrawingArea(),
		startTime:   0,
		running:     true,
	}

	s.DrawingArea.SetSizeRequest(size, size)
	s.DrawingArea.SetDrawFunc(func(area *gtk.DrawingArea, cr *cairo.Context, width, height int) {
		s.draw(cr, width, height)
	})

	// Start animation
	s.startTime = glib.GetMonotonicTime() / 1000 // Convert to ms

	// Use timeout for animation (~60fps)
	var animate func() bool
	animate = func() bool {
		if !s.running {
			return false
		}
		s.DrawingArea.QueueDraw()
		return true
	}
	glib.TimeoutAdd(16, animate) // ~60fps

	return s
}

func (s *MaterialSpinner) draw(cr *cairo.Context, width, height int) {
	currentTime := glib.GetMonotonicTime() / 1000 // ms
	elapsed := currentTime - s.startTime

	// Calculate base rotation angle (continuous rotation)
	rotationProgress := float64(elapsed%rotationDuration) / float64(rotationDuration)
	rotationAngle := rotationProgress * 2 * math.Pi

	// Calculate arc length animation
	// The arc expands for half the cycle, then contracts
	arcProgress := float64(elapsed%arcDuration) / float64(arcDuration)

	// Use sine easing for smooth expand/contract
	// 0 -> 0.5: arc expands (head moves faster than tail)
	// 0.5 -> 1: arc contracts (tail catches up to head)
	var arcLength float64
	if arcProgress < 0.5 {
		// Expanding: ease out
		t := arcProgress * 2
		arcLength = minArc + (maxArc-minArc)*easeInOutCubic(t)
	} else {
		// Contracting: ease in
		t := (arcProgress - 0.5) * 2
		arcLength = maxArc - (maxArc-minArc)*easeInOutCubic(t)
	}

	// Calculate how many full arc cycles have completed
	// Each cycle adds (maxArc - minArc) worth of rotation to keep it continuous
	completedCycles := elapsed / arcDuration
	cycleOffset := float64(completedCycles) * (maxArc - minArc) * 2 * math.Pi

	// Additional rotation within current cycle to make it feel like the head is "catching" the tail
	// This offset increases as the arc contracts
	headOffset := 0.0
	if arcProgress >= 0.5 {
		t := (arcProgress - 0.5) * 2
		headOffset = (maxArc - minArc) * 2 * math.Pi * easeInOutCubic(t)
	}

	// Center and radius
	cx := float64(width) / 2
	cy := float64(height) / 2
	radius := math.Min(cx, cy) - strokeWidth

	// Calculate start and end angles
	startAngle := rotationAngle + cycleOffset + headOffset - math.Pi/2 // Start from top
	endAngle := startAngle + arcLength*2*math.Pi

	// Draw arc
	cr.SetLineWidth(strokeWidth)
	cr.SetLineCap(cairo.LineCapRound)

	// Light gray color (#9e9e9e)
	cr.SetSourceRGB(0.62, 0.62, 0.62)

	cr.NewPath()
	cr.Arc(cx, cy, radius, startAngle, endAngle)
	cr.Stroke()
}

// easeInOutCubic provides smooth acceleration and deceleration
func easeInOutCubic(t float64) float64 {
	if t < 0.5 {
		return 4 * t * t * t
	}
	return 1 - math.Pow(-2*t+2, 3)/2
}

// Stop stops the spinner animation
func (s *MaterialSpinner) Stop() {
	s.running = false
}
