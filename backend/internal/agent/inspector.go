package agent

import "time"

// TurnOutput is the provider-neutral result of ReadLatestOutput (05 §2): the
// most recent completed assistant output for a worker's current conversation.
// Empty Output means "no completed turn yet" — never an error. No provider
// handle (session/sandbox id) is carried.
type TurnOutput struct {
	Output string
	At     time.Time
}
