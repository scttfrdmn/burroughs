// p3http: E2-out's witness guest (#1383, ADR 0135). It drives wasi:http@0.3's outgoing half through bindings
// generated from the pinned WIT: the four resources, request.new and the consume-bodies with their streams and
// futures, and client.send against a peer on 127.0.0.1. Each run is one row, `p3http <row> <port> <closed-port>`, and
// it prints PROPERTIES of the answers, never a port: an error is its case name only, since the payload strings are an
// implementation's (wasmtime's DNS-error carries one, and the WIT says not to depend on them).
//
// The peer is testdata/p3http/peer for the oracle's readings and its twin, httpPeer in p3http_test.go, for
// Burroughs'.

wit_bindgen::generate!({ path: "wit", world: "p3http", generate_all });

use wasi::http::client;
use wasi::http::types::{ErrorCode, Fields, HeaderError, Method, Request, RequestOptions, Response, Scheme};

type Trailers = Result<Option<Fields>, ErrorCode>;

// An error's case name: the variant's Debug form up to its payload.
fn ec(e: &ErrorCode) -> String {
    let s = format!("{e:?}");
    s.split(['(', ' ', '{']).next().unwrap_or("").to_string()
}

fn he(e: &HeaderError) -> String {
    let s = format!("{e:?}");
    s.split(['(', ' ', '{']).next().unwrap_or("").to_string()
}

fn text(v: &[u8]) -> String {
    String::from_utf8_lossy(v).into_owned()
}

// A request to the peer with no body, its trailers future resolved ok(none) by the writer's drop.
fn req_to(port: &str, method: Method, path: &str) -> (Request, wit_bindgen::FutureReader<Result<(), ErrorCode>>) {
    let (tx, rx) = wit_future::new::<Trailers>(|| Ok(None));
    let (req, sent) = Request::new(Fields::new(), None, rx, None);
    drop(tx);
    req.set_method(&method).unwrap();
    req.set_scheme(Some(&Scheme::Http)).unwrap();
    req.set_authority(Some(&format!("127.0.0.1:{port}"))).unwrap();
    req.set_path_with_query(Some(path)).unwrap();
    (req, sent)
}

// show prints a send's outcome: the status, five headers by name, the response headers' immutability, the body, the
// trailers, and the `res` write.
async fn show(resp: Result<Response, ErrorCode>) {
    let resp = match resp {
        Err(e) => {
            println!("send: err {}", ec(&e));
            return;
        }
        Ok(resp) => resp,
    };
    println!("status: {}", resp.get_status_code());
    let h = resp.get_headers();
    for name in ["x-peer", "content-type", "content-length", "transfer-encoding", "trailer"] {
        let vs: Vec<String> = h.get(name).iter().map(|v| text(v)).collect();
        println!("header {name}: {vs:?}");
    }
    println!("headers set: {:?}", h.set("x-new", &[b"1".to_vec()]).map_err(|e| he(&e)));
    println!("headers append: {:?}", h.append("x-new", b"1").map_err(|e| he(&e)));
    println!("headers delete: {:?}", h.delete("x-peer").map_err(|e| he(&e)));
    println!("headers get-and-delete: {:?}", h.get_and_delete("x-peer").map(|v| v.len()).map_err(|e| he(&e)));
    drop(h);
    let (res_tx, res_rx) = wit_future::new::<Result<(), ErrorCode>>(|| Ok(()));
    let (body, trailers) = Response::consume_body(resp, res_rx);
    let bytes = body.collect().await;
    if bytes.len() <= 512 {
        println!("body: {:?}", text(&bytes));
    } else {
        let ok = bytes.iter().enumerate().all(|(i, &b)| b == b'a' + (i % 26) as u8);
        println!("body: {} bytes, the peer's pattern: {ok}", bytes.len());
    }
    match trailers.await {
        Ok(None) => println!("trailers: none"),
        Ok(Some(t)) => {
            let all: Vec<(String, String)> = t.copy_all().into_iter().map(|(k, v)| (k, text(&v))).collect();
            println!("trailers: {all:?}");
            println!("trailers set: {:?}", t.set("x-new", &[b"1".to_vec()]).map_err(|e| he(&e)));
        }
        Err(e) => println!("trailers: err {}", ec(&e)),
    }
    println!("res write: {:?}", res_tx.write(Ok(())).await.is_ok());
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    let row = args.get(1).cloned().unwrap_or_default();
    let port = args.get(2).cloned().unwrap_or_default();
    let closed = args.get(3).cloned().unwrap_or_default();
    wit_bindgen::block_on(async move {
        match row.as_str() {
            "get" => {
                let (req, sent) = req_to(&port, Method::Get, "/hello");
                println!("request headers set: {:?}", req.get_headers().set("x", &[b"1".to_vec()]).map_err(|e| he(&e)));
                show(client::send(req).await).await;
                println!("sent: {:?}", sent.await.map_err(|e| ec(&e)));
            }
            "head" => {
                let (req, sent) = req_to(&port, Method::Head, "/hello");
                show(client::send(req).await).await;
                println!("sent: {:?}", sent.await.map_err(|e| ec(&e)));
            }
            "purge" => {
                let (req, sent) = req_to(&port, Method::Other("PURGE".to_string()), "/hello");
                show(client::send(req).await).await;
                println!("sent: {:?}", sent.await.map_err(|e| ec(&e)));
            }
            "dump" => {
                // What the host puts on the wire beside the guest's headers: the peer answers with what it read.
                let (tx, rx) = wit_future::new::<Trailers>(|| Ok(None));
                let hs = Fields::from_list(&[("X-A".to_string(), b"1".to_vec()), ("x-a".to_string(), b"2".to_vec())]).unwrap();
                let (req, sent) = Request::new(hs, None, rx, None);
                drop(tx);
                req.set_scheme(Some(&Scheme::Http)).unwrap();
                req.set_authority(Some(&format!("127.0.0.1:{port}"))).unwrap();
                req.set_path_with_query(Some("/dump?q=1&r")).unwrap();
                show(client::send(req).await).await;
                println!("sent: {:?}", sent.await.map_err(|e| ec(&e)));
            }
            "post" | "big" | "post-declared" | "post-cl" | "post-empty" | "post-dropbody" => {
                // A body written through the stream while the send is in flight, then the trailers future.
                let n: usize = match row.as_str() {
                    "big" => 256 * 1024,
                    "post-empty" | "post-dropbody" => 0,
                    _ => 8,
                };
                let (mut body_tx, body_rx) = wit_stream::new::<u8>();
                let (tr_tx, tr_rx) = wit_future::new::<Trailers>(|| Ok(None));
                let hs = Fields::from_list(&[("x-req".to_string(), b"yes".to_vec())]).unwrap();
                if row == "post-declared" {
                    hs.append("trailer", b"x-req-trailer").unwrap();
                }
                if row == "post-cl" {
                    hs.append("content-length", b"8").unwrap();
                }
                let (req, sent) = Request::new(hs, Some(body_rx), tr_rx, None);
                req.set_method(&Method::Post).unwrap();
                req.set_scheme(Some(&Scheme::Http)).unwrap();
                req.set_authority(Some(&format!("127.0.0.1:{port}"))).unwrap();
                req.set_path_with_query(Some("/echo")).unwrap();
                let send = client::send(req);
                let dropbody = row == "post-dropbody";
                let write = async move {
                    if n > 0 {
                        let data: Vec<u8> = (0..n).map(|i| b'a' + (i % 26) as u8).collect();
                        println!("body write rest: {}", body_tx.write_all(data).await.len());
                    }
                    drop(body_tx);
                    if dropbody {
                        println!("trailers write err: {:?}", tr_tx.write(Err(ErrorCode::HttpProtocolError)).await.is_ok());
                        return;
                    }
                    let t = Fields::from_list(&[("x-req-trailer".to_string(), b"tail".to_vec())]).unwrap();
                    println!("trailers write: {:?}", tr_tx.write(Ok(Some(t))).await.is_ok());
                };
                let (resp, ()) = join(send, write).await;
                show(resp).await;
                println!("sent: {:?}", sent.await.map_err(|e| ec(&e)));
            }
            "bigresp" => {
                let (req, sent) = req_to(&port, Method::Get, "/big");
                show(client::send(req).await).await;
                println!("sent: {:?}", sent.await.map_err(|e| ec(&e)));
            }
            "early-drop" => {
                // The body dropped after one short read: the trailers future still resolves.
                let (req, sent) = req_to(&port, Method::Get, "/big");
                match client::send(req).await {
                    Err(e) => println!("send: err {}", ec(&e)),
                    Ok(resp) => {
                        let (res_tx, res_rx) = wit_future::new::<Result<(), ErrorCode>>(|| Ok(()));
                        let (mut body, trailers) = Response::consume_body(resp, res_rx);
                        let (_, buf) = body.read(Vec::with_capacity(10)).await;
                        println!("first read: {} bytes", buf.len());
                        drop(body);
                        match trailers.await {
                            Ok(None) => println!("trailers: none"),
                            Ok(Some(_)) => println!("trailers: some"),
                            Err(e) => println!("trailers: err {}", ec(&e)),
                        }
                        println!("res write: {:?}", res_tx.write(Ok(())).await.is_ok());
                    }
                }
                println!("sent: {:?}", sent.await.map_err(|e| ec(&e)));
            }
            "res-first" => {
                // The `res` future written before the body is read: it completes only if the host reads it at once.
                let (req, sent) = req_to(&port, Method::Get, "/hello");
                match client::send(req).await {
                    Err(e) => println!("send: err {}", ec(&e)),
                    Ok(resp) => {
                        let (res_tx, res_rx) = wit_future::new::<Result<(), ErrorCode>>(|| Ok(()));
                        let (body, trailers) = Response::consume_body(resp, res_rx);
                        println!("res write first: {:?}", res_tx.write(Err(ErrorCode::HttpProtocolError)).await.is_ok());
                        println!("body: {:?}", text(&body.collect().await));
                        println!("trailers none: {}", matches!(trailers.await, Ok(None)));
                    }
                }
                println!("sent: {:?}", sent.await.map_err(|e| ec(&e)));
            }
            "refused" => {
                let (req, sent) = req_to(&closed, Method::Get, "/");
                show(client::send(req).await).await;
                println!("sent: {:?}", sent.await.map_err(|e| ec(&e)));
            }
            "dns" => {
                // .invalid never resolves (RFC 6761), so the row needs no DNS server to fail the same way.
                let (req, sent) = req_to(&port, Method::Get, "/");
                req.set_authority(Some("burroughs-e2out.invalid")).unwrap();
                show(client::send(req).await).await;
                println!("sent: {:?}", sent.await.map_err(|e| ec(&e)));
            }
            "https" | "otherscheme" | "noscheme" | "nopath" | "noauthority" => {
                let (req, sent) = req_to(&port, Method::Get, "/hello");
                match row.as_str() {
                    "https" => req.set_scheme(Some(&Scheme::Https)).unwrap(),
                    "otherscheme" => req.set_scheme(Some(&Scheme::Other("ftp".to_string()))).unwrap(),
                    "noscheme" => req.set_scheme(None).unwrap(),
                    "nopath" => req.set_path_with_query(None).unwrap(),
                    _ => req.set_authority(None).unwrap(),
                }
                show(client::send(req).await).await;
                println!("sent: {:?}", sent.await.map_err(|e| ec(&e)));
            }
            "timeout" => {
                // The peer's /stall never answers, so the first-byte timeout is the only way the send ends.
                let (tx, rx) = wit_future::new::<Trailers>(|| Ok(None));
                let opts = RequestOptions::new();
                println!("first-byte set: {:?}", opts.set_first_byte_timeout(Some(200_000_000)).is_ok());
                let (req, sent) = Request::new(Fields::new(), None, rx, Some(opts));
                drop(tx);
                req.set_scheme(Some(&Scheme::Http)).unwrap();
                req.set_authority(Some(&format!("127.0.0.1:{port}"))).unwrap();
                req.set_path_with_query(Some("/stall")).unwrap();
                show(client::send(req).await).await;
                println!("sent: {:?}", sent.await.map_err(|e| ec(&e)));
            }
            "fields" => fields_row(),
            "order" => order_row(),
            "request" => request_row(),
            "options" => options_row(),
            "options-child-drop" => {
                let (tx, rx) = wit_future::new::<Trailers>(|| Ok(None));
                let (req, _sent) = Request::new(Fields::new(), None, rx, Some(RequestOptions::new()));
                drop(tx);
                let o = req.get_options().unwrap();
                let h = req.get_headers();
                drop(req);
                println!("parent dropped first; options child connect: {:?}", o.get_connect_timeout());
                println!("parent dropped first; headers child copy-all: {:?}", h.copy_all());
            }
            "response" => {
                let (tx, rx) = wit_future::new::<Trailers>(|| Ok(None));
                let hs = Fields::from_list(&[("x-r".to_string(), b"1".to_vec())]).unwrap();
                let (resp, _sent) = Response::new(hs, None, rx);
                drop(tx);
                println!("status: {}", resp.get_status_code());
                for s in [99u16, 1000, 404, 100, 999, 599, 600, 199, 0, 65535] {
                    println!("set {s}: {:?}", resp.set_status_code(s));
                }
                println!("status: {}", resp.get_status_code());
                let h = resp.get_headers();
                println!("headers copy-all: {:?}", h.copy_all());
                println!("headers set: {:?}", h.set("x", &[b"1".to_vec()]).map_err(|e| he(&e)));
                println!("immutable bad name: {:?}", h.append("a b", b"1").map_err(|e| he(&e)));
                println!("immutable forbidden: {:?}", h.append("connection", b"1").map_err(|e| he(&e)));
                println!("immutable delete missing: {:?}", h.delete("missing").map_err(|e| he(&e)));
            }
            "response-consume" | "request-consume" => {
                // A guest-made message consumed by the guest: its own body stream back, then its own trailers future,
                // whose read is an intra-component copy of a non-numeric payload, a trap on both oracles' terms.
                let (mut body_tx, body_rx) = wit_stream::new::<u8>();
                let (tr_tx, tr_rx) = wit_future::new::<Trailers>(|| Ok(None));
                let (res_tx, res_rx) = wit_future::new::<Result<(), ErrorCode>>(|| Ok(()));
                let (body, trailers) = if row == "response-consume" {
                    let (resp, _sent) = Response::new(Fields::new(), Some(body_rx), tr_rx);
                    Response::consume_body(resp, res_rx)
                } else {
                    let (req, _sent) = Request::new(Fields::new(), Some(body_rx), tr_rx, None);
                    Request::consume_body(req, res_rx)
                };
                let write = async move {
                    println!("body write rest: {}", body_tx.write_all(b"round trip".to_vec()).await.len());
                    drop(body_tx);
                };
                let read = async move { println!("body: {:?}", text(&body.collect().await)) };
                join(write, read).await;
                drop(res_tx);
                let t = Fields::new();
                let write = async move { println!("trailers write: {:?}", tr_tx.write(Ok(Some(t))).await.is_ok()) };
                let read = async move { println!("trailers ok: {}", trailers.await.is_ok()) };
                join(write, read).await;
            }
            "consume-empty" => {
                // contents none: the body is an empty stream.
                let (tr_tx, tr_rx) = wit_future::new::<Trailers>(|| Ok(None));
                let (req, _sent) = Request::new(Fields::new(), None, tr_rx, None);
                let (res_tx, res_rx) = wit_future::new::<Result<(), ErrorCode>>(|| Ok(()));
                let (body, trailers) = Request::consume_body(req, res_rx);
                println!("body: {} bytes", body.collect().await.len());
                drop(trailers);
                drop(tr_tx);
                drop(res_tx);
            }
            "consume-request-res" | "consume-response-res" => {
                // The value written to `res` becomes new's transmission future's value.
                let (mut body_tx, body_rx) = wit_stream::new::<u8>();
                let (tr_tx, tr_rx) = wit_future::new::<Trailers>(|| Ok(None));
                let (res_tx, res_rx) = wit_future::new::<Result<(), ErrorCode>>(|| Ok(()));
                let (body, trailers, sent) = if row == "consume-request-res" {
                    let (req, sent) = Request::new(Fields::new(), Some(body_rx), tr_rx, None);
                    let (b, t) = Request::consume_body(req, res_rx);
                    (b, t, sent)
                } else {
                    let (resp, sent) = Response::new(Fields::new(), Some(body_rx), tr_rx);
                    let (b, t) = Response::consume_body(resp, res_rx);
                    (b, t, sent)
                };
                let write = async move {
                    println!("body write rest: {}", body_tx.write_all(b"xyz".to_vec()).await.len());
                    drop(body_tx);
                };
                let read = async move { println!("body: {:?}", text(&body.collect().await)) };
                join(write, read).await;
                drop(trailers);
                drop(tr_tx);
                println!("res write: {:?}", res_tx.write(Err(ErrorCode::HttpProtocolError)).await.is_ok());
                println!("sent: {:?}", sent.await.map_err(|e| ec(&e)));
            }
            "request-drop" | "response-drop" => {
                let (tr_tx, tr_rx) = wit_future::new::<Trailers>(|| Ok(None));
                let sent = if row == "request-drop" {
                    let (req, sent) = Request::new(Fields::new(), None, tr_rx, None);
                    drop(req);
                    sent
                } else {
                    let (resp, sent) = Response::new(Fields::new(), None, tr_rx);
                    drop(resp);
                    sent
                };
                drop(tr_tx);
                println!("sent: {:?}", sent.await.map_err(|e| ec(&e)));
            }
            _ => println!("unknown row {row:?}"),
        }
    });
}

fn fields_row() {
    let f = Fields::new();
    println!("new has: {}", f.has("a"));
    println!("append a 1: {:?}", f.append("a", b"1").map_err(|e| he(&e)));
    println!("append A 2: {:?}", f.append("A", b"2").map_err(|e| he(&e)));
    println!("get a: {:?}", f.get("a"));
    println!("get A: {:?}", f.get("A"));
    println!("has A: {}", f.has("A"));
    println!("copy-all: {:?}", f.copy_all());
    println!("set b [x, y]: {:?}", f.set("b", &[b"x".to_vec(), b"y".to_vec()]).map_err(|e| he(&e)));
    println!("set b []: {:?}", f.set("b", &[]).map_err(|e| he(&e)));
    println!("has b after the empty set: {}", f.has("b"));
    println!("set a [3]: {:?}", f.set("a", &[b"3".to_vec()]).map_err(|e| he(&e)));
    println!("copy-all: {:?}", f.copy_all());
    println!("get-and-delete a: {:?}", f.get_and_delete("a").map_err(|e| he(&e)));
    println!("get-and-delete missing: {:?}", f.get_and_delete("missing").map_err(|e| he(&e)));
    println!("delete missing: {:?}", f.delete("missing").map_err(|e| he(&e)));
    println!("append empty value: {:?}", f.append("e", b"").map_err(|e| he(&e)));
    println!("get e: {:?}", f.get("e"));
    for (what, name) in [("bad name", "bad name"), ("empty name", ""), ("colon", "a:b"), ("utf-8", "é"), ("tchars", "!#$%&'*+-.^_`|~09az")] {
        println!("append name {what}: {:?}", f.append(name, b"1").map_err(|e| he(&e)));
    }
    for (what, value) in [("LF", &b"a\nb"[..]), ("NUL", b"a\0b"), ("CR", b"a\rb"), ("DEL", &[0x7f]), ("0xff", &[0xff]), ("HTAB", b"a\tb"), ("spaces", b" x ")] {
        println!("append value {what}: {:?}", f.append("v", value).map_err(|e| he(&e)));
    }
    println!("get v: {:?}", f.get("v"));
    println!("has bad name: {}", f.has("bad name"));
    println!("get bad name: {:?}", f.get("bad name"));
    println!("delete bad name: {:?}", f.delete("bad name").map_err(|e| he(&e)));
    println!("get-and-delete bad name: {:?}", f.get_and_delete("a b").map_err(|e| he(&e)));
    for name in [
        "connection", "keep-alive", "proxy-connection", "transfer-encoding", "upgrade", "host", "http2-settings", "te",
        "proxy-authorization", "proxy-authenticate", "trailer", "expect", "content-length",
    ] {
        println!("append {name}: {:?}", f.append(name, b"x").map_err(|e| he(&e)));
    }
    println!("set forbidden: {:?}", f.set("connection", &[b"x".to_vec()]).map_err(|e| he(&e)));
    println!("set with a bad value: {:?}", f.set("ok", &[b"1".to_vec(), b"\n".to_vec()]).map_err(|e| he(&e)));
    println!("has ok: {}", f.has("ok"));
    println!("delete forbidden: {:?}", f.delete("connection").map_err(|e| he(&e)));
    println!("get-and-delete forbidden: {:?}", f.get_and_delete("connection").map_err(|e| he(&e)));
    println!("has forbidden: {}", f.has("connection"));
    for v in [&b"8"[..], b"08", b"", b"1,1", b"-1"] {
        println!("append content-length {:?}: {:?}", text(v), f.append("content-length", v).map_err(|e| he(&e)));
    }
    let list = |entries: &[(&str, &[u8])]| -> Result<Vec<(String, Vec<u8>)>, String> {
        let e: Vec<(String, Vec<u8>)> = entries.iter().map(|(k, v)| (k.to_string(), v.to_vec())).collect();
        Fields::from_list(&e).map(|f| f.copy_all()).map_err(|e| he(&e))
    };
    println!("from-list ok: {:?}", list(&[("a", b"1"), ("A", b"2"), ("X-Up", b"3")]));
    println!("from-list bad name: {:?}", list(&[("a b", b"1")]));
    println!("from-list bad value: {:?}", list(&[("a", b"\n")]));
    println!("from-list forbidden: {:?}", list(&[("connection", b"x")]));
    let c = f.clone();
    println!("clone append: {:?}", c.append("z", b"1").map_err(|e| he(&e)));
    println!("original has z: {}", f.has("z"));
    println!("clone copy-all: {:?}", c.copy_all());
}

// order_row reads the order copy-all answers across appends, a set and two removals.
fn order_row() {
    let f = Fields::new();
    for n in ["a", "b", "c", "d"] {
        f.append(n, n.as_bytes()).unwrap();
    }
    f.append("b", b"b2").unwrap();
    println!("order: {:?}", f.copy_all());
    f.delete("a").unwrap();
    println!("after delete a: {:?}", f.copy_all());
    f.set("b", &[b"B".to_vec()]).unwrap();
    println!("after set b: {:?}", f.copy_all());
    f.append("a", b"a2").unwrap();
    println!("after append a: {:?}", f.copy_all());
    let _ = f.get_and_delete("c");
    println!("after get-and-delete c: {:?}", f.copy_all());
    f.set("e", &[b"1".to_vec(), b"2".to_vec()]).unwrap();
    println!("after set e: {:?}", f.copy_all());
}

fn request_row() {
    let (tx, rx) = wit_future::new::<Trailers>(|| Ok(None));
    let (req, _sent) = Request::new(Fields::new(), None, rx, None);
    drop(tx);
    println!("method: {:?}", req.get_method());
    println!("path: {:?}", req.get_path_with_query());
    println!("scheme: {:?}", req.get_scheme());
    println!("authority: {:?}", req.get_authority());
    println!("options: {:?}", req.get_options().is_some());
    for m in ["GET", "PURGE", "get", "BAD METHOD", "", "POST"] {
        println!("set-method other {m:?}: {:?}", req.set_method(&Method::Other(m.to_string())));
        println!("method: {:?}", req.get_method());
    }
    println!("set-method delete: {:?}", req.set_method(&Method::Delete));
    println!("method: {:?}", req.get_method());
    for p in ["/a?b", "/a b", "", "a", "*", "/a#f", "/{x}", "/?q=<", "/é", "/%41"] {
        println!("set-path {p:?}: {:?}", req.set_path_with_query(Some(p)));
        println!("path: {:?}", req.get_path_with_query());
    }
    println!("set-path none: {:?}", req.set_path_with_query(None));
    println!("path: {:?}", req.get_path_with_query());
    for s in ["ftp", "http", "HTTPS", "b d", "1x", "a+b-c.d"] {
        println!("set-scheme other {s:?}: {:?}", req.set_scheme(Some(&Scheme::Other(s.to_string()))));
        println!("scheme: {:?}", req.get_scheme());
    }
    println!("set-scheme none: {:?}", req.set_scheme(None));
    println!("scheme: {:?}", req.get_scheme());
    for a in ["example.com:8080", "a b", "u@example.com", "", "[::1]:80", "h:x", "h:", "a/b", "a?b"] {
        println!("set-authority {a:?}: {:?}", req.set_authority(Some(a)));
        println!("authority: {:?}", req.get_authority());
    }
    println!("set-authority none: {:?}", req.set_authority(None));
    println!("authority: {:?}", req.get_authority());
    let h = req.get_headers();
    println!("headers set: {:?}", h.set("x", &[b"1".to_vec()]).map_err(|e| he(&e)));
    println!("headers copy-all: {:?}", h.copy_all());
    let h2 = h.clone();
    println!("headers clone append: {:?}", h2.append("x", b"1").map_err(|e| he(&e)));
}

fn options_row() {
    let o = RequestOptions::new();
    println!("connect: {:?}", o.get_connect_timeout());
    println!("first-byte: {:?}", o.get_first_byte_timeout());
    println!("between-bytes: {:?}", o.get_between_bytes_timeout());
    println!("set connect 1s: {:?}", o.set_connect_timeout(Some(1_000_000_000)));
    println!("set first-byte 2s: {:?}", o.set_first_byte_timeout(Some(2_000_000_000)));
    println!("set between-bytes 3s: {:?}", o.set_between_bytes_timeout(Some(3_000_000_000)));
    println!("connect: {:?}", o.get_connect_timeout());
    println!("first-byte: {:?}", o.get_first_byte_timeout());
    println!("between-bytes: {:?}", o.get_between_bytes_timeout());
    println!("set connect none: {:?}", o.set_connect_timeout(None));
    println!("connect: {:?}", o.get_connect_timeout());
    println!("set connect 0: {:?}", o.set_connect_timeout(Some(0)));
    println!("connect: {:?}", o.get_connect_timeout());
    let c = o.clone();
    println!("clone first-byte: {:?}", c.get_first_byte_timeout());
    println!("clone set: {:?}", c.set_connect_timeout(Some(5)));
    println!("original connect: {:?}", o.get_connect_timeout());
    let (tx, rx) = wit_future::new::<Trailers>(|| Ok(None));
    let (req, _sent) = Request::new(Fields::new(), None, rx, Some(o));
    drop(tx);
    let g = req.get_options().unwrap();
    println!("got first-byte: {:?}", g.get_first_byte_timeout());
    println!("got set: {:?}", g.set_connect_timeout(Some(5)));
    let gc = g.clone();
    println!("got clone set: {:?}", gc.set_connect_timeout(Some(5)));
    drop(g);
    let g2 = req.get_options().unwrap();
    println!("second get-options first-byte: {:?}", g2.get_first_byte_timeout());
}

// A two-future join, so the guest needs no futures crate: each future is polled until both are done.
async fn join<A: std::future::Future, B: std::future::Future>(a: A, b: B) -> (A::Output, B::Output) {
    use std::pin::pin;
    use std::task::Poll;
    let mut a = pin!(a);
    let mut b = pin!(b);
    let (mut ra, mut rb) = (None, None);
    std::future::poll_fn(|cx| {
        if ra.is_none() {
            if let Poll::Ready(v) = a.as_mut().poll(cx) {
                ra = Some(v);
            }
        }
        if rb.is_none() {
            if let Poll::Ready(v) = b.as_mut().poll(cx) {
                rb = Some(v);
            }
        }
        if ra.is_some() && rb.is_some() {
            Poll::Ready(())
        } else {
            Poll::Pending
        }
    })
    .await;
    (ra.unwrap(), rb.unwrap())
}
