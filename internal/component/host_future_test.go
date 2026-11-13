// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"bytes"
	"io"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestWriteViaStreamCompletionWritesOnlyItsPayloadsBytes is #1246's witness: completing write-via-stream's
// future writes the guest no byte past `result<_, error-code>`, which is 2 bytes at alignment 1.
//
// The seam is the real one, short of a guest: `writeViaStream` mints the future over a readable stream end,
// `future.read` registers the reader's pointer, and the stream's host-completion hook resolves the future,
// exactly as the guest-driven copy does. The memory around the pointer is sentinel bytes, so any byte the
// completion writes is visible.
//
// **Exactly one byte changes, not two**, and that is the model's answer rather than a shortfall: `result::ok`
// is a payload-less case, and `store_variant` writes the discriminant and then stores a payload only if the
// case has one (definitions.py:store_variant:"if c.t is not None:"). So the discriminant byte becomes 0, the
// payload byte is left as it was, and every byte from `size(type)` on must be untouched.
//
// NEUTER, run: drop `completeLocked`'s payload arm, so the completion falls through to the 4-byte write.
// Bytes 1 to 3 at the pointer become 0 and this fails on all three; 2 and 3 are the grave's, past the
// result. `TestGateAsyncGuestRunsToTheCommittedReading` stays green under the neuter, which is why nothing
// caught the defect: the committed guest keeps no live data after its read buffer.
func TestWriteViaStreamCompletionWritesOnlyItsPayloadsBytes(t *testing.T) {
	cc, err := interp.NewCanonCallerForTest(1)
	if err != nil {
		t.Fatalf("harness caller: %v", err)
	}
	const (
		ptr      = 64
		before   = 8
		span     = 24
		sentinel = 0xAA
		// size(result<_, error-code>): a u8 discriminant, then the enum's u8 at offset 1. Stated rather than
		// asked of the codec, whose `size` is unexported; `TestCodecMatchesReferenceModel`'s result and enum
		// rows are what verify the codec agrees with the model about it.
		resultSize = 2
	)
	if werr := cc.Write(ptr-before, bytes.Repeat([]byte{sentinel}, span)); werr != nil {
		t.Fatalf("writing sentinels: %v", werr)
	}

	async := newAsyncHandles()
	re := &readableStreamEnd{}
	ri := async.addLocked(re)
	ret, err := writeViaStream(io.Discard)(async)(cc, []interp.Value{interp.I32(int32(ri))})
	if err != nil {
		t.Fatalf("write-via-stream: %v", err)
	}
	fi := ret[0].Int32()
	if _, rerr := futureRead(async)(cc, []interp.Value{interp.I32(fi), interp.I32(ptr)}); rerr != nil {
		t.Fatalf("future.read: %v", rerr)
	}
	if re.onHostComplete == nil {
		t.Fatal("write-via-stream registered no host completion; the seam this test drives is gone")
	}
	async.mu.Lock()
	re.onHostComplete(copyCompleted)
	fe, _ := handleAt[*readableFutureEnd](async, uint32(fi))
	async.mu.Unlock()
	if fe == nil || !fe.resolved {
		t.Fatal("the future did not resolve on the stream's completion")
	}
	if fe.completeErr != nil {
		t.Fatalf("the completion failed: %v", fe.completeErr)
	}

	got, err := cc.Read(ptr-before, span)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	for i, b := range got {
		at := i - before // offset from the reader's pointer
		switch {
		case at == 0:
			if b != 0 {
				t.Errorf("discriminant at ptr = %#x, want 0 (result::ok)", b)
			}
		case at >= resultSize:
			if b != sentinel {
				t.Errorf("byte ptr+%d = %#x, want the sentinel %#x: the completion wrote past the "+
					"%d-byte result<_, error-code>", at, b, sentinel, resultSize)
			}
		default: // before the pointer, and the payload byte a payload-less case does not store
			if b != sentinel {
				t.Errorf("byte ptr%+d = %#x, want the sentinel %#x", at, b, sentinel)
			}
		}
	}
}
