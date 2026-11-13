// p3std: an ordinary Rust wasm32-wasip3 std program, the witness for #1261 and #1262 (ADR 0120). It uses
// std's own printing and exit, with no bindings, because the defects were in what std does with them:
//
//   * std keeps one stdout stream open across writes, so the second println is a second write on one stream;
//   * eprintln is the same over stderr's stream;
//   * print!("") then a flush offers std's writer nothing, and the flush with nothing buffered is a no-op;
//   * the last print! has no newline, so it stays in std's line buffer until process::exit flushes it, and
//     the flush is one more write on the open stream before the exit;
//   * process::exit(7) calls exit-with-code and follows the call with `unreachable`, since it does not return.

use std::io::Write;

fn main() {
    println!("p3std: line 1");
    println!("p3std: line 2");
    println!("p3std: line 3");
    eprintln!("p3std: a line to stderr");
    print!("");
    std::io::stdout().flush().unwrap();
    print!("p3std: buffered with no newline");
    std::process::exit(7);
}
