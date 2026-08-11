// Package lifecycle defines format-neutral artifact state and durable job
// execution semantics.
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
