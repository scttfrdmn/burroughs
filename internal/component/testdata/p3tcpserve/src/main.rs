// p3tcpserve: an ordinary Rust wasm32-wasip3 std program that is a TCP server, the witness for E7b-2 (#1277, ADR
// 0125). It uses std's own TcpListener and TcpStream, with no bindings, because what is under test is what std and
// wasi-libc do with them:
//
//   * TcpListener::bind is wasi:sockets' tcp-socket.create, bind, set-listen-backlog-size and listen, which wasi-libc
//     calls in that order;
//   * local_addr on the listener is get-local-address on a listening socket;
//   * TcpStream::connect to the listener is a connect that the kernel completes into the listen backlog;
//   * accept is one stream.read of listen's stream<own<tcp-socket>>, then receive, send and get-remote-address on the
//     accepted handle, which wasi-libc calls at once;
//   * a small echo in both directions, each side shutting its write half and reading the other to the end.
//
// It is single-threaded, and it prints properties, never a port or an address, so its output is the same on every
// host whatever port the listener is given.

use std::io::{Read, Write};
use std::net::{IpAddr, Ipv4Addr, Shutdown, TcpListener, TcpStream};

fn main() {
    let lo = IpAddr::V4(Ipv4Addr::LOCALHOST);
    let ln = match TcpListener::bind((lo, 0)) {
        Ok(l) => {
            println!("p3tcpserve: bind: ok");
            l
        }
        Err(e) => {
            println!("p3tcpserve: bind: {:?}", e.kind());
            return;
        }
    };
    let la = match ln.local_addr() {
        Ok(a) => {
            println!("p3tcpserve: listener is v4 loopback with a nonzero port: {}", a.ip() == lo && a.port() != 0);
            a
        }
        Err(e) => {
            println!("p3tcpserve: listener address: {:?}", e.kind());
            return;
        }
    };
    let mut client = match TcpStream::connect(la) {
        Ok(s) => {
            println!("p3tcpserve: connect: ok");
            s
        }
        Err(e) => {
            println!("p3tcpserve: connect: {:?}", e.kind());
            return;
        }
    };
    let (mut server, peer) = match ln.accept() {
        Ok(p) => {
            println!("p3tcpserve: accept: ok");
            p
        }
        Err(e) => {
            println!("p3tcpserve: accept: {:?}", e.kind());
            return;
        }
    };
    println!("p3tcpserve: accepted peer is the client's local address: {}", client.local_addr().ok() == Some(peer));
    println!("p3tcpserve: client's peer is the listener: {}", client.peer_addr().ok() == Some(la));
    println!("p3tcpserve: accepted local address is the listener's: {}", server.local_addr().ok() == Some(la));

    let ping: Vec<u8> = (0..1024u32).map(|i| (i.wrapping_mul(2654435761) >> 24) as u8).collect();
    let pong: Vec<u8> = ping.iter().rev().copied().collect();
    if let Err(e) = client.write_all(&ping) {
        println!("p3tcpserve: client write: {:?}", e.kind());
    }
    if let Err(e) = client.shutdown(Shutdown::Write) {
        println!("p3tcpserve: client shutdown: {:?}", e.kind());
    }
    let mut got = Vec::new();
    match server.read_to_end(&mut got) {
        Ok(n) => println!("p3tcpserve: server read {} of {} bytes, match: {}", n, ping.len(), got == ping),
        Err(e) => println!("p3tcpserve: server read: {:?}", e.kind()),
    }
    if let Err(e) = server.write_all(&pong) {
        println!("p3tcpserve: server write: {:?}", e.kind());
    }
    if let Err(e) = server.shutdown(Shutdown::Write) {
        println!("p3tcpserve: server shutdown: {:?}", e.kind());
    }
    let mut back = Vec::new();
    match client.read_to_end(&mut back) {
        Ok(n) => println!("p3tcpserve: client read {} of {} bytes, match: {}", n, pong.len(), back == pong),
        Err(e) => println!("p3tcpserve: client read: {:?}", e.kind()),
    }
}
