package main

import "runtime"

// runtimeGOMAXPROCS is reported so a row says how many agents the run actually had. Under the fork,
// GOMAXPROCS is what decides whether there is a second M at all, and a stress row that did not say so would
// be indistinguishable from a single-agent run.
func runtimeGOMAXPROCS() int { return runtime.GOMAXPROCS(0) }
