package api

// White-box test for the hub's per-project fan-out (11 phase 2): broadcast is
// partitioned by projectID, so a push for one project reaches only that
// project's connected clients and never leaks onto another project's stream.
// In-package (not api_test) so it can construct clients with a projectID and
// drive broadcast directly, without standing up an SSE connection per project.

import "testing"

// projA / projB are the two project scopes the broadcast-isolation tests use.
const (
	projA = "proj-A"
	projB = "proj-B"
)

func TestBroadcast_DeliversOnlyToMatchingProjectClients(t *testing.T) {
	h := NewHub(nil) // broadcast never reads the board port.
	a := &client{ch: make(chan sseFrame, clientBuffer), projectID: projA}
	b := &client{ch: make(chan sseFrame, clientBuffer), projectID: projB}
	h.add(a)
	h.add(b)

	h.broadcast(projA, sseFrame{event: eventBoard, data: []byte(`{"worker_total":1}`)})

	select {
	case got := <-a.ch:
		if got.event != eventBoard {
			t.Errorf("client A received event %q, want board", got.event)
		}
	default:
		t.Error("client A (matching project) received no frame, want the board frame")
	}

	select {
	case got := <-b.ch:
		t.Errorf("client B (other project) received a %q frame, want none — the fan-out must not cross projects", got.event)
	default:
	}
}

func TestBroadcast_FansOutToEverySameProjectClient(t *testing.T) {
	h := NewHub(nil)
	a := &client{ch: make(chan sseFrame, clientBuffer), projectID: projA}
	b := &client{ch: make(chan sseFrame, clientBuffer), projectID: projA}
	h.add(a)
	h.add(b)

	h.broadcast(projA, sseFrame{event: eventSay, data: []byte(`{"text":"hi"}`)})

	for name, c := range map[string]*client{"a": a, "b": b} {
		select {
		case <-c.ch:
		default:
			t.Errorf("client %s (same project) received no frame, want the say frame", name)
		}
	}
}
