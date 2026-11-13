// p3sleep: an ordinary Rust wasm32-wasip3 std program that sleeps, the witness for E5 (#1233) and grave #1267
// (ADR 0121). It uses std's own sleeps, with no bindings, because the defects were in what std does with them:
//
//   * thread::sleep reaches wasi-libc's clock_nanosleep, which async-lowers `wait-for` and then waits on a
//     waitable set inside `run`, which is async-typed and lifted plainly (grave #1267's specimen);
//   * thread::sleep_until reaches clock_nanosleep with TIMER_ABSTIME, which async-lowers `wait-until` with
//     a mark on the monotonic clock `now` reads;
//   * thread::sleep(ZERO) makes no call at all: std's loop runs while the remaining time is nonzero.
//
// It prints properties, never durations, so its output is the same on every host that serves the sleeps.
#![feature(thread_sleep_until)]

use std::time::{Duration, Instant};

fn main() {
    let start = Instant::now();
    std::thread::sleep(Duration::from_millis(20));
    println!("p3sleep: sleep(20ms) took at least 20ms: {}", start.elapsed() >= Duration::from_millis(20));

    let deadline = Instant::now() + Duration::from_millis(20);
    std::thread::sleep_until(deadline);
    println!("p3sleep: sleep_until(deadline) returned past its mark: {}", Instant::now() >= deadline);

    std::thread::sleep(Duration::ZERO);
    println!("p3sleep: sleep(ZERO) returned");
}
