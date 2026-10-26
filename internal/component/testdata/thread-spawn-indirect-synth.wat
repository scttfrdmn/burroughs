(component
  (core module $m (table (export "t") shared 1 (ref null (shared func))))
  (core instance $i (instantiate $m))
  (alias core export $i "t" (core table $t))
  (core type $ft (shared (func (param i32))))
  (canon thread.spawn-indirect $ft (table $t) (core func $s))
)
