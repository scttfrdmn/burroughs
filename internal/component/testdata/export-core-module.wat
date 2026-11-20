;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; export-core-module: #1258. A component exporting a core module, the one core sort Binary.md lets an export name,
;; with a core func aliased ahead of the export. TestAnExportOfACoreFuncIsRefused patches the export's core sort
;; byte from module (0x11) to func (0x00), which the text format cannot spell, and the loader must refuse it.
;; Assembled with `wasm-tools parse export-core-module.wat -o export-core-module.wasm` (wasm-tools 1.258.0).
(component
  (core module $m (func (export "f")))
  (core instance $i (instantiate $m))
  (alias core export $i "f" (core func $f))
  (export "m" (core module $m)))
