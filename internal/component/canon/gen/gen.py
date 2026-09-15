#!/usr/bin/env python3
# Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
#
# Offline fixture generator for the Canonical ABI differential (ADR 0084, slice 2 / PR A).
#
# It drives the pinned component-model reference model (`definitions.py` @ 2bed77e, vendored beside this
# script under gen/vendor/, gitignored) over every in-scope value type and case, and emits committed
# fixtures: for each case, the linear-memory byte image and realloc trace of a `store`, and the flat
# core-value sequence of a `lower_flat`. The fixtures are the oracle's reading; the Go codec's
# differential test compares to them with no Python in the loop (the wabt precedent — a live oracle as a
# licensed skip would be a CI dependency in disguise under BURROUGHS_NO_SKIP=1).
#
# Run offline, never in CI:  make canon-fixtures  (or: python3 gen.py > ../testdata/fixtures.json)
#
# uv is the fleet's only Python; run under it:  uv run --no-project python3 gen.py

import json
import os
import struct
import sys

PIN = "2bed77e4228841c1d2721996d3ecc169ff96b158"
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "vendor"))

import definitions  # noqa: E402
from definitions import (  # noqa: E402
    BoolType, U8Type, U16Type, U32Type, U64Type, S8Type, S16Type, S32Type, S64Type,
    F32Type, F64Type, CharType, StringType, ListType, VariantType, ResultType,
    OwnType, BorrowType, TupleType, CaseType, MemInst, CanonicalOptions, ComponentInstance,
    LiftLowerContext, Store, store, load, lower_flat_values, flatten_types, align_to,
    alignment, elem_size, ResourceType, ResourceHandle,
)

definitions.DETERMINISTIC_PROFILE = True

# A bump-allocating heap identical to run_tests.py's Heap, but logging every realloc call so the fixture
# records the allocations a type requires (Scott's PR-A spec). The Go codec's test harness uses the same
# bump semantics from the same initial state, so a recorded ptr is reproducible on the other side.
class TracingHeap:
    def __init__(self, size):
        self.memory = bytearray(size)
        self.last_alloc = 0
        self.calls = []

    def realloc(self, args):
        original_ptr, original_size, alignment, new_size = args
        if original_ptr != 0 and new_size < original_size:
            ret = align_to(original_ptr, alignment)
            self.calls.append({"args": args, "ret": ret})
            return [ret]
        ret = align_to(self.last_alloc, alignment)
        self.last_alloc = ret + new_size
        if self.last_alloc > len(self.memory):
            raise RuntimeError("heap exhausted")
        self.memory[ret:ret + original_size] = self.memory[original_ptr:original_ptr + original_size]
        self.calls.append({"args": list(args), "ret": ret})
        return [ret]


def mk_cx(heap, encoding="utf8"):
    opts = CanonicalOptions()
    opts.memory = MemInst(heap.memory, "i32")
    opts.string_encoding = encoding
    opts.realloc = heap.realloc
    opts.post_return = None
    opts.sync_task_return = False
    opts.async_ = False
    opts.callback = None
    return LiftLowerContext(opts, ComponentInstance(Store()))


# The in-scope WIT-type mini-language, shared byte-for-byte with the Go side's fixture reader. A type
# outside this set has no builder here and none in the codec — the guest-driven scope, refused by name
# on both sides.
def build_type(spec):
    k = spec["kind"]
    match k:
        case "bool": return BoolType()
        case "u8": return U8Type()
        case "u16": return U16Type()
        case "u32": return U32Type()
        case "u64": return U64Type()
        case "s8": return S8Type()
        case "s16": return S16Type()
        case "s32": return S32Type()
        case "s64": return S64Type()
        case "f32": return F32Type()
        case "f64": return F64Type()
        case "char": return CharType()
        case "string": return StringType()
        case "list": return ListType(build_type(spec["elem"]))
        case "tuple": return TupleType([build_type(f) for f in spec["fields"]])
        case "variant":
            return VariantType([CaseType(c["name"], build_type(c["type"]) if c.get("type") else None)
                                for c in spec["cases"]])
        case "result":
            ok = build_type(spec["ok"]) if spec.get("ok") else None
            err = build_type(spec["err"]) if spec.get("err") else None
            return ResultType(ok, err)
        case "own": return OwnType(spec.get("rt", 0))
        case "borrow": return BorrowType(spec.get("rt", 0))
    raise ValueError(f"unknown type kind {k!r}")


# A JSON value -> the reference model's Python value. Strings become the model's String tuple; variants
# and results become single-key dicts; own/borrow are the i32 handle (the codec differential is over the
# ABI representation, not resource-table semantics — those are PR B/C).
def build_value(spec, value):
    k = spec["kind"]
    if k in ("f32", "f64"):
        # A float case gives its IEEE bit pattern as a hex string, so a non-canonical NaN payload and a
        # negative zero survive to the model exactly. The model canonicalizes NaN on lower/lift.
        bits = int(value, 16)
        if k == "f32":
            return struct.unpack("<f", struct.pack("<I", bits))[0]
        return struct.unpack("<d", struct.pack("<Q", bits))[0]
    if k == "string":
        return (value, "utf8", len(value.encode("utf-8")))
    if k == "list":
        return [build_value(spec["elem"], e) for e in value]
    if k == "variant":
        (name, payload), = value.items()
        c = next(c for c in spec["cases"] if c["name"] == name)
        return {name: build_value(c["type"], payload) if c.get("type") else None}
    if k == "result":
        (arm, payload), = value.items()
        sub = spec.get("ok") if arm == "ok" else spec.get("err")
        return {arm: build_value(sub, payload) if sub else None}
    return value  # primitives, char (a 1-char str), own/borrow (an int)


def flat_to_bits(ty, val):
    if ty == "f32":
        return definitions.core_i32_reinterpret_f32(val)
    if ty == "f64":
        return definitions.core_i64_reinterpret_f64(val)
    return val


def emit(case):
    t = build_type(case["type"])
    v = build_value(case["type"], case["value"])

    store_heap = TracingHeap(case.get("heap_size", 64))
    scx = mk_cx(store_heap)
    # Reserve the value's own region first — as lower_flat_values' spill and a list element store do —
    # so a string/list's data reallocs land after the header rather than colliding with it at ptr 0.
    ptr = store_heap.realloc([0, 0, alignment(t, "i32"), elem_size(t, "i32")])[0]
    store(scx, v, t, ptr)

    flat_heap = TracingHeap(case.get("heap_size", 64))
    fcx = mk_cx(flat_heap)
    flat_types = flatten_types([t], fcx.opts)
    flat_vals = lower_flat_values(fcx, 16, [v], [t])
    # Record a float flat slot as its IEEE bits, not the Python float: Go's == makes every NaN unequal
    # to itself, so the differential compares bit patterns (Scott's PR-A refinement 1).
    flat_vals = [flat_to_bits(ty, x) for ty, x in zip(flat_types, flat_vals)]

    return {
        "name": case["name"],
        "type": case["type"],
        "value": case["value"],
        "heap_size": case.get("heap_size", 64),
        "store": {
            "ptr": ptr,
            "memory_hex": store_heap.memory.hex(),
            "realloc": store_heap.calls,
            # The post-store handle table (empty for non-own types). The differential seeds the load
            # heap's table from this so lift_own can consume the handle a store put here — #728.
            "table": serialize_table(scx.inst.handles),
        },
        "flat": {
            "types": flat_types,
            "values": flat_vals,
            "memory_hex": flat_heap.memory.hex(),
            "realloc": flat_heap.calls,
            # The flat lowering's own handle table (empty for non-own), so lift-flat can consume it (#728).
            "table": serialize_table(fcx.inst.handles),
        },
    }


# Own-handle round-trip cases. A handle's value is a resource representation (an int); lower_own adds an
# owned entry to the instance table and stores its index, lift_own consumes it. The borrow value path is
# PR B's (its lend accounting lives at the call scope), so no borrow round-trip here.
OWN_CASES = [
    {"name": "own-rep-42", "rt": 0, "rep": 42},
    {"name": "own-rep-zero", "rt": 0, "rep": 0},
    {"name": "own-rep-max", "rt": 0, "rep": 4294967295},
]


def serialize_table(tbl):
    out = []
    for i, h in enumerate(tbl.array):
        if h is not None and isinstance(h, ResourceHandle):
            out.append({"index": i, "rt": h.rt, "rep": h.rep, "own": h.own, "num_lends": h.num_lends})
    return out


def emit_own(c):
    heap = TracingHeap(64)
    cx = mk_cx(heap)
    # An int rt, as build_type uses for the result cases and as the Go codec uses (OwnType(int),
    # lift_own's int rt check) — so serialize_table's rt is a plain int the fixture can carry (#728).
    t = OwnType(c.get("rt", 0))
    ptr = heap.realloc([0, 0, alignment(t, "i32"), elem_size(t, "i32")])[0]
    store(cx, c["rep"], t, ptr)  # lower_own: add owned handle, store its index
    idx = int.from_bytes(heap.memory[ptr:ptr + 4], "little")
    lower_table = serialize_table(cx.inst.handles)
    lifted = load(cx, ptr, t)  # lift_own: consume the handle, return the rep
    return {
        "name": c["name"], "rt": c.get("rt", 0), "rep": c["rep"],
        "lower": {"memory_hex": heap.memory.hex(), "index": idx, "table": lower_table, "realloc": heap.calls},
        "lift": {"rep": lifted, "table": serialize_table(cx.inst.handles)},
    }


def emit_shapes():
    heap = TracingHeap(64)
    cx = mk_cx(heap)
    rt = ResourceType(cx.inst)
    shapes = []
    for kind, t in (("own", OwnType(rt)), ("borrow", BorrowType(rt))):
        shapes.append({
            "kind": kind,
            "size": elem_size(t, "i32"),
            "align": alignment(t, "i32"),
            "flat": flatten_types([t], cx.opts),
        })
    return shapes


# Async-lower cases (gate:async slice-1 2a-i-A): the async `canon lower`'s SYNC-RESOLVING arm. The oracle
# for the async-lower ABI — flat signature (params + a retptr), the packed return `[RETURNED]` when the
# callee resolves inline, and the result lowered to the retptr. Driven **model-faithfully**: the ambient
# `current_thread`/`current_instance` `canon_lower` requires (definitions.py:2188) is established by the
# model's OWN construction — a sync outer export lifted through `Store.lift`/`invoke` (which builds the
# Task+Thread and resumes it), whose body drives the lower — never a stub that satisfies the lookup
# (#728's model-produced-state-only rule, first biting in the async tier). Bounded: one thread, no `tick`
# loop, since the outer lift is sync and the inner lower resolves inline.
ASYNC_LOWER_CASES = [
    {"name": "async-lower-u32-resolves-inline", "params": [{"kind": "u32"}], "result": {"kind": "u32"}, "args": [7], "ret_value": 107},
    {"name": "async-lower-empty-resolves-inline", "params": [], "result": None, "args": [], "ret_value": None},
    {"name": "async-lower-u64-resolves-inline", "params": [{"kind": "u64"}], "result": {"kind": "u64"}, "args": [42], "ret_value": 1000042},
]


def emit_async_lower(case):
    from definitions import FuncType, canon_lower, flatten_functype  # noqa: E402
    heap = TracingHeap(case.get("heap_size", 64))
    inst = ComponentInstance(Store())

    def mk_opts(async_):
        o = CanonicalOptions()
        o.memory = MemInst(heap.memory, "i32")
        o.string_encoding = "utf8"
        o.realloc = heap.realloc
        o.post_return = None
        o.sync_task_return = False
        o.async_ = async_
        o.callback = None
        return o

    ptypes = [(f"p{i}", build_type(p)) for i, p in enumerate(case["params"])]
    rtype = [build_type(case["result"])] if case["result"] is not None else []
    ft_inner = FuncType(ptypes, rtype, async_=True)
    opts_async = mk_opts(True)
    flat_ft = flatten_functype(opts_async, ft_inner, "lower")

    rv = case["ret_value"]

    def callee_inner(on_start, on_resolve):
        on_start()                                   # lift the lower's params (model-produced)
        on_resolve([rv] if rv is not None else [])   # resolve INLINE
        return lambda: None                          # on_cancel

    captured = {}

    def outer_core(flat_args):
        core_lower = inst.store.lower(callee_inner, ft_inner, opts_async, inst)
        args = list(case["args"])
        retptr = None
        if rtype:  # async results land at a retptr appended to the flat params
            retptr = heap.realloc([0, 0, alignment(rtype[0], "i32"), elem_size(rtype[0], "i32")])[0]
            args = args + [retptr]
        ret = core_lower(args)
        captured["ret"] = [int(x) for x in ret]
        captured["retptr"] = retptr
        return []

    outer_ft = FuncType([], [], async_=False)
    inst.store.invoke(inst.store.lift(outer_core, outer_ft, mk_opts(False), inst),
                      lambda: [], lambda r: None)

    return {
        "name": case["name"],
        "flat_params": list(flat_ft.params),
        "flat_results": list(flat_ft.results),
        "args": list(case["args"]),                    # the lower's flat params (before the retptr)
        "result_kind": case["result"]["kind"] if case["result"] is not None else None,
        "ret_value": rv,                               # the value the callee resolved with, lowered at retptr
        "ret": captured["ret"],                        # the packed core return ([RETURNED]=2 inline)
        "retptr": captured["retptr"],
        "memory_hex": heap.memory.hex(),               # result lowered at retptr
        "realloc": heap.calls,
    }


# Async-lower BLOCKING-arm cases (gate:async slice-1 2a-i-B): the async `canon lower`'s arm taken when the
# callee does NOT resolve inline. Same driver as the sync-resolving arm (a sync outer lifted through
# `Store.lift`/`invoke`, whose own `canon_lift` resume loop self-drives — no concurrent task, confirming
# the slice-1 scope ruling from the model's side, #739), but the callee STARTS then DEFERS resolve, so at
# lower time `subtask.resolved()` is False and the blocking arm is taken: the subtask is registered
# (`subtaski`) and the packed return is `[state | (subtaski<<4)]` (not `[RETURNED]`). The guest then joins
# the subtask to a waitable set, the callee's progress is armed (the captured `on_resolve` — through the
# model's own `resolve`→`on_progress`→`set_pending_event`), and `waitable-set.wait` delivers the
# `(SUBTASK, subtaski, state)` event as two u32 at a ptr.
#
# DELIBERATE OMISSION, load-bearing — do NOT add an "unsatisfiable wait" / deadlock case here. Every case
# below pins the resolve-and-wake path, and the guest-caused-deadlock outcome is intentionally absent. The
# model's sync-lift driver answers that condition with `trap_if(not candidates)` (definitions.py:2161)
# because an executable driver must terminate; contract H-4 deliberately DECLINES to arbitrate the same
# condition, making it a guest property with `Close`/fault as the only exits (Burroughs parks-and-hangs,
# does not trap — the "first hang" H-4 names; #739). The two answer different questions and neither is
# wrong. A future reader "completing" the battery with the missing trap case would be adding a defect: it
# would pin the model's trap as the expected outcome and make Burroughs' *compliant* hang read as a failure
# against its own oracle. The omission is the correct differential, not a gap.
ASYNC_LOWER_BLOCKING_CASES = [
    {"name": "async-lower-u32-blocks-then-resolves", "params": [{"kind": "u32"}], "result": {"kind": "u32"}, "args": [7], "ret_value": 107},
    {"name": "async-lower-empty-blocks-then-resolves", "params": [], "result": None, "args": [], "ret_value": None},
    {"name": "async-lower-u64-blocks-then-resolves", "params": [{"kind": "u64"}], "result": {"kind": "u64"}, "args": [42], "ret_value": 1000042},
]


def emit_async_lower_blocking(case):
    from definitions import (  # noqa: E402
        FuncType, flatten_functype,
        canon_waitable_set_new, canon_waitable_join, canon_waitable_set_wait,
    )
    heap = TracingHeap(case.get("heap_size", 64))
    inst = ComponentInstance(Store())

    def mk_opts(async_):
        o = CanonicalOptions()
        o.memory = MemInst(heap.memory, "i32")
        o.string_encoding = "utf8"
        o.realloc = heap.realloc
        o.post_return = None
        o.sync_task_return = False
        o.async_ = async_
        o.callback = None
        return o

    ptypes = [(f"p{i}", build_type(p)) for i, p in enumerate(case["params"])]
    rtype = [build_type(case["result"])] if case["result"] is not None else []
    ft_inner = FuncType(ptypes, rtype, async_=True)
    opts_async = mk_opts(True)
    flat_ft = flatten_functype(opts_async, ft_inner, "lower")

    rv = case["ret_value"]
    cap = {}

    def callee_inner(on_start, on_resolve):
        on_start()                 # lift the lower's params -> STARTED (model-produced)
        cap["on_resolve"] = on_resolve  # DEFER: the blocking arm — do NOT resolve inline
        return lambda: None             # on_cancel

    def outer_core(flat_args):
        core_lower = inst.store.lower(callee_inner, ft_inner, opts_async, inst)
        args = list(case["args"])
        retptr = None
        if rtype:  # async results land at a retptr appended to the flat params
            retptr = heap.realloc([0, 0, alignment(rtype[0], "i32"), elem_size(rtype[0], "i32")])[0]
            args = args + [retptr]
        packed = core_lower(args)
        cap["packed"] = [int(x) for x in packed]
        cap["retptr"] = retptr
        subtaski = cap["packed"][0] >> 4
        cap["subtaski"] = subtaski
        cap["state_at_lower"] = cap["packed"][0] & 0xf   # STARTING=0 or STARTED=1
        # Join the parked subtask to a waitable set, arm its resolution (the callee's progress), then wait.
        wset_i = canon_waitable_set_new()[0]
        canon_waitable_join(subtaski, wset_i)
        cap["on_resolve"]([rv] if rv is not None else [])
        eventptr = heap.realloc([0, 0, 4, 8])[0]      # two u32: (p1=subtaski, p2=state)
        code = canon_waitable_set_wait(opts_async.memory, wset_i, eventptr)[0]
        cap["event_code"] = int(code)
        cap["eventptr"] = eventptr
        cap["event_p1"] = int.from_bytes(heap.memory[eventptr:eventptr + 4], "little")
        cap["event_p2"] = int.from_bytes(heap.memory[eventptr + 4:eventptr + 8], "little")
        return []

    outer_ft = FuncType([], [], async_=False)
    inst.store.invoke(inst.store.lift(outer_core, outer_ft, mk_opts(False), inst),
                      lambda: [], lambda r: None)

    return {
        "name": case["name"],
        "flat_params": list(flat_ft.params),
        "flat_results": list(flat_ft.results),
        "args": list(case["args"]),
        "result_kind": case["result"]["kind"] if case["result"] is not None else None,
        "ret_value": rv,
        "packed": cap["packed"],                # the blocking packed return [state | (subtaski<<4)]
        "subtaski": cap["subtaski"],
        "state_at_lower": cap["state_at_lower"],
        "retptr": cap["retptr"],
        "event_code": cap["event_code"],        # EventCode.SUBTASK = 1
        "event_ptr": cap["eventptr"],
        "event_p1": cap["event_p1"],            # subtaski
        "event_p2": cap["event_p2"],            # resolved state (RETURNED = 2)
        "memory_hex": heap.memory.hex(),        # result lowered at retptr + event stored at event_ptr
        "realloc": heap.calls,
    }


# Future.read cases (gate:async slice-1 increment 3, first slice — `future<T>` is the degenerate `stream<T>`
# in the CABI, so the shared copy substrate lands here on the single-value case). The oracle for the async
# `future.read` path: the read returns BLOCKED, the outcome is delivered as a `(FUTURE_READ, i, payload)`
# where the payload is the `CopyResult` the guest branches on (definitions.py:919 COMPLETED=0/DROPPED=1/
# CANCELLED=2), and the readable end's STATE after differs by outcome. Driven model-faithfully with the same
# sync outer + waitable-set loop as the blocking-arm oracle: read async -> BLOCKED (pending), then the
# producer/cancel fires on the same task, then the event is retrieved.
#
# TWO read outcomes are pinned, and the OUTCOME SET IS PER-DIRECTION (found by running the model, not from
# the WIT surface): a pending future READ is only ever COMPLETED or CANCELLED. DROPPED is a future.*write*
# outcome — `shared.drop` notifies a pending write (a ReadableBuffer), and `WritableFutureEnd.drop` traps
# unless the writable end is already DONE, so a read can never be dropped. DROPPED is pinned when future.write
# lands, not here.
#
# CANCELLED is a DELIBERATE INCLUSION beyond the first slice's guest scope (contrast the deliberate OMISSION
# of the deadlock case in the async-lower blocking oracle): the #734 stdio guest binds `future.read`/`drop`
# but NOT `future.cancel-read`, so COMPLETED is its only reachable outcome. CANCELLED is pinned anyway to
# FORCE the codec's CopyResult encoding to DISTINGUISH the outcomes (payload 2, end state IDLE) rather than
# hardcode success (payload 0, end state DONE) — a success-only fixture is green against a codec that cannot
# tell a cancelled read from a completed one, and the end-state half of that (IDLE vs DONE) mis-routes the
# NEXT operation's trap-legality far from the cause. Do not prune it as unused; the inclusion is the guard.
FUTURE_READ_CASES = [
    {"name": "future-read-u32-completed", "value_type": {"kind": "u32"}, "outcome": "completed", "value": 107},
    {"name": "future-read-u32-cancelled", "value_type": {"kind": "u32"}, "outcome": "cancelled"},
]


# Context.get/set (gate:async increment 4, the first op the real guest hits — #739). Per-thread (per-agent)
# i32 storage of a fixed number of slots (definitions.py: thread.storage = [0,0], canon_context_get/set at
# def:2293/2303), get returning 0 for an unset slot and the stored value after a set, bounds i < len. No
# byte encoding — it is slot storage — so the pin is behavioral (roundtrip, unset-zero, slot count); it is
# pinned anyway, per the discipline that a fast iterate-to-green loop does not skip an op's model pin.
def emit_context_ops():
    from definitions import (  # noqa: E402
        FuncType, canon_context_get, canon_context_set,
    )
    heap = TracingHeap(64)
    inst = ComponentInstance(Store())

    def mk_opts(async_):
        o = CanonicalOptions()
        o.memory = MemInst(heap.memory, "i32")
        o.string_encoding = "utf8"
        o.realloc = heap.realloc
        o.post_return = None
        o.sync_task_return = False
        o.async_ = async_
        o.callback = None
        return o

    cap = {}

    def outer_core(_flat):
        from definitions import current_thread
        cap["slots"] = len(current_thread().storage)          # the fixed slot count (thread.storage init)
        cap["get0_unset"] = int(canon_context_get("i32", 0)[0])  # unset -> 0
        canon_context_set("i32", 0, 107)
        cap["get0_after_set"] = int(canon_context_get("i32", 0)[0])  # -> 107
        cap["get1_unset"] = int(canon_context_get("i32", 1)[0])  # a distinct slot stays 0
        return []

    inst.store.invoke(inst.store.lift(outer_core, FuncType([], [], async_=False), mk_opts(False), inst),
                      lambda: [], lambda r: None)
    return {
        "slots": cap["slots"],                 # number of per-agent context slots (bounds: i < slots)
        "get0_unset": cap["get0_unset"],        # get before any set -> 0
        "set_value": 107,
        "get0_after_set": cap["get0_after_set"],  # get slot 0 after set(0, 107) -> 107
        "get1_unset": cap["get1_unset"],        # slot 1 unaffected by writing slot 0 -> 0
    }


# Stream.new (gate:async increment 4). The first op that creates BOTH ends of a stream inside the guest —
# inverting every prior slice's host-provided end. definitions.py:2451: it adds a ReadableStreamEnd and a
# WritableStreamEnd over one SharedStreamImpl and returns `ri | (wi<<32)`, both ends IDLE. In the real guest
# (p3async-hello) the guest keeps the writable end (stream.write) and hands the READABLE end to the host via
# a lowered write-via-stream(ri): the host reads ri, which — whichever side arrives second in the shared
# impl drives on_copy_done (def:992) — drives the guest's write completion. So this is the first guest->host
# copy flow, and the write's on_copy_done is driven by the host's READ, not a direct completion (#739 note).
def emit_stream_new():
    from definitions import (  # noqa: E402
        U8Type, StreamType, FuncType, canon_stream_new,
    )
    heap = TracingHeap(64)
    inst = ComponentInstance(Store())

    def mk_opts(async_):
        o = CanonicalOptions()
        o.memory = MemInst(heap.memory, "i32")
        o.string_encoding = "utf8"
        o.realloc = heap.realloc
        o.post_return = None
        o.sync_task_return = False
        o.async_ = async_
        o.callback = None
        return o

    cap = {}

    def outer_core(_flat):
        packed = canon_stream_new(StreamType(U8Type()))[0]
        ri, wi = packed & 0xffffffff, packed >> 32
        cap["packed"] = packed
        cap["ri"] = ri
        cap["wi"] = wi
        cap["ri_state"] = inst.handles.get(ri).state.name
        cap["wi_state"] = inst.handles.get(wi).state.name
        return []

    inst.store.invoke(inst.store.lift(outer_core, FuncType([], [], async_=False), mk_opts(False), inst),
                      lambda: [], lambda r: None)
    return {
        "packed": cap["packed"],       # ri | (wi<<32) — the i64 the guest destructures
        "ri": cap["ri"],               # readable end (handed to the host in the guest->host flow)
        "wi": cap["wi"],               # writable end (the guest writes to it)
        "ri_state": cap["ri_state"],   # IDLE at creation
        "wi_state": cap["wi_state"],   # IDLE at creation
    }


def emit_future_read(case):
    from definitions import (  # noqa: E402
        FuncType, FutureType,
        canon_future_new, canon_future_read, canon_future_write, canon_future_cancel_read,
        canon_waitable_set_new, canon_waitable_join, canon_waitable_set_wait,
    )
    heap = TracingHeap(case.get("heap_size", 256))
    inst = ComponentInstance(Store())

    def mk_opts(async_):
        o = CanonicalOptions()
        o.memory = MemInst(heap.memory, "i32")
        o.string_encoding = "utf8"
        o.realloc = heap.realloc
        o.post_return = None
        o.sync_task_return = False
        o.async_ = async_
        o.callback = None
        return o

    t = build_type(case["value_type"])
    future_t = FutureType(t)
    cap = {}

    def outer_core(_flat):
        packed = canon_future_new(future_t)[0]
        ri, wi = packed & 0xffffffff, packed >> 32
        dstptr = heap.realloc([0, 0, alignment(t, "i32"), elem_size(t, "i32")])[0]
        cap["read_ret"] = [int(x) for x in canon_future_read(future_t, mk_opts(True), ri, dstptr)]
        if case["outcome"] == "completed":
            wset = canon_waitable_set_new()[0]
            canon_waitable_join(ri, wset)
            srcptr = heap.realloc([0, 0, alignment(t, "i32"), elem_size(t, "i32")])[0]
            store(mk_cx(heap), case["value"], t, srcptr)
            canon_future_write(future_t, mk_opts(True), wi, srcptr)
            evptr = heap.realloc([0, 0, 4, 8])[0]
            cap["event_code"] = int(canon_waitable_set_wait(mk_opts(True).memory, wset, evptr)[0])
            cap["event_p1"] = int.from_bytes(heap.memory[evptr:evptr + 4], "little")
            cap["payload"] = int.from_bytes(heap.memory[evptr + 4:evptr + 8], "little")
            cap["dst_val"] = int.from_bytes(heap.memory[dstptr:dstptr + int(elem_size(t, "i32"))], "little")
        else:  # cancelled: cancel-read returns the CANCELLED payload directly (it consumes the event)
            cap["payload"] = int(canon_future_cancel_read(future_t, False, ri)[0])
        cap["ri"] = ri
        cap["ri_state"] = inst.handles.get(ri).state.name
        return []

    inst.store.invoke(inst.store.lift(outer_core, FuncType([], [], async_=False), mk_opts(False), inst),
                      lambda: [], lambda r: None)

    return {
        "name": case["name"],
        "value_type": case["value_type"]["kind"],
        "outcome": case["outcome"],
        "read_ret": cap["read_ret"],                 # [BLOCKED] = [0xffffffff] — the async read parks
        "subtaski": cap["ri"],                        # the readable end's handle index
        "event_code": cap.get("event_code"),         # EventCode.FUTURE_READ = 4 (completed path only)
        "event_p1": cap.get("event_p1"),              # the end index in the delivered event
        "payload": cap["payload"],                    # CopyResult: COMPLETED=0 / CANCELLED=2
        "ri_state": cap["ri_state"],                  # DONE (completed) vs IDLE (cancelled) — the second guard
        "value": case.get("value"),
        "dst_val": cap.get("dst_val"),                # the copied value at the read ptr (completed only)
        "memory_hex": heap.memory.hex(),
        "realloc": heap.calls,
    }


# Stream.write cases (gate:async increment 3, stream write-side slice). `stream_copy` is `future_copy`
# generalized to n elements with PROGRESS: the write event packs `result | (progress<<4)` (def:2500), not a
# bare CopyResult. That is the hazard stream adds — a guest reading only the low bits is right on a full copy
# and WRONG on a partial one — so a partial case (a consumer taking fewer elements than offered) is pinned
# beside the full one, the same class as the empty-list realloc and the variant discriminant. DROPPED lands
# here (the write side's outcome future.read could not reach) with its end state, as COMPLETED/CANCELLED did
# for the read. Unlike future, a COMPLETED stream write leaves the end IDLE (open for more); only DROPPED is
# DONE. Driven model-faithfully: an async write of n elements parks (BLOCKED); a reader consumes n (full) or
# m<n (partial), or the readable end drops (DROPPED); waitable-set.wait delivers the STREAM_WRITE event. The
# reader is the model's own stream.read producing the state — NOT a claim the engine binds stream.read (the
# guest binds only the write side; stream.read is refused by name in the Go slice).
STREAM_WRITE_CASES = [
    {"name": "stream-write-u8-full-completed", "outcome": "full", "n": 4},
    {"name": "stream-write-u8-partial-progress", "outcome": "partial", "n": 4, "m": 2},
    {"name": "stream-write-u8-dropped", "outcome": "dropped", "n": 4},
]


def emit_stream_write(case):
    from definitions import (  # noqa: E402
        U8Type, StreamType, FuncType,
        canon_stream_new, canon_stream_write, canon_stream_read, canon_stream_drop_readable,
        canon_waitable_set_new, canon_waitable_join, canon_waitable_set_wait,
    )
    heap = TracingHeap(case.get("heap_size", 256))
    inst = ComponentInstance(Store())

    def mk_opts(async_):
        o = CanonicalOptions()
        o.memory = MemInst(heap.memory, "i32")
        o.string_encoding = "utf8"
        o.realloc = heap.realloc
        o.post_return = None
        o.sync_task_return = False
        o.async_ = async_
        o.callback = None
        return o

    st = StreamType(U8Type())
    n = case["n"]
    cap = {}

    def outer_core(_flat):
        packed = canon_stream_new(st)[0]
        ri, wi = packed & 0xffffffff, packed >> 32
        srcptr = heap.realloc([0, 0, 1, n])[0]
        for k in range(n):
            heap.memory[srcptr + k] = 65 + k  # "ABCD…" — the bytes the guest writes
        cap["write_ret"] = [int(x) for x in canon_stream_write(st, mk_opts(True), wi, srcptr, n)]
        wset = canon_waitable_set_new()[0]
        canon_waitable_join(wi, wset)
        if case["outcome"] == "full":
            dst = heap.realloc([0, 0, 1, n])[0]
            canon_stream_read(st, mk_opts(True), ri, dst, n)
        elif case["outcome"] == "partial":
            dst = heap.realloc([0, 0, 1, case["m"]])[0]
            canon_stream_read(st, mk_opts(True), ri, dst, case["m"])
        elif case["outcome"] == "dropped":
            canon_stream_drop_readable(st, ri)
        evptr = heap.realloc([0, 0, 4, 8])[0]
        cap["event_code"] = int(canon_waitable_set_wait(mk_opts(True).memory, wset, evptr)[0])
        cap["event_p1"] = int.from_bytes(heap.memory[evptr:evptr + 4], "little")
        cap["packed"] = int.from_bytes(heap.memory[evptr + 4:evptr + 8], "little")
        cap["wi"] = wi
        cap["wi_state"] = inst.handles.get(wi).state.name
        return []

    inst.store.invoke(inst.store.lift(outer_core, FuncType([], [], async_=False), mk_opts(False), inst),
                      lambda: [], lambda r: None)

    packed = cap["packed"]
    return {
        "name": case["name"],
        "outcome": case["outcome"],
        "n": n,
        "m": case.get("m"),
        "write_ret": cap["write_ret"],            # [BLOCKED] — the async write parks
        "wi": cap["wi"],                           # the writable end's handle index
        "event_code": cap["event_code"],           # EventCode.STREAM_WRITE = 3
        "event_p1": cap["event_p1"],               # the end index in the delivered event
        "packed": packed,                          # result | (progress<<4)
        "result": packed & 0xf,                    # CopyResult: COMPLETED=0 / DROPPED=1
        "progress": packed >> 4,                   # elements copied — the low-bits-only hazard
        "wi_state": cap["wi_state"],               # IDLE (completed, open) vs DONE (dropped) — the 2nd guard
        "memory_hex": heap.memory.hex(),
        "realloc": heap.calls,
    }


def main():
    with open(os.path.join(os.path.dirname(__file__), "cases.json")) as f:
        cases = json.load(f)
    out = {
        "pin": PIN,
        "cases": [emit(c) for c in cases],
        "handles": [emit_own(c) for c in OWN_CASES],
        "async_lowers": [emit_async_lower(c) for c in ASYNC_LOWER_CASES],
        "async_lower_blocking": [emit_async_lower_blocking(c) for c in ASYNC_LOWER_BLOCKING_CASES],
        "future_reads": [emit_future_read(c) for c in FUTURE_READ_CASES],
        "stream_writes": [emit_stream_write(c) for c in STREAM_WRITE_CASES],
        "context_ops": emit_context_ops(),  # gate:async increment 4: context.get/set (the guest's first op)
        "stream_new": emit_stream_new(),  # gate:async increment 4: stream.new (the guest->host inversion)
        "shapes": emit_shapes(),
    }
    json.dump(out, sys.stdout, indent=2)
    sys.stdout.write("\n")


if __name__ == "__main__":
    main()
