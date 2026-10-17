;; forward-base: the base the two ➡️ forward fixtures are derived from. No toolchain emits `stream.forward` or
;; `future.forward` yet: wasm-tools 1.258.0 rejects 0x2e as "invalid leading byte for canonical function".
;; So this component's one canon, `stream.drop-writable` (`0x14 t:<typeidx>`), which has the same operand
;; shape as `0x2e t:<typeidx>` and `0x2f t:<typeidx>`, is parsed and then patched at its opcode byte:
;;
;;   wasm-tools parse forward-base.wat -o base.wasm     (wasm-tools 1.258.0)
;;   byte 17 (0x14, followed by type index 0x00) := 0x2e  ->  stream-forward.wasm
;;   byte 17                                       := 0x2f  ->  future-forward.wasm
;;
;; The witness is TestTheForwardBuiltinsAreRefusedByName.
(component
  (type $s (stream u8))
  (core func $f (canon stream.drop-writable $s))
)
