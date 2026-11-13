// p3cat: an ordinary Rust wasm32-wasip3 std program that reads stdin to its end, the witness for E6 (#1234)
// and #1264 (ADR 0122). It uses std's own stdin, with no bindings, because what is under test is what std and
// wasi-libc do with it:
//
//   * the first read calls `wasi:cli/stdin@0.3.0`'s read-via-stream once, sync-lowered with a return pointer,
//     and keeps the stream and its future;
//   * each read() is one async `stream.read`, which blocks on a waitable set when it returns BLOCKED, so
//     read_to_end is several reads, the last of them short;
//   * the read after the last byte sees the writer's drop (DROPPED), and then reads the future to tell end of
//     input from an error.
//
// It prints the byte count, a checksum and the line count, then echoes the first line, so its output is the
// same on every host that serves stdin's bytes in order and loses none.

use std::io::{Read, Write};

fn main() {
    let mut buf = Vec::new();
    if let Err(e) = std::io::stdin().read_to_end(&mut buf) {
        eprintln!("p3cat: reading stdin failed: {e}");
        std::process::exit(1);
    }
    let sum = buf.iter().fold(0u32, |a, &b| a.wrapping_mul(31).wrapping_add(u32::from(b)));
    let lines = buf.iter().filter(|&&b| b == b'\n').count();
    println!("p3cat: read {} bytes, checksum {:08x}, {} lines", buf.len(), sum, lines);
    let first = buf.split(|&b| b == b'\n').next().unwrap_or(&[]);
    let mut out = std::io::stdout().lock();
    out.write_all(b"p3cat: first line: ").unwrap();
    out.write_all(first).unwrap();
    out.write_all(b"\n").unwrap();
}
