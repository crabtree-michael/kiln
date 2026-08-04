package obs

import (
	"crypto/rand"
	"encoding/hex"
)

// InstanceKey is the log attribute name every record carries identifying the
// process that emitted it. Two backend instances run side by side for the
// ~70–85 s of every rolling deploy, and until this existed the application
// could not tell you which of them did what — the duplicate-agent
// investigations (docs/root-cause-2026-08-0*) only worked because Render
// happens to label log lines by instance externally.
const InstanceKey = "instance"

// instanceIDBytes is how much randomness the id carries: 8 bytes / 16 hex
// characters — short enough to eyeball in a log line, far beyond collision
// range for the handful of processes alive during one deploy.
const instanceIDBytes = 8

// instanceID is minted once, at process boot, and never changes. It is
// deliberately not derived from the hostname or container id: those are
// reused across restarts, and "which *run* of the process" is the question a
// deploy-overlap incident actually asks.
var instanceID = mintInstanceID()

// InstanceID returns this process's boot id — stamped onto every log record by
// the composition root, so a line can always be attributed to one process.
func InstanceID() string { return instanceID }

// mintInstanceID draws the boot id. crypto/rand does not fail on any platform
// we run on; if it somehow did, a constant id degrades observability but must
// never stop the process from booting.
func mintInstanceID() string {
	var b [instanceIDBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}
