// Package lifecycle defines the format-neutral artifact state machine.
package lifecycle

type State string

const (
	Staged     State = "staged"
	Visible    State = "visible"
	Tombstoned State = "tombstoned"
)

func CanTransition(from, to State) bool {
	return (from == Staged && to == Visible) || (from == Visible && to == Tombstoned)
}
