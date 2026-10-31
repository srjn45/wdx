package screen

// Activity is what an agent's ACA is doing right now, read from its screen.
type Activity string

const (
	ActivityWorking Activity = "working"
	ActivityIdle    Activity = "idle"
	ActivityWaiting Activity = "waiting"
	// ActivityUnknown is a screen the parser cannot read. It is never acted
	// on. With an input box it means the box's footer does not say, and
	// whoever reads the screen over time decides.
	ActivityUnknown Activity = "unknown"
)

// ScreenState is what a parser reads from one screen.
type ScreenState struct {
	Activity Activity
	// InputBox is true when the ACA's input box shows.
	InputBox bool
	// Draft is true when the box holds text a human put there, whatever the
	// activity. An open prompt or a missing box has none.
	Draft bool
}
