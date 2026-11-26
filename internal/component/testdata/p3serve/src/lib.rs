// p3serve: E2-in-b's witness guest (#1385, ADR 0137). It exports wasi:http@0.3's handler through bindings generated
// from the pinned WIT, one row per path, and is served by `wasmtime serve` for the oracle's readings and by
// Burroughs' handler for its own. Every row's response says only what the row is about: a body is the guest's own
// text, never a port or a time.
//
// Most rows return their response first and write its body afterwards from a spawned future, the shape of the
// vendored wasip3 crate's examples/http-proxy.rs: a stream write completes only when the host reads it, and the host
// reads the body only once it has the response, so a body written before the return would wait for ever. A handler
// that returns and then runs on is the case E2-in-a (ADR 0136) made answerable at its return.
//
// What a row observes that the client cannot see, a reader that went away or a handler that was dropped, it writes
// to stderr as one line beginning with the row's name.

wit_bindgen::generate!({ path: "wit", world: "p3serve", generate_all });

use std::future::Future;

use exports::wasi::http::handler::Guest;
use wasi::clocks::monotonic_clock;
use wasi::http::client;
use wasi::http::types::{ErrorCode, Fields, Request, Response, Scheme};
use wit_bindgen::{FutureWriter, StreamReader, StreamResult, StreamWriter};

type Trailers = Result<Option<Fields>, ErrorCode>;

struct P3Serve;

export!(P3Serve);

fn fields(hs: &[(&str, &str)]) -> Fields {
    let list: Vec<(String, Vec<u8>)> = hs.iter().map(|(k, v)| (k.to_string(), v.as_bytes().to_vec())).collect();
    Fields::from_list(&list).unwrap()
}

fn text(v: &[u8]) -> String {
    String::from_utf8_lossy(v).into_owned()
}

// An error's case name: the variant's Debug form up to its payload.
fn ec(e: &ErrorCode) -> String {
    let s = format!("{e:?}");
    s.split(['(', ' ', '{']).next().unwrap_or("").to_string()
}

// respond makes a response of the status and headers, whose body and trailers `body` writes from a spawned future
// once the handler has returned it.
fn respond<F, Fut>(status: u16, hs: &[(&str, &str)], body: F) -> Result<Response, ErrorCode>
where
    F: FnOnce(StreamWriter<u8>, FutureWriter<Trailers>) -> Fut,
    Fut: Future<Output = ()> + 'static,
{
    let (body_tx, body_rx) = wit_stream::new::<u8>();
    let (tr_tx, tr_rx) = wit_future::new::<Trailers>(|| Ok(None));
    let (resp, _sent) = Response::new(fields(hs), Some(body_rx), tr_rx);
    if status != 200 {
        resp.set_status_code(status).unwrap();
    }
    wit_bindgen::spawn_local(body(body_tx, tr_tx));
    Ok(resp)
}

// fixed is a response whose whole body is `b`, and whose trailers are none.
fn fixed(status: u16, hs: &[(&str, &str)], b: Vec<u8>) -> Result<Response, ErrorCode> {
    respond(status, hs, move |mut tx, _tr| async move {
        tx.write_all(b).await;
    })
}

// copy reads `from` to its end into `to`, and reports the bytes copied and whether `to`'s reader went away first.
async fn copy(from: &mut StreamReader<u8>, to: &mut StreamWriter<u8>) -> (usize, bool) {
    let mut n = 0;
    loop {
        let (st, buf) = from.read(Vec::with_capacity(16 << 10)).await;
        if !buf.is_empty() {
            n += buf.len();
            if !to.write_all(buf).await.is_empty() {
                return (n, true);
            }
        }
        if !matches!(st, StreamResult::Complete(_)) {
            return (n, false);
        }
    }
}

// drain reads `from` to its end and returns what it read.
async fn drain(from: &mut StreamReader<u8>) -> Vec<u8> {
    let mut all = Vec::new();
    loop {
        let (st, buf) = from.read(Vec::with_capacity(16 << 10)).await;
        all.extend_from_slice(&buf);
        if !matches!(st, StreamResult::Complete(_)) {
            return all;
        }
    }
}

// The pattern every long body is: byte i is 'a' + i mod 26.
fn pattern(off: usize, n: usize) -> Vec<u8> {
    (off..off + n).map(|i| b'a' + (i % 26) as u8).collect()
}

// Dropped says on stderr that a handler's future was dropped before it finished, which is how wit-bindgen ends an
// export task cancelled before its return.
struct Dropped(&'static str);

impl Drop for Dropped {
    fn drop(&mut self) {
        eprintln!("{}: the handler was dropped before it returned", self.0);
    }
}

impl Guest for P3Serve {
    async fn handle(req: Request) -> Result<Response, ErrorCode> {
        let pq = req.get_path_with_query().unwrap_or_default();
        let route = pq.split('?').next().unwrap_or("").to_string();
        match route.as_str() {
            "/hello" => {
                let line = format!("hello, {:?} {}", req.get_method(), pq);
                fixed(200, &[("content-type", "text/plain"), ("x-guest", "hello")], line.into_bytes())
            }
            "/dump" => {
                // The request as the guest reads it: its method, target, scheme and authority, its headers sorted by
                // name (the host's order is not compared: Go's Header is a map), whether they are immutable, and its
                // options, and its body and trailers.
                let method = format!("{:?}", req.get_method());
                let h = req.get_headers();
                let mut all: Vec<(String, String)> = h.copy_all().into_iter().map(|(k, v)| (k, text(&v))).collect();
                all.sort();
                let set = h.set("x-new", &[b"1".to_vec()]).map_err(|e| format!("{e:?}"));
                drop(h);
                let opts = match req.get_options() {
                    None => "none".to_string(),
                    Some(o) => format!(
                        "connect={:?} first-byte={:?} between-bytes={:?}",
                        o.get_connect_timeout(),
                        o.get_first_byte_timeout(),
                        o.get_between_bytes_timeout()
                    ),
                };
                let (res_tx, res_rx) = wit_future::new::<Result<(), ErrorCode>>(|| Ok(()));
                let (mut body, trailers) = Request::consume_body(req, res_rx);
                let got = drain(&mut body).await;
                let tr = match trailers.await {
                    Ok(None) => "none".to_string(),
                    Ok(Some(t)) => format!("{:?}", t.copy_all().into_iter().map(|(k, v)| (k, text(&v))).collect::<Vec<_>>()),
                    Err(e) => format!("err {}", ec(&e)),
                };
                drop(res_tx);
                let out = format!(
                    "method: {method}\npath-with-query: {pq:?}\nheaders: {all:?}\nheaders set: {set:?}\noptions: {opts}\nbody: {:?}\ntrailers: {tr}\n",
                    text(&got)
                );
                fixed(200, &[("x-guest", "dump")], out.into_bytes())
            }
            "/target" => {
                // The scheme and authority, apart from /dump, since they name the listener's address.
                let out = format!("scheme: {:?}\nauthority: {:?}\n", req.get_scheme(), req.get_authority());
                fixed(200, &[("x-guest", "target")], out.into_bytes())
            }
            "/big" => respond(200, &[("x-guest", "big")], |mut tx, _tr| async move {
                // 4 MiB in 64 KiB writes, more than a socket's buffers hold, so the host must flush as it goes.
                const CHUNK: usize = 64 << 10;
                for i in 0..64 {
                    if !tx.write_all(pattern(i * CHUNK, CHUNK)).await.is_empty() {
                        return;
                    }
                }
            }),
            "/echo" => {
                // The request body, streamed back as it arrives: the response is returned before any of it is read.
                let (res_tx, res_rx) = wit_future::new::<Result<(), ErrorCode>>(|| Ok(()));
                let (mut body, trailers) = Request::consume_body(req, res_rx);
                respond(200, &[("x-guest", "echo")], |mut tx, _tr| async move {
                    copy(&mut body, &mut tx).await;
                    drop(tx);
                    let _ = trailers.await;
                    drop(res_tx);
                })
            }
            "/trailers" => {
                // The request's body and trailers, read before the return; the response's trailers name what was read.
                let (res_tx, res_rx) = wit_future::new::<Result<(), ErrorCode>>(|| Ok(()));
                let (mut body, trailers) = Request::consume_body(req, res_rx);
                let got = drain(&mut body).await;
                let echo = match trailers.await {
                    Ok(Some(t)) => t.get("x-req-trailer").first().map(|v| text(v)).unwrap_or_else(|| "absent".to_string()),
                    Ok(None) => "none".to_string(),
                    Err(e) => format!("err {}", ec(&e)),
                };
                drop(res_tx);
                let n = got.len().to_string();
                respond(200, &[("x-guest", "trailers"), ("trailer", "x-sum, x-echo")], move |mut tx, tr| async move {
                    tx.write_all(format!("read {n} bytes").into_bytes()).await;
                    drop(tx);
                    let t = fields(&[("x-sum", n.as_str()), ("x-echo", echo.as_str())]);
                    let _ = tr.write(Ok(Some(t))).await;
                })
            }
            "/trailers-err" => respond(200, &[("x-guest", "trailers-err")], |mut tx, tr| async move {
                // A body, then its trailers future written as an error: the body did not end well.
                tx.write_all(b"partial".to_vec()).await;
                drop(tx);
                let _ = tr.write(Err(ErrorCode::InternalError(None))).await;
            }),
            "/status" => fixed(418, &[("x-guest", "teapot")], b"short and stout".to_vec()),
            "/fixed" => fixed(200, &[("x-guest", "fixed"), ("content-length", "5")], b"fixed".to_vec()),
            "/err" => Err(ErrorCode::HttpRequestDenied),
            "/err-internal" => Err(ErrorCode::InternalError(Some("p3serve: an internal error".to_string()))),
            "/trap" => panic!("p3serve: a trap before the return"),
            "/trap-after" => respond(200, &[("x-guest", "trap-after")], |mut tx, _tr| async move {
                tx.write_all(b"partial".to_vec()).await;
                panic!("p3serve: a trap after the return");
            }),
            "/late" => respond(200, &[("x-guest", "late")], |mut tx, tr| async move {
                // Returned, then three writes with a wait between each: the task runs on, suspending, after the answer.
                for (i, part) in ["one ", "two ", "three"].iter().enumerate() {
                    if i > 0 {
                        monotonic_clock::wait_for(1_000_000).await;
                    }
                    tx.write_all(part.as_bytes().to_vec()).await;
                }
                drop(tx);
                let _ = tr.write(Ok(Some(fields(&[("x-late", "done")])))).await;
            }),
            "/forever" => respond(200, &[("x-guest", "forever")], |mut tx, _tr| async move {
                // An endless body: the client reads some and goes away, and the guest is told by its write.
                let mut i = 0;
                loop {
                    if !tx.write_all(pattern(i * 4096, 4096)).await.is_empty() {
                        eprintln!("forever: the body's reader went away");
                        return;
                    }
                    i += 1;
                }
            }),
            "/stall" => {
                // The request body read before the return, which the client never finishes: it goes away instead.
                let guard = Dropped("stall");
                let (res_tx, res_rx) = wit_future::new::<Result<(), ErrorCode>>(|| Ok(()));
                let (mut body, trailers) = Request::consume_body(req, res_rx);
                let got = drain(&mut body).await;
                let tr = match trailers.await {
                    Ok(_) => "ok".to_string(),
                    Err(e) => format!("err {}", ec(&e)),
                };
                std::mem::forget(guard);
                eprintln!("stall: the request body ended after {} bytes, its trailers {tr}", got.len());
                drop(res_tx);
                fixed(200, &[("x-guest", "stall")], b"stalled".to_vec())
            }
            "/sleep" => {
                // A handler that neither reads its body nor returns for a while, whose client goes away meanwhile: it is
                // told only if the host cancels its task, and then wit-bindgen drops this future. It says it has entered,
                // so the client goes away only once the handler is running.
                let guard = Dropped("sleep");
                eprintln!("sleep: the handler entered");
                monotonic_clock::wait_for(100_000_000).await;
                std::mem::forget(guard);
                eprintln!("sleep: the wait ended and the handler returned");
                fixed(200, &[("x-guest", "sleep")], b"slept".to_vec())
            }
            "/send" => {
                // An outgoing request from inside the handler, to the peer the query names: serve grants outgoing
                // HTTP, as wasmtime's does. The peer's address is not in the answer, only its status and body.
                let to = pq.split("to=").nth(1).unwrap_or("").to_string();
                let (tx, rx) = wit_future::new::<Trailers>(|| Ok(None));
                let (out, _sent) = Request::new(Fields::new(), None, rx, None);
                drop(tx);
                out.set_scheme(Some(&Scheme::Http)).unwrap();
                out.set_authority(Some(&to)).unwrap();
                out.set_path_with_query(Some("/peer")).unwrap();
                let line = match client::send(out).await {
                    Err(e) => format!("send: err {}", ec(&e)),
                    Ok(resp) => {
                        let status = resp.get_status_code();
                        let (res_tx, res_rx) = wit_future::new::<Result<(), ErrorCode>>(|| Ok(()));
                        let (body, trailers) = Response::consume_body(resp, res_rx);
                        let b = body.collect().await;
                        let _ = trailers.await;
                        drop(res_tx);
                        format!("send: status {status}, body {:?}", text(&b))
                    }
                };
                fixed(200, &[("x-guest", "send")], line.into_bytes())
            }
            _ => fixed(404, &[("x-guest", "none")], format!("no row {route}").into_bytes()),
        }
    }
}
