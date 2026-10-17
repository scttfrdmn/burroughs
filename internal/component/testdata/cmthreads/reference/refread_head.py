#!/usr/bin/env python3
# Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
#
# refread.py's cancellation scenarios, read against a definitions.py OTHER than the pin: upstream's head,
# to ask whether the pin is stale on a row (the chair's ruling of 2026-10-17, the sync `subtask.cancel` row).
#
#   curl -fsSL https://raw.githubusercontent.com/WebAssembly/component-model/<sha>/design/mvp/canonical-abi/definitions.py -o /tmp/head/definitions.py
#   uv run --no-project --python 3.14 python3 -I refread_head.py /tmp/head/definitions.py > refread-head.reading
#
# Head needs Python >= 3.14: it dropped `from __future__ import annotations` and forward-references types in
# annotations; upstream CI runs `python-version: 3.x`.
#
# **A head trap is read by frame, not by type.** Head's `trap()` calls `current_instance().store.lock_down()`
# (upstream 5b724da8c and a25fc0b37), and the embedder's thread has no current instance, so a sync-tail trap
# surfaces as an AttributeError raised from `trap_if(not candidates)`. refread's watch reports it as ERROR;
# the breakdown in `TestASyncCancelFollowsUpstream726InBothGateStates`'s doc gives the frame.
import importlib.util
import os
import sys

spec = importlib.util.spec_from_file_location("definitions", sys.argv[1])
mod = importlib.util.module_from_spec(spec)
sys.modules["definitions"] = mod  # refread's `import definitions` binds this, not the vendored pin
spec.loader.exec_module(mod)
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import refread as r  # noqa: E402

assert r.d is mod, "refread bound the pin, not the definitions.py given"
r.read("wait-runs-ready (calibration; wasmtime 49.0.2: 5)", r.wait_runs_ready)
r.read("cancel, sync canceller (wasmtime 49.0.2: 20)", r.cancel_other_instance(False))
r.read("cancel, async canceller (control; wasmtime 49.0.2: 20)", r.cancel_other_instance(True))
r.read("cancel, late child, sync canceller (wasmtime 49.0.2: trap)", r.cancel_other_instance(False, late=True))
r.read("cancel, late child, async canceller (wasmtime 49.0.2: 20)", r.cancel_other_instance(True, late=True))
