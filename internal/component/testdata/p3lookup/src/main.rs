// p3lookup: an ordinary Rust wasm32-wasip3 std program that resolves names, the witness for E7c (#1278, ADR 0126). It
// uses std's own ToSocketAddrs and TcpStream, with no bindings, because what is under test is what std and wasi-libc
// do with them: wasi-libc's getaddrinfo lowers wasi:sockets/ip-name-lookup's resolve-addresses, an `async func`,
// WITHOUT the async option, so every lookup is the pin's blocking sync lower (#1268).
//
// It needs no DNS server, so neither engine's reading depends on the network: localhost is answered from the hosts
// file, the literal is parsed by wasi-libc without reaching the host, and the last two names are syntactically
// invalid, which the host refuses before any lookup.
//
// Usage: p3lookup <echo-port>. It resolves localhost, the literal 127.0.0.1, "a b" and the empty name, then connects to
// localhost:<echo-port>, writes 1 KiB, shuts its write half and reads the echo to the end. It prints properties, never
// a port or the answers themselves: which addresses localhost has, and in what order, are the host's, and differ
// between getaddrinfo and Go's resolver (wasmtime 49.0.3 on darwin answers both loopbacks, and std tries ::1 first).

use std::io::{Read, Write};
use std::net::{IpAddr, Ipv4Addr, Shutdown, SocketAddr, TcpStream, ToSocketAddrs};

fn main() {
    let args: Vec<String> = std::env::args().collect();
    let port = match args.get(1).and_then(|s| s.parse::<u16>().ok()) {
        Some(p) => p,
        None => {
            eprintln!("usage: p3lookup <echo-port>");
            std::process::exit(2);
        }
    };
    let lo = IpAddr::V4(Ipv4Addr::LOCALHOST);
    match ("localhost", port).to_socket_addrs() {
        Ok(it) => {
            // Sorted, so nothing below could depend on the resolver's order even if a property were made to.
            let mut v: Vec<SocketAddr> = it.collect();
            v.sort();
            println!(
                "p3lookup: localhost: resolved, includes 127.0.0.1: {}, every address is loopback at argv's port: {}",
                v.iter().any(|a| a.ip() == lo),
                !v.is_empty() && v.iter().all(|a| a.ip().is_loopback() && a.port() == port)
            );
        }
        Err(e) => println!("p3lookup: localhost: {:?}: {}", e.kind(), e),
    }
    match ("127.0.0.1", port).to_socket_addrs() {
        Ok(it) => {
            let v: Vec<SocketAddr> = it.collect();
            println!("p3lookup: the literal 127.0.0.1 resolves to itself: {}", v == [SocketAddr::new(lo, port)]);
        }
        Err(e) => println!("p3lookup: the literal 127.0.0.1: {:?}: {}", e.kind(), e),
    }
    for (what, name) in [("\"a b\"", "a b"), ("the empty name", "")] {
        match (name, port).to_socket_addrs() {
            Ok(it) => println!("p3lookup: {}: resolved to {} addresses", what, it.count()),
            Err(e) => println!("p3lookup: {}: {:?}: {}", what, e.kind(), e),
        }
    }
    match TcpStream::connect(("localhost", port)) {
        Err(e) => println!("p3lookup: connect localhost: {:?}", e.kind()),
        Ok(mut s) => {
            println!("p3lookup: connect localhost: ok");
            let data: Vec<u8> = (0..1024u32).map(|i| (i.wrapping_mul(2654435761) >> 24) as u8).collect();
            if let Err(e) = s.write_all(&data) {
                println!("p3lookup: write: {:?}", e.kind());
            }
            if let Err(e) = s.shutdown(Shutdown::Write) {
                println!("p3lookup: shutdown: {:?}", e.kind());
            }
            let mut back = Vec::new();
            match s.read_to_end(&mut back) {
                Ok(got) => println!("p3lookup: echoed {} of {} bytes, match: {}", got, data.len(), back == data),
                Err(e) => println!("p3lookup: read: {:?}", e.kind()),
            }
        }
    }
}
