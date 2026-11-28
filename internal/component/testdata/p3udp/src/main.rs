// p3udp: an ordinary Rust wasm32-wasip3 std program over UDP, the witness for E3 (#1376, ADR 0140). It uses std's
// own UdpSocket, with no bindings, because what is under test is what std and wasi-libc do with it:
//
//   * UdpSocket::bind is wasi:sockets' udp-socket.create, then bind; local_addr is get-local-address;
//   * send_to and recv_from are send with an address and receive; connect, send, recv and peer_addr are connect,
//     send with none, receive and get-remote-address;
//   * set_ttl and ttl are the unicast hop limit;
//   * a datagram to a port nobody holds is refused at the next receive, and a read timeout ends a receive.
//
// It takes no arguments: every socket is its own, on loopback. It prints properties, never a port or an address, so
// its output is the same on every host.

use std::net::{IpAddr, Ipv4Addr, Ipv6Addr, SocketAddr, UdpSocket};
use std::time::Duration;

fn pair(ip: IpAddr) -> std::io::Result<(UdpSocket, UdpSocket)> {
    Ok((UdpSocket::bind(SocketAddr::new(ip, 0))?, UdpSocket::bind(SocketAddr::new(ip, 0))?))
}

fn main() {
    let lo4 = IpAddr::V4(Ipv4Addr::LOCALHOST);
    let lo6 = IpAddr::V6(Ipv6Addr::LOCALHOST);
    let (a, b) = match pair(lo4) {
        Ok(p) => p,
        Err(e) => {
            println!("p3udp: bind: {:?}", e.kind());
            return;
        }
    };
    println!("p3udp: bind: ok");
    let (al, bl) = (a.local_addr().unwrap(), b.local_addr().unwrap());
    println!("p3udp: local address is v4 loopback with a port: {}", al.ip() == lo4 && al.port() != 0);
    let mut buf = vec![0u8; 65536];

    // Unconnected: send_to and recv_from.
    match a.send_to(b"hello", bl) {
        Ok(n) => println!("p3udp: send_to: {} bytes", n),
        Err(e) => println!("p3udp: send_to: {:?}", e.kind()),
    }
    match b.recv_from(&mut buf) {
        Ok((n, from)) => println!("p3udp: recv_from: {:?}, from the sender: {}", &buf[..n], from == al),
        Err(e) => println!("p3udp: recv_from: {:?}", e.kind()),
    }

    // Connected: connect, send, recv, peer_addr.
    match a.connect(bl) {
        Ok(()) => println!("p3udp: connect: ok, peer is the other socket: {}", a.peer_addr().map(|p| p == bl).unwrap_or(false)),
        Err(e) => println!("p3udp: connect: {:?}", e.kind()),
    }
    match a.send(b"world") {
        Ok(n) => println!("p3udp: send: {} bytes", n),
        Err(e) => println!("p3udp: send: {:?}", e.kind()),
    }
    match b.recv_from(&mut buf) {
        Ok((n, from)) => println!("p3udp: recv_from after connect: {:?}, from the sender: {}", &buf[..n], from == al),
        Err(e) => println!("p3udp: recv_from after connect: {:?}", e.kind()),
    }
    b.connect(al).unwrap();
    let _ = b.send(b"back");
    match a.recv(&mut buf) {
        Ok(n) => println!("p3udp: recv: {:?}", &buf[..n]),
        Err(e) => println!("p3udp: recv: {:?}", e.kind()),
    }

    // An empty datagram, a large one, and one larger than the caller's buffer.
    let _ = a.send(&[]);
    match b.recv(&mut buf) {
        Ok(n) => println!("p3udp: empty datagram: {} bytes", n),
        Err(e) => println!("p3udp: empty datagram: {:?}", e.kind()),
    }
    let big: Vec<u8> = (0..8192u32).map(|i| (i.wrapping_mul(2654435761) >> 24) as u8).collect();
    let _ = a.send(&big);
    match b.recv(&mut buf) {
        Ok(n) => println!("p3udp: 8192-byte datagram: {} bytes, match: {}", n, buf[..n] == big[..]),
        Err(e) => println!("p3udp: 8192-byte datagram: {:?}", e.kind()),
    }
    let _ = a.send(b"0123456789");
    let mut small = [0u8; 4];
    match b.recv(&mut small) {
        Ok(n) => println!("p3udp: into a 4-byte buffer: {} bytes, {:?}", n, &small[..n]),
        Err(e) => println!("p3udp: into a 4-byte buffer: {:?}", e.kind()),
    }

    // The unicast hop limit.
    match a.set_ttl(42).and_then(|()| a.ttl()) {
        Ok(t) => println!("p3udp: ttl after set_ttl(42): {}", t),
        Err(e) => println!("p3udp: ttl: {:?}", e.kind()),
    }

    // IPv6 loopback.
    match pair(lo6) {
        Err(e) => println!("p3udp: v6 bind: {:?}", e.kind()),
        Ok((c, d)) => {
            let (cl, dl) = (c.local_addr().unwrap(), d.local_addr().unwrap());
            println!("p3udp: v6 local address is ::1 with a port: {}", cl.ip() == lo6 && cl.port() != 0);
            let _ = c.send_to(b"six", dl);
            match d.recv_from(&mut buf) {
                Ok((n, from)) => println!("p3udp: v6 recv_from: {:?}, from the sender: {}", &buf[..n], from == cl),
                Err(e) => println!("p3udp: v6 recv_from: {:?}", e.kind()),
            }
        }
    }

    // A port nobody holds: the datagram is refused, and the refusal is the next receive's.
    let gone = UdpSocket::bind(SocketAddr::new(lo4, 0)).unwrap();
    let port = gone.local_addr().unwrap().port();
    drop(gone);
    let e = UdpSocket::bind(SocketAddr::new(lo4, 0)).unwrap();
    e.connect(SocketAddr::new(lo4, port)).unwrap();
    let _ = e.send(b"anyone?");
    match e.recv(&mut buf) {
        Ok(n) => println!("p3udp: closed port: {} bytes", n),
        Err(err) => println!("p3udp: closed port: {:?}", err.kind()),
    }

    // A read timeout ends a receive nobody answers.
    let f = UdpSocket::bind(SocketAddr::new(lo4, 0)).unwrap();
    match f.set_read_timeout(Some(Duration::from_millis(50))) {
        Err(err) => println!("p3udp: set_read_timeout: {:?}", err.kind()),
        Ok(()) => match f.recv(&mut buf) {
            Ok(n) => println!("p3udp: read timeout: {} bytes", n),
            Err(err) => println!("p3udp: read timeout: {:?}", err.kind()),
        },
    }
    // After the timeout, a datagram that arrives is the next receive's: the receive the timeout ended took nothing.
    let g = UdpSocket::bind(SocketAddr::new(lo4, 0)).unwrap();
    let _ = g.send_to(b"late", f.local_addr().unwrap());
    let _ = f.set_read_timeout(None);
    match f.recv(&mut buf) {
        Ok(n) => println!("p3udp: after the timeout: {:?}", &buf[..n]),
        Err(err) => println!("p3udp: after the timeout: {:?}", err.kind()),
    }

    // Nonblocking: a receive with nothing to read is WouldBlock at once, and the next datagram is the next receive's.
    let h = UdpSocket::bind(SocketAddr::new(lo4, 0)).unwrap();
    let _ = h.set_nonblocking(true);
    match h.recv(&mut buf) {
        Ok(n) => println!("p3udp: nonblocking: {} bytes", n),
        Err(err) => println!("p3udp: nonblocking: {:?}", err.kind()),
    }
    let _ = g.send_to(b"next", h.local_addr().unwrap());
    let _ = h.set_nonblocking(false);
    match h.recv(&mut buf) {
        Ok(n) => println!("p3udp: after nonblocking: {:?}", &buf[..n]),
        Err(err) => println!("p3udp: after nonblocking: {:?}", err.kind()),
    }
}
