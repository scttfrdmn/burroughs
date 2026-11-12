;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; type-chain-18: grave #1256's storage witness, the cheap arm. An imported instance type holding a chain of
;; shared types, t0 = u32 and t_k = tuple<t_{k-1}, t_{k-1}>, each exported and so each kept by the decoder,
;; and two functions returning t16 and t18. t_k's effective size is 2^(k+1)-1 nodes, so a decoder that stores
;; each level expanded allocates about a million nodes for these 18 levels, and one that shares a resolved
;; type allocates a few per level. 18 levels keep the expanded form affordable, so the neuter can be run.
;;
;; **Not a valid component.** wasm-tools 1.258.0 refuses it, *"effective type size exceeds the limit of
;; 1000000"*, since the instance type's effective size sums its exports'. The decoder reads it anyway, which
;; is the case the witness is for.
;; Assembled with `wasm-tools parse type-chain-18.wat -o type-chain-18.wasm` (wasm-tools 1.258.0).
(component
  (type $chain-t (instance
    (type $t0 u32)
    (type $t1 (tuple $t0 $t0))
    (export "t1" (type (eq $t1)))
    (type $t2 (tuple $t1 $t1))
    (export "t2" (type (eq $t2)))
    (type $t3 (tuple $t2 $t2))
    (export "t3" (type (eq $t3)))
    (type $t4 (tuple $t3 $t3))
    (export "t4" (type (eq $t4)))
    (type $t5 (tuple $t4 $t4))
    (export "t5" (type (eq $t5)))
    (type $t6 (tuple $t5 $t5))
    (export "t6" (type (eq $t6)))
    (type $t7 (tuple $t6 $t6))
    (export "t7" (type (eq $t7)))
    (type $t8 (tuple $t7 $t7))
    (export "t8" (type (eq $t8)))
    (type $t9 (tuple $t8 $t8))
    (export "t9" (type (eq $t9)))
    (type $t10 (tuple $t9 $t9))
    (export "t10" (type (eq $t10)))
    (type $t11 (tuple $t10 $t10))
    (export "t11" (type (eq $t11)))
    (type $t12 (tuple $t11 $t11))
    (export "t12" (type (eq $t12)))
    (type $t13 (tuple $t12 $t12))
    (export "t13" (type (eq $t13)))
    (type $t14 (tuple $t13 $t13))
    (export "t14" (type (eq $t14)))
    (type $t15 (tuple $t14 $t14))
    (export "t15" (type (eq $t15)))
    (type $t16 (tuple $t15 $t15))
    (export "t16" (type (eq $t16)))
    (type $t17 (tuple $t16 $t16))
    (export "t17" (type (eq $t17)))
    (type $t18 (tuple $t17 $t17))
    (export "t18" (type (eq $t18)))
    (export "get16" (func (result $t16)))
    (export "get18" (func (result $t18)))))
  (import "test:chain/types@0.1.0" (instance (type $chain-t))))
