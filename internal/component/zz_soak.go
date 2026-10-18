package component

import "time"

// TEMPORARY (the chair's ruling of 2026-10-17, item 1): the widening points for the fence soak. Reverted by
// the next commit of the same PR, so they never land on main. Zero, and so inert, outside the soak test.
var (
	soakWidenHost   time.Duration
	soakWidenCancel time.Duration
)

func soakSleep(d time.Duration) {
	if d > 0 {
		time.Sleep(d)
	}
}
