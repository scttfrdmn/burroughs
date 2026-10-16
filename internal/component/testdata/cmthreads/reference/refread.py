#!/usr/bin/env python3
# Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
#
# REFERENCE READINGS for the 🧵 fixtures: each scenario mirrors a committed cmthreads fixture, written in Python
# against the pinned component-model reference, `definitions.py` @ CANON_PIN. The scenarios are oracles for the
# cases wasmtime 49.0.2 cannot read (ADR 0101's Option A amendment; ADR 0102: the pin defines the semantics).
# Their output is labelled REFERENCE READING so it is never mistaken for a wasmtime reading.
#
# Run offline, never in CI (the vendored file is fetched by `make canon-fixtures` into a gitignored dir):
#
#   uv run --no-project python3 internal/component/testdata/cmthreads/reference/refread.py > \
#     internal/component/testdata/cmthreads/reference/refread.reading
#
# # Calibration before reliance (the chair's ruling)
#
# `wait-runs-ready` is a fixture wasmtime DOES read (5). Its scenario runs first, and the method is trusted
# only if it gives the same value. A scenario that has not been calibrated is not an oracle.
#
# # How a trap is observed, because the pin does not propagate one across Threads
#
# `trap()` raises `Trap(BaseException)`. A Thread's continuation runs on its own Python thread
# (`cont_new`'s `thread_base`), which has no handler, so a Trap there ends that Python thread and leaves its
# resumer blocked on `handler.lock` forever. The computation is over, which is ADR 0100's reading, but nothing
# returns. So each scenario runs on a watched thread, and `threading.excepthook` records a Trap from ANY
# thread: the outcome is the first Trap's site, or the call's result, or HANG after the bound.
#
# # Each outcome is a set, not one run
#
# The reference schedules with `random.choice` and, unless DETERMINISTIC_PROFILE, returns early from
# `wait_until` at random. Every scenario runs under both profile settings and several seeds, and the reading
# is the set of outcomes it produced. A reading with more than one element is reported as such.

import os
import random
import sys
import threading

PIN = "2bed77e4228841c1d2721996d3ecc169ff96b158"  # the Makefile's CANON_PIN
VENDOR = os.path.join(os.path.dirname(__file__), "..", "..", "..", "canon", "gen", "vendor")
sys.path.insert(0, VENDOR)

import definitions as d  # noqa: E402

SEEDS = range(8)
BOUND_S = 5.0


class Outcome:
    def __init__(self):
        self.done = threading.Event()
        self.value = None


def watch(body):
    """Run body(out, site) on a watched thread; return 'value N', 'trap SITE', or 'HANG'."""
    out = Outcome()
    site = {"name": "?"}

    def hook(args):
        if issubclass(args.exc_type, d.Trap):
            if out.value is None:
                out.value = "trap " + site["name"]
            out.done.set()
        else:  # an engine or harness error, not a trap: report it loudly
            out.value = "ERROR %s: %s" % (args.exc_type.__name__, args.exc_value)
            out.done.set()

    threading.excepthook = hook
    t = threading.Thread(target=lambda: body(out, site), daemon=True)
    t.start()
    if not out.done.wait(BOUND_S):
        return "HANG"
    return out.value


def read(name, body):
    seen = set()
    for det in (False, True):
        d.DETERMINISTIC_PROFILE = det
        for seed in SEEDS:
            random.seed(seed)
            seen.add(watch(body))
    print("%s: %s" % (name, " | ".join(sorted(seen))))


def u32(mem, p):
    return int.from_bytes(mem.bytes[p:p + 4], "little")


def run_to_resolution(store, fi, out):
    """The embedder: invoke fi and tick the store until it resolves."""
    got = []
    store.invoke(fi, lambda: [], lambda r: got.append(r))
    while not got:
        store.tick()
    out.value = "value %d" % got[0][0]
    out.done.set()


def child_g_and_release(store):
    """The nested child both fixtures use: async `g` YIELDs until a flag that its sync `release` sets."""
    inst = d.ComponentInstance(store)
    flag = {"set": False}
    gft = d.FuncType([], [d.U32Type()], async_=True)

    def g(args):
        return [1]  # YIELD

    def g_cb(args):
        if flag["set"]:
            d.canon_task_return([d.U32Type()], d.LiftOptions(), [5])
            return [0]  # EXIT
        return [1]

    def release(args):
        flag["set"] = True
        return []

    relft = d.FuncType([], [])
    g_fi = store.lift(g, gft, d.CanonicalOptions(async_=True, callback=g_cb), inst)
    rel_fi = store.lift(release, relft, d.CanonicalOptions(), inst)
    return g_fi, gft, rel_fi, relft


BODYT = d.CoreFuncType(["i32"], [])


def wait_runs_ready(out, site):
    """cmthreads/wait-runs-ready.wat. wasmtime 49.0.2: 5. CALIBRATION: the reference must agree."""
    store = d.Store()
    g_fi, gft, rel_fi, relft = child_g_and_release(store)
    inst = d.ComponentInstance(store)
    mem = d.MemInst(bytearray(65536), "i32")
    glow = store.lower(g_fi, gft, d.CanonicalOptions(async_=True, memory=mem), inst)
    rellow = store.lower(rel_fi, relft, d.CanonicalOptions(), inst)
    tbl = d.Table()
    fi = tbl.add(d.CoreFuncRef(BODYT, lambda args: (rellow([]), [])[1]))  # T: let g finish

    def run(args):
        [t] = d.canon_thread_new_indirect(BODYT, tbl, fi, 0)
        d.canon_thread_resume_later(t)
        [p] = glow([32])
        [s] = d.canon_waitable_set_new()
        d.canon_waitable_join(p >> 4, s)
        return [2 | (s << 4)]  # WAIT on s

    def cb(args):
        d.canon_task_return([d.U32Type()], d.LiftOptions(), [u32(mem, 32)])
        return [0]

    rft = d.FuncType([], [d.U32Type()], async_=True)
    run_fi = store.lift(run, rft, d.CanonicalOptions(async_=True, callback=cb), inst)
    run_to_resolution(store, run_fi, out)


def wait_runs_ready_control(out, site):
    """The same with T never readied: `run` cannot finish."""
    store = d.Store()
    g_fi, gft, rel_fi, relft = child_g_and_release(store)
    inst = d.ComponentInstance(store)
    mem = d.MemInst(bytearray(65536), "i32")
    glow = store.lower(g_fi, gft, d.CanonicalOptions(async_=True, memory=mem), inst)
    rellow = store.lower(rel_fi, relft, d.CanonicalOptions(), inst)
    tbl = d.Table()
    fi = tbl.add(d.CoreFuncRef(BODYT, lambda args: (rellow([]), [])[1]))

    def run(args):
        d.canon_thread_new_indirect(BODYT, tbl, fi, 0)  # created, never readied
        [p] = glow([32])
        [s] = d.canon_waitable_set_new()
        d.canon_waitable_join(p >> 4, s)
        return [2 | (s << 4)]

    def cb(args):
        d.canon_task_return([d.U32Type()], d.LiftOptions(), [u32(mem, 32)])
        return [0]

    rft = d.FuncType([], [d.U32Type()], async_=True)
    run_fi = store.lift(run, rft, d.CanonicalOptions(async_=True, callback=cb), inst)
    run_to_resolution(store, run_fi, out)


def trap_while_waiting(t2_traps):
    """cmthreads/trap-while-waiting.wat (t2_traps) and its completing control (`unreachable` -> `nop`)."""

    def body(out, site):
        store = d.Store()
        g_fi, gft, rel_fi, relft = child_g_and_release(store)
        inst = d.ComponentInstance(store)
        mem = d.MemInst(bytearray(65536), "i32")
        glow = store.lower(g_fi, gft, d.CanonicalOptions(async_=True, memory=mem), inst)
        rellow = store.lower(rel_fi, relft, d.CanonicalOptions(), inst)
        wswait = lambda s, ptr: d.canon_waitable_set_wait(mem, s, ptr)  # noqa: E731
        marked = {"t1": False}

        def t1(args):
            [p] = glow([32])
            if p & 0xF != 2:
                [s] = d.canon_waitable_set_new()
                d.canon_waitable_join(p >> 4, s)
                wswait(s, 72)
            marked["t1"] = True
            return []

        def t2(args):
            if t2_traps:
                site["name"] = "t2"
                d.trap()  # the fixture's `unreachable`
            rellow([])
            return []

        tbl = d.Table()
        f1 = tbl.add(d.CoreFuncRef(BODYT, t1))
        f2 = tbl.add(d.CoreFuncRef(BODYT, t2))

        def run(args):
            [a] = d.canon_thread_new_indirect(BODYT, tbl, f1, 0)
            [b] = d.canon_thread_new_indirect(BODYT, tbl, f2, 0)
            d.canon_thread_resume_later(a)
            d.canon_thread_resume_later(b)
            return [1]  # YIELD

        def cb(args):
            if marked["t1"]:
                d.canon_task_return([d.U32Type()], d.LiftOptions(), [1])
                return [0]
            return [1]

        rft = d.FuncType([], [d.U32Type()], async_=True)
        run_fi = store.lift(run, rft, d.CanonicalOptions(async_=True, callback=cb), inst)
        run_to_resolution(store, run_fi, out)

    return body


def nested_sync_unwind(t_traps):
    """c': a nested child C's SYNC lift `h` starts a grandchild's async `g`, readies its Thread T, and waits
    on g's subtask; T would `release` g but traps first (t_traps). The parent's async `run` calls `h`
    synchronously. ADR 0101 point 7's prose expected T's trap; the reference decides."""

    def body(out, site):
        store = d.Store()
        g_fi, gft, rel_fi, relft = child_g_and_release(store)
        c = d.ComponentInstance(store)
        cmem = d.MemInst(bytearray(65536), "i32")
        glow = store.lower(g_fi, gft, d.CanonicalOptions(async_=True, memory=cmem), c)
        rellow = store.lower(rel_fi, relft, d.CanonicalOptions(), c)

        def t(args):
            if t_traps:
                site["name"] = "T"
                d.trap()
            rellow([])
            return []

        tbl = d.Table()
        ft_ = tbl.add(d.CoreFuncRef(BODYT, t))

        def h(args):
            [p] = glow([32])
            [s] = d.canon_waitable_set_new()
            d.canon_waitable_join(p >> 4, s)
            [ti] = d.canon_thread_new_indirect(BODYT, tbl, ft_, 0)
            d.canon_thread_resume_later(ti)
            site["name"] = "h's wait (the sync tail)"  # a trap with no other site set is the sync tail's
            d.canon_waitable_set_wait(cmem, s, 72)
            return [u32(cmem, 32)]

        hft = d.FuncType([], [d.U32Type()])
        h_fi = store.lift(h, hft, d.CanonicalOptions(), c)

        p = d.ComponentInstance(store)
        hlow = store.lower(h_fi, hft, d.CanonicalOptions(), p)

        def run(args):
            [v] = hlow([])
            d.canon_task_return([d.U32Type()], d.LiftOptions(), [v])
            return [0]

        def cb(args):
            return [0]

        rft = d.FuncType([], [d.U32Type()], async_=True)
        run_fi = store.lift(run, rft, d.CanonicalOptions(async_=True, callback=cb), p)
        run_to_resolution(store, run_fi, out)

    return body


def residual(t2_sets_flag):
    """d, cmthreads/residual.wat (#977's residual): T1 waits on a child's `g`, `g` WAITs on the parent's own
    async `h2`, and `h2` YIELDs until a flag only T2 sets. wasmtime 49.0.2 hits its "delivering non-present
    event" bug on this shape, so this reading is the oracle. Control (not t2_sets_flag): T2 does nothing."""

    def body(out, site):
        store = d.Store()
        p = d.ComponentInstance(store)
        pmem = d.MemInst(bytearray(65536), "i32")
        flag = {"set": False, "t1": False}
        hft = d.FuncType([], [d.U32Type()], async_=True)

        def h2(args):
            return [1]  # YIELD

        def h2_cb(args):
            if flag["set"]:
                d.canon_task_return([d.U32Type()], d.LiftOptions(), [7])
                return [0]
            return [1]

        h2_fi = store.lift(h2, hft, d.CanonicalOptions(async_=True, callback=h2_cb), p)

        c = d.ComponentInstance(store)
        cmem = d.MemInst(bytearray(65536), "i32")
        hlow = store.lower(h2_fi, hft, d.CanonicalOptions(async_=True, memory=cmem), c)

        def g(args):
            [q] = hlow([16])
            if q & 0xF == 2:
                d.canon_task_return([d.U32Type()], d.LiftOptions(), [u32(cmem, 16) + 1])
                return [0]
            [s] = d.canon_waitable_set_new()
            d.canon_waitable_join(q >> 4, s)
            return [2 | (s << 4)]  # WAIT

        def g_cb(args):
            d.canon_task_return([d.U32Type()], d.LiftOptions(), [u32(cmem, 16) + 1])
            return [0]

        g_fi = store.lift(g, hft, d.CanonicalOptions(async_=True, callback=g_cb), c)
        glow = store.lower(g_fi, hft, d.CanonicalOptions(async_=True, memory=pmem), p)

        def t1(args):
            [q] = glow([24])
            if q & 0xF != 2:
                [s] = d.canon_waitable_set_new()
                d.canon_waitable_join(q >> 4, s)
                d.canon_waitable_set_wait(pmem, s, 32)
            flag["t1"] = True
            return []

        def t2(args):
            if t2_sets_flag:
                flag["set"] = True
            return []

        tbl = d.Table()
        f1 = tbl.add(d.CoreFuncRef(BODYT, t1))
        f2 = tbl.add(d.CoreFuncRef(BODYT, t2))

        def run(args):
            [a] = d.canon_thread_new_indirect(BODYT, tbl, f1, 0)
            [b] = d.canon_thread_new_indirect(BODYT, tbl, f2, 0)
            d.canon_thread_resume_later(a)
            d.canon_thread_resume_later(b)
            return [1]

        def cb(args):
            if flag["t1"]:
                d.canon_task_return([d.U32Type()], d.LiftOptions(), [u32(pmem, 24)])
                return [0]
            return [1]

        run_fi = store.lift(run, hft, d.CanonicalOptions(async_=True, callback=cb), p)
        run_to_resolution(store, run_fi, out)

    return body


def sync_wait_own_thread(t_writes):
    """#1005's admitted case, cmthreads/sync-wait-own-thread.wat: a sync lift `h` waits on a future that a
    Thread of its OWN instance writes, so the sync tail has a candidate and the wait completes. Control (not
    t_writes): T does not write, so no candidate remains and the tail traps."""

    def body(out, site):
        store = d.Store()
        inst = d.ComponentInstance(store)
        mem = d.MemInst(bytearray(65536), "i32")
        fut = d.FutureType(d.U8Type())
        aopts = d.CanonicalOptions(async_=True, memory=mem)

        def t(args):
            if t_writes:
                mem.bytes[80] = 7
                d.canon_future_write(fut, aopts, u32(mem, 20), 80)
            return []

        tbl = d.Table()
        fi = tbl.add(d.CoreFuncRef(BODYT, t))

        def h(args):
            [packed] = d.canon_future_new(fut)
            mem.bytes[16:20] = (packed & 0xFFFFFFFF).to_bytes(4, "little")
            mem.bytes[20:24] = (packed >> 32).to_bytes(4, "little")
            [r] = d.canon_future_read(fut, aopts, u32(mem, 16), 64)
            if r == d.BLOCKED:
                [s] = d.canon_waitable_set_new()
                d.canon_waitable_join(u32(mem, 16), s)
                [ti] = d.canon_thread_new_indirect(BODYT, tbl, fi, 0)
                d.canon_thread_resume_later(ti)
                site["name"] = "h's wait (the sync tail)"
                d.canon_waitable_set_wait(mem, s, 72)
            return [mem.bytes[64]]

        hft = d.FuncType([], [d.U32Type()])
        h_fi = store.lift(h, hft, d.CanonicalOptions(), inst)
        got = []
        store.invoke(h_fi, lambda: [], lambda r: got.append(r))
        out.value = "value %d" % got[0][0]
        out.done.set()

    return body


if __name__ == "__main__":
    import hashlib

    with open(os.path.join(VENDOR, "definitions.py"), "rb") as fh:
        digest = hashlib.sha256(fh.read()).hexdigest()
    print("# REFERENCE READING — definitions.py @ %s, not a wasmtime reading." % PIN)
    print("# vendored file sha256 %s" % digest)
    print("# Each line is the set of outcomes over DETERMINISTIC_PROFILE in {False, True} x seeds %s." % list(SEEDS))
    print("# HANG means no result and no trap within %.0fs." % BOUND_S)
    read("wait-runs-ready (calibration; wasmtime 49.0.2: 5)", wait_runs_ready)
    read("wait-runs-ready control (T never readied)", wait_runs_ready_control)
    read("trap-while-waiting (wasmtime 49.0.2: trap in t2)", trap_while_waiting(True))
    read("trap-while-waiting control (unreachable -> nop)", trap_while_waiting(False))
    read("nested-sync-unwind (T traps)", nested_sync_unwind(True))
    read("nested-sync-unwind control (T releases)", nested_sync_unwind(False))
    read("residual (#977; wasmtime 49.0.2: BUG)", residual(True))
    read("residual control (T2 does nothing)", residual(False))
    read("sync-wait-own-thread (#1005's admitted case; wasmtime 49.0.2: 7)", sync_wait_own_thread(True))
    read("sync-wait-own-thread control (T does not write)", sync_wait_own_thread(False))
