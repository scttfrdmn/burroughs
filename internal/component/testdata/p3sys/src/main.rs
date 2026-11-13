// p3sys: the E4 witness guest (#1232, ADR 0119). It calls every synchronous WASI 0.3 function E4 serves,
// through bindings generated from the pinned WIT, and prints PROPERTIES of the answers, never the answers:
// random bytes and clock readings differ run to run and engine to engine, and what each prints must compare
// byte for byte across wasmtime 49.0.3 and Burroughs.
//
// One write per channel, on purpose. std's wasip3 stdout keeps one stream open across writes, and Burroughs'
// write-via-stream consumes a single write (ADR 0119 records it), so every line goes out in one `print!`.
// stderr gets one line, and then the guest exits with code 7.

wit_bindgen::generate!({ path: "wit", world: "p3sys", generate_all });

use std::fmt::Write as _;
use wasi::cli::{environment, exit, terminal_stderr, terminal_stdin, terminal_stdout};
use wasi::clocks::{monotonic_clock, system_clock};
use wasi::random::{insecure, insecure_seed, random};

// 2026-01-01T00:00:00Z: a system clock reading before it is not a reading of now.
const JAN_2026: i64 = 1_767_225_600;

fn bytes_line(out: &mut String, name: &str, max_len: u64, f: fn(u64) -> Vec<u8>) {
    let n = f(max_len).len() as u64;
    let prop = if max_len == 0 { n == 0 } else { 1 <= n && n <= max_len };
    let _ = writeln!(out, "{name}({max_len}): len within the WIT's bound: {prop}");
}

fn huge_line(out: &mut String, name: &str, label: &str, max_len: u64, f: fn(u64) -> Vec<u8>) {
    let _ = writeln!(out, "{name}({label}): len {}", f(max_len).len());
}

fn main() {
    let mut out = String::new();

    // wasi:random/random and wasi:random/insecure: lengths, the cap, and two draws that differ.
    bytes_line(&mut out, "get-random-bytes", 0, random::get_random_bytes);
    bytes_line(&mut out, "get-random-bytes", 16, random::get_random_bytes);
    huge_line(&mut out, "get-random-bytes", "2^30", 1 << 30, random::get_random_bytes);
    huge_line(&mut out, "get-random-bytes", "u64::MAX", u64::MAX, random::get_random_bytes);
    let differ = random::get_random_bytes(16) != random::get_random_bytes(16);
    let _ = writeln!(out, "get-random-bytes: two 16-byte draws differ: {differ}");
    let differ = random::get_random_u64() != random::get_random_u64();
    let _ = writeln!(out, "get-random-u64: two draws differ: {differ}");
    bytes_line(&mut out, "get-insecure-random-bytes", 0, insecure::get_insecure_random_bytes);
    bytes_line(&mut out, "get-insecure-random-bytes", 16, insecure::get_insecure_random_bytes);
    huge_line(&mut out, "get-insecure-random-bytes", "u64::MAX", u64::MAX, insecure::get_insecure_random_bytes);
    let differ = insecure::get_insecure_random_u64() != insecure::get_insecure_random_u64();
    let _ = writeln!(out, "get-insecure-random-u64: two draws differ: {differ}");

    // wasi:random/insecure-seed: the WIT means it to be stable, so two calls agree.
    let (a, b) = (insecure_seed::get_insecure_seed(), insecure_seed::get_insecure_seed());
    let _ = writeln!(out, "get-insecure-seed: stable across calls: {}", a == b);
    let _ = writeln!(out, "get-insecure-seed: nonzero: {}", a != (0, 0));

    // wasi:clocks/monotonic-clock: now advances within a bounded number of reads. A clock that saturates or
    // stands still never does.
    let t0 = monotonic_clock::now();
    let advanced = (0..1_000_000).any(|_| monotonic_clock::now() > t0);
    let _ = writeln!(out, "monotonic-clock now: advances: {advanced}");
    let _ = writeln!(out, "monotonic-clock get-resolution: above 0: {}", monotonic_clock::get_resolution() > 0);

    // wasi:clocks/system-clock: the instant's two fields are in range.
    let t = system_clock::now();
    let _ = writeln!(out, "system-clock now: nanoseconds below 1e9: {}", t.nanoseconds < 1_000_000_000);
    let _ = writeln!(out, "system-clock now: seconds after 2026-01-01: {}", t.seconds > JAN_2026);
    let _ = writeln!(out, "system-clock get-resolution: above 0: {}", system_clock::get_resolution() > 0);

    // wasi:cli: the environment and cwd are not granted, and the host's streams are not terminals.
    let _ = writeln!(out, "get-environment: empty: {}", environment::get_environment().is_empty());
    let _ = writeln!(out, "get-initial-cwd: none: {}", environment::get_initial_cwd().is_none());
    let _ = writeln!(out, "get-terminal-stdin: none: {}", terminal_stdin::get_terminal_stdin().is_none());
    let _ = writeln!(out, "get-terminal-stdout: none: {}", terminal_stdout::get_terminal_stdout().is_none());
    let _ = writeln!(out, "get-terminal-stderr: none: {}", terminal_stderr::get_terminal_stderr().is_none());

    print!("{out}");
    eprint!("p3sys: exiting with code 7\n");
    exit::exit_with_code(7);
}
