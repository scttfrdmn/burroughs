// p3tcp: an ordinary Rust wasm32-wasip3 std program that is a TCP client, the witness for E7a (#1235, ADR 0123). It
// uses std's own TcpStream, with no bindings, because what is under test is what std and wasi-libc do with it:
//
//   * TcpStream::connect is wasi:sockets' tcp-socket.create, then an async connect whose 13 flat parameters spill
//     to a pointer, then receive and send, which wasi-libc calls at once;
//   * local_addr and peer_addr are get-local-address and get-remote-address;
//   * write_all is stream.write on send's stream, shutdown(Write) drops its writable end, and read_to_end reads
//     receive's stream to the peer's end;
//   * a connect to a port nobody listens on is refused.
//
// Usage: p3tcp <echo-port> <closed-port> [<bytes>]. It connects to 127.0.0.1:<echo-port>, writes <bytes> (102400 by
// default), shuts its write half, reads to the end, and then connects to <closed-port>. It prints properties, never
// a port or an address, so its output is the same on every host whatever ports the peers are given.

use std::io::{Read, Write};
use std::net::{IpAddr, Ipv4Addr, Shutdown, SocketAddr, TcpStream};

fn main() {
    let args: Vec<String> = std::env::args().collect();
    let arg = |i: usize| args.get(i).and_then(|s| s.parse::<u16>().ok());
    let (port, closed) = match (arg(1), arg(2)) {
        (Some(p), Some(c)) => (p, c),
        _ => {
            eprintln!("usage: p3tcp <echo-port> <closed-port> [<bytes>]");
            std::process::exit(2);
        }
    };
    let n: usize = args.get(3).and_then(|s| s.parse().ok()).unwrap_or(100 * 1024);
    let lo = IpAddr::V4(Ipv4Addr::LOCALHOST);
    match TcpStream::connect(SocketAddr::new(lo, port)) {
        Err(e) => println!("p3tcp: connect: {:?}", e.kind()),
        Ok(mut s) => {
            println!("p3tcp: connect: ok");
            match s.local_addr() {
                Ok(a) => println!("p3tcp: local address is v4 loopback: {}", a.is_ipv4() && a.ip().is_loopback()),
                Err(e) => println!("p3tcp: local address: {:?}", e.kind()),
            }
            match s.peer_addr() {
                Ok(a) => println!("p3tcp: remote is v4 loopback at argv's port: {}", a.ip() == lo && a.port() == port),
                Err(e) => println!("p3tcp: remote address: {:?}", e.kind()),
            }
            let data: Vec<u8> = (0..n as u32).map(|i| (i.wrapping_mul(2654435761) >> 24) as u8).collect();
            if let Err(e) = s.write_all(&data) {
                println!("p3tcp: write: {:?}", e.kind());
            }
            if let Err(e) = s.shutdown(Shutdown::Write) {
                println!("p3tcp: shutdown: {:?}", e.kind());
            }
            let mut back = Vec::new();
            match s.read_to_end(&mut back) {
                Ok(got) => println!("p3tcp: echoed {} of {} bytes, match: {}", got, data.len(), back == data),
                Err(e) => println!("p3tcp: read: {:?}", e.kind()),
            }
        }
    }
    match TcpStream::connect(SocketAddr::new(lo, closed)) {
        Err(e) => println!("p3tcp: closed port: {:?}", e.kind()),
        Ok(_) => println!("p3tcp: closed port: connected"),
    }
}
