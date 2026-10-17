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
#   uv run --no-project --managed-python --python 3.14.8 python3 \
#     internal/component/testdata/cmthreads/reference/refread.py > \
#     internal/component/testdata/cmthreads/reference/refread.reading
#
# The interpreter is named exactly (the Makefile's CANON_PYTHON): the pin needs >= 3.14, and an unpinned
# `python3` once resolved to a Homebrew 3.15.0 and once to a uv-managed 3.12.2.
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

PIN = "2e7895c76905cd3fb7e6be4ac632258972b1f688"  # the Makefile's CANON_PIN (from 2bed77e, 2026-10-17)
VENDOR = os.path.join(os.path.dirname(__file__), "..", "..", "..", "canon", "gen", "vendor")
# REFREAD_DEFINITIONS reads a definitions.py other than the vendored pin, to compare a candidate pin against
# the current one row by row. Unset, the vendored pin is read.
DEFINITIONS = os.environ.get("REFREAD_DEFINITIONS") or os.path.join(VENDOR, "definitions.py")
if os.environ.get("REFREAD_DEFINITIONS"):
    import importlib.util

    _spec = importlib.util.spec_from_file_location("definitions", DEFINITIONS)
    d = importlib.util.module_from_spec(_spec)
    sys.modules["definitions"] = d
    _spec.loader.exec_module(d)
else:
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


def host_invoke(store, fi, on_resolve):
    """Invoke fi from inside a HOST Thread, as upstream's own `mk_host_func` drives host-side work.

    The embedder's Python thread has no current Thread. The pin never asked for one, but head's `trap()`
    does (`current_instance().store.lock_down()`, upstream a25fc0b37), so a sync-tail trap raised there was
    an AttributeError from inside `trap()` rather than a trap. Inside a host Thread, `current_instance()`
    is the host's, the store locks down, and the Trap propagates out to the watch as one. The pin reads
    identically either way: the calibration line and every committed reading were re-taken through this."""
    host = d.ComponentInstance(store)
    task = d.Task(d.FuncType([], []), d.CanonicalOptions(), host, lambda: [], lambda _: None)

    def body():
        store.invoke(fi, lambda: [], on_resolve)
        return None  # head reads a Thread function's result as the Thread to switch to; there is none

    d.Thread(task, body).resume()


def run_to_resolution(store, fi, out):
    """The embedder: invoke fi (from a host Thread, see host_invoke) and tick the store until it resolves."""
    got = []
    host_invoke(store, fi, lambda r: got.append(r))
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
        host_invoke(store, h_fi, lambda r: got.append(r))
        out.value = "value %d" % got[0][0]
        out.done.set()

    return body


def sync_wait_host_import(eager):
    """ADR 0102's sync-wait row with gate:cm-threads off: `async-waitset-synth`, `run-slot` and
    `result-store-heap` share this prefix. A SYNC lift `run` async-lowers an imported `op(7)` into retptr 0,
    returns mem[0] at once if it RETURNED, else joins the subtask to a set and waits. `op` is a provider
    instance's callback lift that YIELDs once and then returns x (eager: returns x on entry, so the wait is
    never reached). A run that reaches the wait and is admitted reads 1000 + mem[0], so it cannot pass for
    the early return's 7."""

    def body(out, site):
        store = d.Store()
        prov = d.ComponentInstance(store)
        opft = d.FuncType([d.U32Type()], [d.U32Type()], async_=True)
        held = {}

        def op(args):
            if eager:
                d.canon_task_return([d.U32Type()], d.LiftOptions(), [args[0]])
                return [0]  # EXIT
            held["x"] = args[0]
            return [1]  # YIELD

        def op_cb(args):
            d.canon_task_return([d.U32Type()], d.LiftOptions(), [held["x"]])
            return [0]

        op_fi = store.lift(op, opft, d.CanonicalOptions(async_=True, callback=op_cb), prov)

        f = d.ComponentInstance(store)
        cmem = d.MemInst(bytearray(65536), "i32")
        oplow = store.lower(op_fi, opft, d.CanonicalOptions(async_=True, memory=cmem), f)

        def run(args):
            [p] = oplow([7, 0])
            if p == 2:  # RETURNED
                return [u32(cmem, 0)]
            [s] = d.canon_waitable_set_new()
            d.canon_waitable_join(p >> 4, s)
            site["name"] = "run's wait (the sync tail)"
            d.canon_waitable_set_wait(cmem, s, 8)
            return [1000 + u32(cmem, 0)]

        rft = d.FuncType([], [d.U32Type()])
        run_fi = store.lift(run, rft, d.CanonicalOptions(), f)
        run_to_resolution(store, run_fi, out)

    return body


def cancel_other_instance(canceller_async, late=False):
    """`asynclift/cancel-wat`'s shape, where the pin and wasmtime split: a parent's `go` async-lowers a child's
    callback-lifted `run`, which WAITs (the receipt child WAITs on a host `tick`), then calls `subtask.cancel`
    SYNCHRONOUSLY. The child answers TASK_CANCELLED with `task.cancel` and EXIT, or (late) with YIELD,
    resolving only on its next event. canceller_async=False: `go` is a sync lift, as in the fixture. True: an
    async callback lift, the control. The value is lower-state * 16 + cancel-status."""

    def body(out, site):
        store = d.Store()
        c = d.ComponentInstance(store)
        rft = d.FuncType([d.U32Type()], [d.U32Type()], async_=True)

        def run(args):
            [s] = d.canon_waitable_set_new()
            return [2 | (s << 4)]  # WAIT on s

        def run_cb(args):
            if args[0] == 6 and late:  # TASK_CANCELLED, left unresolved for one more event
                return [1]  # YIELD
            if args[0] == 6 or (late and args[0] == 0):  # TASK_CANCELLED, or late's next (NONE) event
                site["name"] = "child's task.cancel"
                d.canon_task_cancel()
                return [0]  # EXIT
            site["name"] = "child got event %d" % args[0]
            d.trap()

        run_fi = store.lift(run, rft, d.CanonicalOptions(async_=True, callback=run_cb), c)

        p = d.ComponentInstance(store)
        pmem = d.MemInst(bytearray(65536), "i32")
        runlow = store.lower(run_fi, rft, d.CanonicalOptions(async_=True, memory=pmem), p)

        def go_core():
            [packed] = runlow([1, 0])
            site["name"] = "go's sync subtask.cancel (the sync tail)"
            [status] = d.canon_subtask_cancel(False, packed >> 4)
            return (packed & 0xf) * 16 + status

        if canceller_async:
            def go(args):
                d.canon_task_return([d.U32Type()], d.LiftOptions(), [go_core()])
                return [0]

            gft = d.FuncType([], [d.U32Type()], async_=True)
            go_fi = store.lift(go, gft, d.CanonicalOptions(async_=True, callback=lambda a: [0]), p)
        else:
            def go(args):
                return [go_core()]

            gft = d.FuncType([], [d.U32Type()])
            go_fi = store.lift(go, gft, d.CanonicalOptions(), p)
        run_to_resolution(store, go_fi, out)

    return body


def future_self_copy():
    """#1006 on the default path, `future-self-copy.wat`: one async callback task creates a future, reads it
    (BLOCKED), writes 7 into it itself, and WAITs on the readable end. The callback returns
    byte + (write status << 8) + (event code << 16). No Threads."""

    def body(out, site):
        store = d.Store()
        inst = d.ComponentInstance(store)
        mem = d.MemInst(bytearray(65536), "i32")
        opts = d.CanonicalOptions(async_=True, memory=mem)
        ft = d.FutureType(d.U8Type())
        st = {}

        def run(args):
            [p] = d.canon_future_new(ft)
            r, w = p & 0xffffffff, p >> 32
            [rd] = d.canon_future_read(ft, opts, r, 64)
            if rd != d.BLOCKED:
                site["name"] = "the read did not block"
                d.trap()
            mem.bytes[80] = 7
            [st["wr"]] = d.canon_future_write(ft, opts, w, 80)
            [s] = d.canon_waitable_set_new()
            d.canon_waitable_join(r, s)
            return [2 | (s << 4)]  # WAIT

        def cb(args):
            d.canon_task_return([d.U32Type()], d.LiftOptions(), [mem.bytes[64] + (st["wr"] << 8) + (args[0] << 16)])
            return [0]

        rft = d.FuncType([], [d.U32Type()], async_=True)
        run_fi = store.lift(run, rft, d.CanonicalOptions(async_=True, callback=cb), inst)
        run_to_resolution(store, run_fi, out)

    return body


def poll_before_suspend(polls):
    """#1020's class, a shape OUTSIDE it (`poll-before-suspend.wat`): a parent's async callback `run`
    async-lowers a provider's `op` (YIELD once, then return x), joins the subtask to a set, and polls it `polls`
    times without suspending. The value is seen * 100 + lower-state; 9000 + state if the lower did not START."""

    def body(out, site):
        store = d.Store()
        prov = d.ComponentInstance(store)
        opft = d.FuncType([d.U32Type()], [d.U32Type()], async_=True)
        held = {}

        def op(args):
            held["x"] = args[0]
            return [1]  # YIELD

        def op_cb(args):
            d.canon_task_return([d.U32Type()], d.LiftOptions(), [held["x"]])
            return [0]

        op_fi = store.lift(op, opft, d.CanonicalOptions(async_=True, callback=op_cb), prov)
        inst = d.ComponentInstance(store)
        mem = d.MemInst(bytearray(65536), "i32")
        oplow = store.lower(op_fi, opft, d.CanonicalOptions(async_=True, memory=mem), inst)

        def run(args):
            [p] = oplow([7, 0])
            if p & 0xf != 1:
                d.canon_task_return([d.U32Type()], d.LiftOptions(), [9000 + (p & 0xf)])
                return [0]
            [s] = d.canon_waitable_set_new()
            d.canon_waitable_join(p >> 4, s)
            seen = 0
            for _ in range(polls):
                [code] = d.canon_waitable_set_poll(mem, s, 8)
                if code != 0:
                    seen = 1
                    break
            d.canon_task_return([d.U32Type()], d.LiftOptions(), [seen * 100 + (p & 0xf)])
            return [0]

        rft = d.FuncType([], [d.U32Type()], async_=True)
        run_fi = store.lift(run, rft, d.CanonicalOptions(async_=True, callback=lambda a: [0]), inst)
        run_to_resolution(store, run_fi, out)

    return body


def later_host_tick(store, flag):
    """A HOST async import whose event arrives later: a host Thread, as head's own `mk_host_func` builds one,
    waiting on a flag the embedder sets once it starts ticking. It resolves with 49."""
    tft = d.FuncType([], [d.U32Type()], async_=True)

    def tick_inst(on_start, on_resolve):
        hinst = d.ComponentInstance(store)
        task = d.Task(tft, d.CanonicalOptions(), hinst, on_start, on_resolve)

        def thread_func():
            on_start()
            h.wait_until(lambda: flag["go"])
            on_resolve([49])
            return None

        h = d.Thread(task, thread_func)
        h.resume()
        return lambda: None

    return tick_inst, tft


def host_event_later(cancel):
    """#1020's class on the default path, no Threads. A parent async-lowers a child's callback-lifted `run`,
    which async-lowers a HOST `tick` whose event arrives later (`later_host_tick`) and WAITs for it.
      - cancel=False, mechanism (a): an async parent returns its lower's state (1 STARTED, 2 RETURNED).
      - cancel=True, mechanism (b): `cancel-wat`'s shape. A SYNC `go` cancels at once, the child answers
        TASK_CANCELLED with `task.cancel`, and the value is lower-state * 16 + cancel status."""

    def body(out, site):
        store = d.Store()
        flag = {"go": False}
        tick_inst, tft = later_host_tick(store, flag)
        child = d.ComponentInstance(store)
        cmem = d.MemInst(bytearray(65536), "i32")
        ticklow = store.lower(tick_inst, tft, d.CanonicalOptions(async_=True, memory=cmem), child)

        def crun(args):
            [p] = ticklow([0])
            if p & 0xf == 2:
                d.canon_task_return([d.U32Type()], d.LiftOptions(), [u32(cmem, 0)])
                return [0]
            [s] = d.canon_waitable_set_new()
            d.canon_waitable_join(p >> 4, s)
            return [2 | (s << 4)]  # WAIT

        def ccb(args):
            if args[0] == 6:  # TASK_CANCELLED
                d.canon_task_cancel()
                return [0]
            d.canon_task_return([d.U32Type()], d.LiftOptions(), [u32(cmem, 0)])
            return [0]

        cft = d.FuncType([d.U32Type()], [d.U32Type()], async_=True)
        crun_fi = store.lift(crun, cft, d.CanonicalOptions(async_=True, callback=ccb), child)
        parent = d.ComponentInstance(store)
        pmem = d.MemInst(bytearray(65536), "i32")
        clow = store.lower(crun_fi, cft, d.CanonicalOptions(async_=True, memory=pmem), parent)

        if cancel:
            def go(args):
                [p] = clow([1, 0])
                site["name"] = "go's sync subtask.cancel (the sync tail)"
                [status] = d.canon_subtask_cancel(False, p >> 4)
                return [(p & 0xf) * 16 + status]

            go_fi = store.lift(go, d.FuncType([], [d.U32Type()]), d.CanonicalOptions(), parent)
        else:
            def prun(args):
                [p] = clow([7, 0])
                d.canon_task_return([d.U32Type()], d.LiftOptions(), [p & 0xf])
                return [0]

            go_fi = store.lift(prun, d.FuncType([], [d.U32Type()], async_=True),
                               d.CanonicalOptions(async_=True, callback=lambda a: [0]), parent)
        got = []
        host_invoke(store, go_fi, lambda res: got.append(res))
        flag["go"] = True  # the host's event arrives only now, after the parent's call gave control back
        while not got:
            store.tick()
        out.value = "value %d" % got[0][0]
        out.done.set()

    return body


def promote_implicit(promote_run):
    """Step 4, cmthreads/promote-implicit.wat: T yield-then-promotes its task's implicit Thread, which waits
    at its YIELD arm (promote_run), or a Thread U that was never readied (the control). The value is 16 + the
    flag run's callback saw for "T resumed" on its first entry after T started."""

    def body(out, site):
        store = d.Store()
        inst = d.ComponentInstance(store)
        m = {"started": 0, "resumed": 0, "target": 0, "seen": -1}

        def t(args):
            m["started"] = 1
            d.canon_thread_yield_then_promote(m["target"])
            m["resumed"] = 1
            return []

        tbl = d.Table()
        fi = tbl.add(d.CoreFuncRef(BODYT, t))

        def run(args):
            if promote_run:
                [m["target"]] = d.canon_thread_index()
            else:
                [m["target"]] = d.canon_thread_new_indirect(BODYT, tbl, fi, 1)  # U: never readied
            [ti] = d.canon_thread_new_indirect(BODYT, tbl, fi, 0)
            d.canon_thread_resume_later(ti)
            return [1]  # YIELD

        def cb(args):
            if m["started"] and m["seen"] == -1:
                m["seen"] = m["resumed"]
            if m["resumed"] and m["seen"] != -1:
                d.canon_task_return([d.U32Type()], d.LiftOptions(), [16 + m["seen"]])
                return [0]
            return [1]

        rft = d.FuncType([], [d.U32Type()], async_=True)
        run_fi = store.lift(run, rft, d.CanonicalOptions(async_=True, callback=cb), inst)
        run_to_resolution(store, run_fi, out)

    return body


def host_event_inline():
    """`host_event_later`'s shape with the host `tick` resolving INLINE (`crossCallHost`'s deferTick=false). The
    child sees `tick` RETURNED, calls task.return, and EXITs inside its first segment, so the parent's lower
    returns RETURNED (2): `call-wat-parent`'s inline arm, mechanism (c)."""
    tft = d.FuncType([], [d.U32Type()], async_=True)

    def tick_inst(on_start, on_resolve):
        on_start()
        on_resolve([49])
        return lambda: None

    def body(out, site):
        store = d.Store()
        child = d.ComponentInstance(store)
        cmem = d.MemInst(bytearray(65536), "i32")
        ticklow = store.lower(tick_inst, tft, d.CanonicalOptions(async_=True, memory=cmem), child)

        def crun(args):
            [p] = ticklow([0])
            if p & 0xf == 2:
                d.canon_task_return([d.U32Type()], d.LiftOptions(), [u32(cmem, 0)])
                return [0]
            [s] = d.canon_waitable_set_new()
            d.canon_waitable_join(p >> 4, s)
            return [2 | (s << 4)]  # WAIT

        def ccb(args):
            d.canon_task_return([d.U32Type()], d.LiftOptions(), [u32(cmem, 0)])
            return [0]

        cft = d.FuncType([d.U32Type()], [d.U32Type()], async_=True)
        crun_fi = store.lift(crun, cft, d.CanonicalOptions(async_=True, callback=ccb), child)
        parent = d.ComponentInstance(store)
        pmem = d.MemInst(bytearray(65536), "i32")
        clow = store.lower(crun_fi, cft, d.CanonicalOptions(async_=True, memory=pmem), parent)

        def prun(args):
            [p] = clow([7, 0])
            d.canon_task_return([d.U32Type()], d.LiftOptions(), [p & 0xf])
            return [0]

        go_fi = store.lift(prun, d.FuncType([], [d.U32Type()], async_=True),
                           d.CanonicalOptions(async_=True, callback=lambda a: [0]), parent)
        got = []
        host_invoke(store, go_fi, lambda res: got.append(res))
        while not got:
            store.tick()
        out.value = "value %d" % got[0][0]
        out.done.set()

    return body


def first_segment_return():
    """#1020's class, mechanism (c): `first-segment-return.wat`. A child's callback lift calls task.return and
    EXITs inside its first core call, so it resolves before its first suspension. canon_lift's
    `thread.resume()` runs that segment inside the parent's lower, which therefore returns RETURNED (2)."""

    def body(out, site):
        store = d.Store()
        child = d.ComponentInstance(store)

        def crun(args):
            d.canon_task_return([d.U32Type()], d.LiftOptions(), [7])
            return [0]  # EXIT

        cft = d.FuncType([d.U32Type()], [d.U32Type()], async_=True)
        crun_fi = store.lift(crun, cft, d.CanonicalOptions(async_=True, callback=lambda a: [0]), child)
        parent = d.ComponentInstance(store)
        pmem = d.MemInst(bytearray(65536), "i32")
        clow = store.lower(crun_fi, cft, d.CanonicalOptions(async_=True, memory=pmem), parent)

        def prun(args):
            [p] = clow([1, 0])
            d.canon_task_return([d.U32Type()], d.LiftOptions(), [p & 0xf])
            return [0]

        go_fi = store.lift(prun, d.FuncType([], [d.U32Type()], async_=True),
                           d.CanonicalOptions(async_=True, callback=lambda a: [0]), parent)
        got = []
        host_invoke(store, go_fi, lambda res: got.append(res))
        while not got:
            store.tick()
        out.value = "value %d" % got[0][0]
        out.done.set()

    return body


def battery():
    """Every committed scenario, calibration first."""
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
    read("promote-implicit (#1012; wasmtime 49.0.2: 16)", promote_implicit(True))
    read("promote-implicit control (a never-readied U; wasmtime 49.0.2: 16)", promote_implicit(False))
    read("sync-wait-host-import (ADR 0102's gate-off row; wasmtime 49.0.2: trap)", sync_wait_host_import(False))
    read("sync-wait-host-import, eager control (wasmtime 49.0.2: 7)", sync_wait_host_import(True))
    read("cancel-wat's shape, sync canceller (wasmtime 49.0.2 harness: returned, 1 then 4)", cancel_other_instance(False))
    read("cancel-wat's shape, async canceller (control; wasmtime 49.0.2: 20)", cancel_other_instance(True))
    read("cancel, late child, sync canceller (wasmtime 49.0.2: trap)", cancel_other_instance(False, late=True))
    read("cancel, late child, async canceller (wasmtime 49.0.2: 20)", cancel_other_instance(True, late=True))
    read("future-self-copy (#1006, default path; wasmtime 49.0.2: 262151)", future_self_copy())
    read("poll-before-suspend (#1020, outside the class; wasmtime 49.0.2: 1)", poll_before_suspend(2000))
    read("host-event-later (#1020 mechanism (a): the lower's state)", host_event_later(False))
    read("host-event-later, sync cancel (#1020 mechanism (b))", host_event_later(True))
    read("first-segment-return (#1020 mechanism (c); wasmtime 49.0.3: 2)", first_segment_return())
    read("host-event-inline (#1020 mechanism (c): call-wat-parent's inline arm)", host_event_inline())


if __name__ == "__main__":
    import hashlib
    import platform

    with open(DEFINITIONS, "rb") as fh:
        digest = hashlib.sha256(fh.read()).hexdigest()
    label = os.environ.get("REFREAD_LABEL") or PIN
    print("# REFERENCE READING — definitions.py @ %s, not a wasmtime reading." % label)
    print("# definitions.py sha256 %s; Python %s (uv-managed)." % (digest, platform.python_version()))
    print("# Each line is the set of outcomes over DETERMINISTIC_PROFILE in {False, True} x seeds %s." % list(SEEDS))
    print("# HANG means no result and no trap within %.0fs." % BOUND_S)
    battery()
