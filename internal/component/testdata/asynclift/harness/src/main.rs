//! Slice 1's concurrency witness: can wasmtime hold TWO component tasks in flight at once?
//!
//! The verdict is a RENDEZVOUS, not a timing observation. `tick` blocks until two calls have arrived, then
//! releases both. A second task cannot reach `tick` unless the first has already suspended inside it, so the
//! second arrival *is* the proof that two tasks are live. No clock is consulted for the verdict.
use std::sync::{Arc, Mutex};
use std::time::Duration;

use wasmtime::component::{Component, Linker};
use wasmtime::{Config, Engine, Error, Result, Store};

const BOUND: Duration = Duration::from_secs(5);

struct Rv {
    arrived: Mutex<u32>,
    notify: tokio::sync::Notify,
    log: Mutex<Vec<String>>,
}

impl Rv {
    fn say(&self, s: String) {
        self.log.lock().unwrap().push(s);
    }
}

struct HostState {
    rv: Arc<Rv>,
}

async fn rendezvous(rv: Arc<Rv>, id: u32) -> Result<u32> {
    rv.say(format!("enter({id})"));
    let n = {
        let mut g = rv.arrived.lock().unwrap();
        *g += 1;
        *g
    };
    if n >= 2 {
        rv.say(format!("release(by {id})"));
        rv.notify.notify_waiters();
        return Ok(id);
    }
    rv.say(format!("suspend({id})"));
    match tokio::time::timeout(BOUND, rv.notify.notified()).await {
        Ok(()) => {
            rv.say(format!("resume({id})"));
            Ok(id)
        }
        Err(_) => {
            rv.say(format!("expire({id})"));
            Err(Error::msg(format!(
                "rendezvous expired after {BOUND:?} with only {n} arrival(s)"
            )))
        }
    }
}

#[tokio::main(flavor = "multi_thread")]
async fn main() -> Result<()> {
    let mode = std::env::args().nth(1).unwrap_or_else(|| "concurrent".into());
    let path = std::env::args().nth(2).unwrap_or_else(|| "../conc.component.wasm".into());

    let mut cfg = Config::new();
    cfg.async_support(true);
    cfg.wasm_component_model_async(true);
    cfg.concurrency_support(true);
    let engine = Engine::new(&cfg)?;
    let component = Component::from_file(&engine, &path)?;

    let rv = Arc::new(Rv {
        arrived: Mutex::new(0),
        notify: tokio::sync::Notify::new(),
        log: Mutex::new(Vec::new()),
    });

    let mut linker: Linker<HostState> = Linker::new(&engine);
    linker.root().func_wrap_concurrent("tick", |acc, (id,): (u32,)| {
        let rv = acc.with(|mut s| s.get().rv.clone());
        Box::pin(async move { rendezvous(rv, id).await.map(|v| (v,)) })
    })?;

    let mut store = Store::new(&engine, HostState { rv: rv.clone() });
    let instance = linker.instantiate_async(&mut store, &component).await?;
    let run = instance.get_typed_func::<(u32,), (u32,)>(&mut store, "run")?;

    // **The outer result is CAPTURED, not propagated.** The first version used `?` here, so when the
    // rendezvous expired the host error escaped through the task machinery and `main` returned before
    // printing EVENTS — losing the diagnostic log at exactly the moment the registration's failure table
    // needs it. A witness must report its evidence on the failing path above all.
    let outer = store
        .run_concurrent(async |acc| -> Result<String> {
            if mode == "sequential" {
                // The second call is not issued until the first resolves, so the rendezvous cannot close.
                let a = run.call_concurrent(acc, (1,)).await;
                let b = run.call_concurrent(acc, (2,)).await;
                Ok(format!("sequential: first={a:?} second={b:?}"))
            } else {
                let (a, b) = tokio::join!(
                    run.call_concurrent(acc, (1,)),
                    run.call_concurrent(acc, (2,))
                );
                Ok(format!("concurrent: first={a:?} second={b:?}"))
            }
        })
        .await;

    let log = rv.log.lock().unwrap().clone();
    println!("MODE      {mode}");
    println!("EVENTS    {}", log.join(" -> "));
    match outer {
        Ok(Ok(s)) => println!("OUTCOME   {s}"),
        Ok(Err(e)) => println!("OUTCOME   guest-call error: {e}"),
        Err(e) => println!("OUTCOME   host/task error: {e}"),
    }
    println!("ARRIVALS  {}", *rv.arrived.lock().unwrap());
    Ok(())
}
