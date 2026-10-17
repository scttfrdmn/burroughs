package component

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// TestTheForwardBuiltinsAreRefusedByName is the ➡️ pair's status (`statusForward`), witnessed through a
// component. `stream.forward` (0x2e) and `future.forward` (0x2f) entered the pin at 2e7895c (upstream #717).
//
// No toolchain emits them yet. wasm-tools 1.258.0 rejects 0x2e as *"invalid leading byte for canonical
// function"*, and wasmtime 49.0.2 has no reading of them. So they are refused by name at decode, and ADR 0102
// follows the toolchains' stricter refusal. The fixtures are `forward-base.wat`'s `stream.drop-writable`
// with the opcode byte patched (that file's header gives the derivation).
//
// **Neuter, run:** removing the decoder's 0x2e/0x2f arm makes them fall to `default`, which has no sentinel,
// and this fails on `ErrUnsupportedForm`.
func TestTheForwardBuiltinsAreRefusedByName(t *testing.T) {
	for _, f := range []struct{ file, name string }{
		{"stream-forward.wasm", "stream.forward"},
		{"future-forward.wasm", "future.forward"},
	} {
		b, err := os.ReadFile("testdata/" + f.file)
		if err != nil {
			t.Fatalf("the committed fixture is missing: %v", err)
		}
		_, err = Load(b)
		if !errors.Is(err, ErrUnsupportedForm) {
			t.Fatalf("%s: Load returned %v; want ErrUnsupportedForm, a refusal by name", f.file, err)
		}
		if !strings.Contains(err.Error(), f.name) {
			t.Errorf("%s: the refusal does not name %s: %v", f.file, f.name, err)
		}
	}
}
