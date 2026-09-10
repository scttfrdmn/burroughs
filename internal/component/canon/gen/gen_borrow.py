#!/usr/bin/env python3
# Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
#
# Phase-3 (gate:async) borrow-lifetime fixture harness — DORMANT oracle tooling, not wired to a build
# target and not run in CI. The whole borrow-lifetime discipline relocated to Phase 3 (dated disposition
# on #694): its three assertions are unreachable in the sync tier — the model's subtask `resolve`
# force-accounts num_lends and num_borrows to 0 before a sync parent resumes (probe: 0→1→0,
# num_borrows==0 at every sync return), and the lender is suspended for the callee's duration
# (`may_leave`). They need a caller running while a borrow is outstanding, which is async.
#
# This is the harness Phase 3's fixtures will be generated from: it drives definitions.py's OWN call
# machinery (the test_handles pattern; the mk_opts/mk_host_func/lift_and_run helpers live in the
# reference test harness, inlined here). It commits as generator code with **no fixtures** and no
# Burroughs canon-package lend model — a lend model with nothing that lends is a scaffold without a
# consumer. scenario_lend_released below is the proven driver (the 0→1→0 trace); Phase 3 adds the
# borrow-outliving and own-dropped-while-lent scenarios and commits their fixtures behind gate:async.

import json
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "vendor"))

import definitions  # noqa: E402
from definitions import (  # noqa: E402
    Store, ComponentInstance, ResourceType, FuncType, OwnType, BorrowType, CanonicalOptions, Task,
    Thread, canon_resource_new, canon_resource_drop,
)

definitions.DETERMINISTIC_PROFILE = True


def mk_opts():
    opts = CanonicalOptions()
    opts.string_encoding = "utf8"
    opts.realloc = None
    opts.post_return = None
    opts.sync_task_return = False
    opts.async_ = False
    opts.callback = None
    return opts


def mk_host_func(store, host_func, ft):
    def func_inst(on_start, on_resume):
        def thread_func():
            host_func(on_start, on_resume, lambda rf: host_thread.wait_until(rf))
        inst = ComponentInstance(store)
        task = Task(ft, CanonicalOptions(), inst, on_start, on_resume)
        host_thread = Thread(task, thread_func)
        host_thread.resume()
        return lambda: None
    return func_inst


def lift_and_run(opts, inst, ft, callee, on_start, on_resolve):
    func_inst = inst.store.lift(callee, ft, opts, inst)
    _ = inst.store.invoke(func_inst, on_start, on_resolve)
    while inst.store.waiting:
        inst.store.tick()


def scenario_lend_released():
    """A callee owns a resource and lends a borrow of it to a nested host call. num_lends is 1 while the
    nested call is live and 0 after it resolves — the release the model performs at scope close."""
    store = Store()
    inst = ComponentInstance(store)
    rt = ResourceType(inst, dtor=lambda rep: [])
    opts = mk_opts()

    host_ft = FuncType([BorrowType(rt)], [])
    lends = {}

    def host_func(on_start, on_return, wait_until):
        args = on_start()  # lifting the borrow arg records the lend on the source own
        # observe num_lends on the lender while the borrow is live in this nested call
        lends["during"] = inst.handles.get(observed["own"]).num_lends
        on_return([])

    host_inst = mk_host_func(store, host_func, host_ft)

    observed = {}

    def core_wasm(args):
        h = canon_resource_new(rt, 42)[0]
        observed["own"] = h
        lends["before"] = inst.handles.get(h).num_lends
        # lend a borrow of h into the nested host call
        store.lower(host_inst, host_ft, opts, inst)([h])
        lends["after"] = inst.handles.get(h).num_lends
        canon_resource_drop(rt, h)  # now unlent, drops cleanly
        return []

    lift_and_run(opts, inst, FuncType([], []), core_wasm, lambda: [], lambda r: None)
    return {"name": "lend-released-at-return", "num_lends": lends}


def main():
    out = {"pin": "2bed77e4228841c1d2721996d3ecc169ff96b158", "borrow": [scenario_lend_released()]}
    json.dump(out, sys.stdout, indent=2)
    sys.stdout.write("\n")


if __name__ == "__main__":
    main()
