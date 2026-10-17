package component

import (
	"os"
	"testing"
)

// TestAMisalignedEventPointerTraps is the pin's `unpack_event` (2e7895c, upstream #741): `waitable-set.wait`
// and `.poll` trap unless their event pointer is 4-aligned (#1026). The rule entered with the pin move
// (2026-10-17); the previous pin stored at any address.
//
// Read before the fix (default path, gate:async on):
//
//	fixture            wasmtime 49.0.2                       pin 2e7895c   Burroughs before
//	poll-misaligned    trap "pointer not aligned", 10 of 10   trap          100
//	poll-aligned       100, 10 of 10                          100           100
//
// **Neuter, run:** dropping `storeEvent`'s alignment check makes `poll-misaligned` return 100 again.
func TestAMisalignedEventPointerTraps(t *testing.T) {
	for _, gate := range []string{"0", "1"} {
		for _, tc := range []struct {
			fixture string
			want    uint32 // 0: the trap
		}{
			{"poll-misaligned", 0},
			{"poll-aligned", 100},
		} {
			t.Run(tc.fixture+"/gate="+gate, func(t *testing.T) {
				t.Setenv(asyncGateEnv, "1")
				t.Setenv(cmThreadsGateEnv, gate)
				b, err := os.ReadFile("testdata/" + tc.fixture + ".wasm")
				if err != nil {
					t.Fatalf("the committed fixture is missing: %v", err)
				}
				in, err := Instantiate(b)
				if err != nil {
					t.Fatalf("instantiate: %v", err)
				}
				t.Cleanup(in.Close)
				res, err := in.CallValues("run")
				if tc.want == 0 {
					wantTrapContaining(t, tc.fixture, err, "pointer not aligned")
					return
				}
				if err != nil {
					t.Fatalf("run(): %v", err)
				}
				if v, ok := res[0].U32(); !ok || v != tc.want {
					t.Fatalf("run() = %v, want %d", res, tc.want)
				}
			})
		}
	}
}
