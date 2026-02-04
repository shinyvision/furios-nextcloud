package pages

// BackHandler is an optional interface that pages can implement
// to provide custom back button behavior.
type BackHandler interface {
	// HandleBack is called when the back button is pressed.
	// Returns true if the page handled the back action (modal closed, navigation done, etc.).
	// Returns false if the page didn't handle it (e.g., at root level).
	HandleBack() bool

	// ShowBackButton returns true if the back button should be visible for this page.
	ShowBackButton() bool
}
