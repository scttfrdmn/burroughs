// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// c3Guard bounds each wait of TestC3MultiMRunsOnAtLeastFourAgents: READY, each request, and run's return. A harness
// hang guard, not a measurement: a run that parks fails at a place rather than as the test binary's timeout. It is
// scaled by raceGuardScale, because under -race the CPU phase outran 120 s on CI's macOS runner (#1390's run).
const c3Guard = 120 * time.Second * raceGuardScale

// c3Holds is the number of /hold requests p3multim waits for before its forced GC (its source's `holds`).
const c3Holds = 8

// lockedBuffer is a bytes.Buffer behind a mutex: the host's stderr writes may come from more than one goroutine.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// c3WorkChain is p3multim's /work answer for id, recomputed here so the expected body does not come from the guest.
func c3WorkChain(id int) string {
	h := sha256.Sum256([]byte("c3/work/" + strconv.Itoa(id)))
	for range 256 {
		h = sha256.Sum256(h[:])
	}
	return hex.EncodeToString(h[:]) + "\n"
}

// c3Place is where a parked run's agents are, for a failure's message: the engine's two counts at that moment (the
// executing one is a reading, not a verdict), the run slot's waiters, and how many goroutines are parked in
// memory.atomic.wait32, by bare function name.
func c3Place(core interface {
	PeakLiveAgents() int
	ExecutingAgents() int
},
) string {
	stacks := allGoroutineStacks()
	futex := 0
	for _, g := range strings.Split(stacks, "\n\n") {
		if strings.Contains(g, "(*memory).wait(") {
			futex++
		}
	}
	return fmt.Sprintf("PeakLiveAgents %d, ExecutingAgents now %d, the run slot's waiters: %s; %d goroutine(s) in "+
		"memory.atomic.wait32", core.PeakLiveAgents(), core.ExecutingAgents(), strings.Join(slotWaiters(stacks), "; "),
		futex)
}

// c3Get is one GET of the in-process arm's client, its body required to equal want.
func c3Get(client *http.Client, port, path, want string) error {
	resp, err := client.Get("http://127.0.0.1:" + port + path)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("GET %s: reading the body: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK || string(b) != want {
		return fmt.Errorf("GET %s: %s %q, want 200 %q", path, resp.Status, b, want)
	}
	return nil
}

// TestC3MultiMRunsOnAtLeastFourAgents is C3's in-process arm (#1345): p3multim.wasm, the fork's burroughsspawnp3
// build, run through the engine (InstantiateWithHost and CallRun, not the CLI) with the network granted and
// gate:cm-shared-memory on, so the test holds the instance and reads **the engine's own count**:
// `interp.Instance.PeakLiveAgents` on the core instance that exports `mstart`, the most agents its world has had
// alive at once, which must be at least 4. It is a high-water mark kept by the engine under the world's lock, read
// once after run has returned, not a sample: no window can miss it.
//
// It also asserts the guest's own place, the rendezvous line on stderr, "rendezvous 4 of 4" (four participants met
// while all four spun in guest code), and that its stdout equals p3multim.reading's, which arm N took from stock Go.
// The client here is smaller than cmd/burroughs' driveC3: 64 /work requests over 8 connections, the 8 /hold requests
// the program's forced GC waits for, and /quit. The program's stdout does not depend on which requests it served.
//
// `ExecutingAgents` is sampled every 200 µs across the run and **logged, never asserted** (grave #1360: a sampler's
// thread can miss a window, so a sampled peak can measure the runner).
//
// NEUTER, run (#1345): the run-slot relaxation off (agents.go's spawnAgent: the `!free` Enter arm set for a free
// instance too, ADR 0130's rollback): the run parks before the program's first line, "parked at READY" with stderr
// empty, PeakLiveAgents 3, ExecutingAgents 0, two spawned agents waiting in acquireRun at their entry and one goroutine
// in memory.atomic.wait32: the runtime's first Ms wait for a slot the first agent holds while it waits on them.
// NEUTER, run: the fork's netpollBreak a no-op (a scratch artifact, never committed): every request is answered and
// /quit returns "bye\n", but run never returns: stderr stops after the CPU line, PeakLiveAgents 5, two goroutines in
// memory.atomic.wait32, nothing in acquireRun. Shutdown's poll timer, set on one M, never wakes the M blocked in the
// poller. cmd/burroughs' driveC3 parks earlier with it, at /work?id=0, 5 of 5.
// NEUTER, run: the spawn door unserved (hostsys.go's spawnDoors left empty): the import is bound to the stub host, and
// the runtime's first spawn traps by name ("import burroughs:threads/spawn@0.1.0::spawn is not provided"), so no
// READY line is printed.
func TestC3MultiMRunsOnAtLeastFourAgents(t *testing.T) {
	t.Setenv(cmSharedMemoryGateEnv, "1")
	t.Setenv(asyncGateEnv, "1")
	reading := filepath.Join(forkP3Dir, "p3multim.reading")
	wantOut := ""
	b, err := os.ReadFile(reading)
	if err != nil {
		t.Fatalf("the committed reading is missing: %v", err)
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if q, ok := strings.CutPrefix(ln, "stdout after READY: "); ok {
			if wantOut, err = strconv.Unquote(q); err != nil {
				t.Fatalf("p3multim.reading's stdout line %q: %v", ln, err)
			}
		}
	}
	if wantOut == "" {
		t.Fatal("p3multim.reading has no stdout line")
	}

	pr, pw := io.Pipe()
	var errb lockedBuffer
	h := NewHost(pw, &errb, nil)
	h.Args = []string{"p3multim.wasm"}
	h.InheritNetwork = true
	in, err := InstantiateWithHost(readForkP3(t, "p3multim"), h)
	if err != nil {
		t.Fatalf("p3multim did not instantiate: %v", err)
	}
	defer in.Close()
	core := coreInstanceWithExport(in, "mstart")
	if core == nil {
		t.Fatal("no core instance exports mstart")
	}

	stop := make(chan struct{})
	sampled := make(chan int, 1)
	go func() {
		m := 0
		tick := time.NewTicker(200 * time.Microsecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				sampled <- m
				return
			case <-tick.C:
				m = max(m, core.ExecutingAgents())
			}
		}
	}()
	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		code, err := in.CallRun()
		_ = pw.Close()
		done <- result{code, err}
	}()
	ready := make(chan string, 1)
	rest := make(chan string, 1)
	go func() {
		br := bufio.NewReader(pr)
		line, _ := br.ReadString('\n')
		ready <- line
		b, _ := io.ReadAll(br)
		rest <- string(b)
	}()
	stopSampler := func() int { close(stop); return <-sampled }

	var port string
	select {
	case line := <-ready:
		p, ok := strings.CutPrefix(line, "READY ")
		if !ok {
			stopSampler()
			t.Fatalf("p3multim printed %q where its READY line belongs (stderr %q)", line, errb.String())
		}
		port = strings.TrimSuffix(p, "\n")
	case <-time.After(c3Guard):
		stopSampler()
		t.Fatalf("parked at READY: no READY line within %v (stderr %q); %s", c3Guard, errb.String(), c3Place(core))
	}

	tr := &http.Transport{MaxIdleConnsPerHost: 2 * c3Holds, DisableCompression: true}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: c3Guard}
	// 64 /work requests over 8 goroutines, then the 8 /hold requests at once, then /quit.
	for _, row := range []func(i int) error{
		func(i int) error {
			for j := range 8 {
				id := i + 8*j
				if err := c3Get(client, port, "/work?id="+strconv.Itoa(id), c3WorkChain(id)); err != nil {
					return err
				}
			}
			return nil
		},
		func(i int) error {
			return c3Get(client, port, "/hold?id="+strconv.Itoa(i), fmt.Sprintf("hold %d: in flight through the forced GC\n", i))
		},
	} {
		errs := make([]error, c3Holds)
		var wg sync.WaitGroup
		for i := range c3Holds {
			wg.Go(func() { errs[i] = row(i) })
		}
		wg.Wait()
		for _, e := range errs {
			if e != nil {
				stopSampler()
				t.Fatalf("%v (stderr %q); %s", e, errb.String(), c3Place(core))
			}
		}
	}
	if err := c3Get(client, port, "/quit", "bye\n"); err != nil {
		stopSampler()
		t.Fatalf("%v (stderr %q); %s", err, errb.String(), c3Place(core))
	}
	var r result
	select {
	case r = <-done:
	case <-time.After(c3Guard):
		stopSampler()
		t.Fatalf("run did not return within %v after /quit (stderr %q); %s", c3Guard, errb.String(), c3Place(core))
	}
	executing := stopSampler()
	peak := core.PeakLiveAgents()
	stderr := errb.String()
	if r.err != nil || r.code != 0 {
		t.Fatalf("run: exit %d, %v (stderr %q)", r.code, r.err, stderr)
	}
	t.Logf("PeakLiveAgents %d; ExecutingAgents sampled max %d (logged, not asserted)", peak, executing)
	for _, ln := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(ln, "gomaxprocs ") || strings.HasPrefix(ln, "rendezvous ") || strings.Contains(ln, "ms ran ") {
			t.Logf("guest: %s", ln)
		}
	}
	if peak < 4 {
		t.Errorf("PeakLiveAgents = %d, want at least 4: the engine's world never had four agents alive at once", peak)
	}
	if !regexp.MustCompile(`(?m)^rendezvous 4 of 4$`).MatchString(stderr) {
		t.Errorf("the guest's stderr does not place its rendezvous at 4 of 4: %q", stderr)
	}
	select {
	case out := <-rest:
		if out != wantOut {
			t.Errorf("stdout after READY = %q, want p3multim.reading's %q", out, wantOut)
		}
	case <-time.After(c3Guard):
		t.Fatal("the stdout's EOF did not arrive after run returned")
	}
	in.Close()
	requireNoSocketGoroutines(t)
}
