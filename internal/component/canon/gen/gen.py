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


def main():
    with open(os.path.join(os.path.dirname(__file__), "cases.json")) as f:
        cases = json.load(f)
    out = {
        "pin": PIN,
        "cases": [emit(c) for c in cases],
        "handles": [emit_own(c) for c in OWN_CASES],
        "shapes": emit_shapes(),
    }
    json.dump(out, sys.stdout, indent=2)
    sys.stdout.write("\n")


if __name__ == "__main__":
    main()
