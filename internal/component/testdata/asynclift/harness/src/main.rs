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
    /// Fired by `tick` once the guest has arrived and is about to go pending. **This is the
    /// cancellation arm's drop signal**: the host drops the call future when the guest has
    /// demonstrably suspended, never after a duration (#857 amendment 3).
    arrived_sig: tokio::sync::Notify,
    /// Set by the `note` host import — the guest's receipt that the cancellation reached it.
    ///
    /// **A latch, not a `Notify`.** `notify_waiters` stores no permit, so a receipt arriving while
    /// nobody is awaiting is lost — and the receipt arrives during the engine's cancellation work,
    /// which runs when the canceller is *not* waiting. The first version used a `Notify` here and
    /// could not have observed a receipt even if one had been sent.
    receipt: Mutex<Option<u32>>,
    /// Set when the pending `tick`'s future is dropped. Captures what becomes of a host call left
    /// in flight when its task is cancelled, which Burroughs will have to match.
    tick_dropped: Mutex<bool>,
}

/// Records that the pending host `tick` was dropped. A guard rather than a flag set by hand, because
/// the thing being measured is *the host future being dropped*, and only a `Drop` impl observes that.
struct TickDropped(Arc<Rv>);

impl Drop for TickDropped {
    fn drop(&mut self) {
        *self.0.tick_dropped.lock().unwrap() = true;
        self.0.say("tick-dropped".into());
    }
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

/// The INLINE arm's `tick`: it resolves at once, with a value derived from its argument (#870).
///
/// # Why this arm exists, and why the value is derived
///
/// Removing the bare-import blocker let Burroughs run the suspending guest with a `tick` that resolves
/// immediately — but **none of the committed readings covers that shape**: they all use the rendezvous
/// `tick`, which defers. So Burroughs' output had nothing to check against, and a number with no
/// reference is not evidence.
///
/// `id + 100` rather than a constant, for the same reason the `compute` reading commits two pairs: a
/// constant could not tell that the argument reached the host import at all. Two calls then discriminate
/// a passthrough and a constant from the real thing.
async fn inline_tick(rv: Arc<Rv>, id: u32) -> Result<u32> {
    rv.say(format!("enter({id})"));
    {
        let mut g = rv.arrived.lock().unwrap();
        *g += 1;
    }
    rv.say(format!("inline-resolve({id})"));
    Ok(id + 100)
}

/// The cancellation arm's `tick`: it **never completes**.
///
/// Held pending with no bound, deliberately (#857 amendment 3). The rendezvous `tick` releases on a second
/// arrival and expires at `BOUND`; reused here that makes the arm a race — if `tick` returned before the
/// drop took effect the guest would resume, disarm its receipt guard and call `task.return`, and the arm
/// would record a **normal completion** while claiming to be the positive cancellation arm. A reading that
/// is right about the fact and wrong about the reason.
///
/// With no completion path, cancellation is the only way the call can end, so the verdict cannot turn on
/// which `select!` branch won by a hair.
async fn pending_tick(rv: Arc<Rv>, id: u32) -> Result<u32> {
    rv.say(format!("enter({id})"));
    {
        let mut g = rv.arrived.lock().unwrap();
        *g += 1;
    }
    let _dropped = TickDropped(rv.clone());
    rv.say(format!("suspend({id})"));
    rv.arrived_sig.notify_waiters();
    std::future::pending::<()>().await;
    unreachable!("the cancellation arm's tick never completes; the only exit is cancellation")
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
        arrived_sig: tokio::sync::Notify::new(),
        receipt: Mutex::new(None),
        tick_dropped: Mutex::new(false),
    });

    // Named `abandon`, not `cancel`, because that is what it measures. Dropping the host call future
    // does NOT cancel a started task in wasmtime 49 — it abandons it. The mode was called `cancel`
    // while the premise was believed, and the name is corrected rather than the reading relabelled.
    let abandoning = mode == "abandon";
    let inlining = mode == "inline";
    let mut linker: Linker<HostState> = Linker::new(&engine);
    linker.root().func_wrap_concurrent("tick", move |acc, (id,): (u32,)| {
        let rv = acc.with(|mut s| s.get().rv.clone());
        Box::pin(async move {
            if inlining {
                inline_tick(rv, id).await.map(|v| (v,))
            } else if abandoning {
                pending_tick(rv, id).await.map(|v| (v,))
            } else {
                rendezvous(rv, id).await.map(|v| (v,))
            }
        })
    })?;

    // The receipt channel. SYNC, because the guest calls it from a `Drop` impl, which cannot await.
    // Registered as a host import rather than stdout so the harness is the witness rather than the
    // subject: a stdout line would be the guest's claim about itself.
    //
    // **Amendment 2's question — may a cancelled task call an import? — is NOT reached by this mode**,
    // because nothing here cancels. The receipt is wired up anyway and its absence is part of the
    // reading: it is what shows the guest's cancellation path never ran.
    if abandoning {
        let rv_note = rv.clone();
        linker
            .root()
            .func_wrap("note", move |_store, (code,): (u32,)| {
                rv_note.say(format!("receipt({code})"));
                *rv_note.receipt.lock().unwrap() = Some(code);
                Ok(())
            })?;
    }

    let mut store = Store::new(&engine, HostState { rv: rv.clone() });
    let instance = linker.instantiate_async(&mut store, &component).await?;
    let run = instance.get_typed_func::<(u32,), (u32,)>(&mut store, "run")?;

    // **The outer result is CAPTURED, not propagated.** The first version used `?` here, so when the
    // rendezvous expired the host error escaped through the task machinery and `main` returned before
    // printing EVENTS — losing the diagnostic log at exactly the moment the registration's failure table
    // needs it. A witness must report its evidence on the failing path above all.
    let rv_cancel = rv.clone();
    let outer = store
        .run_concurrent(async |acc| -> Result<String> {
            if mode == "inline" {
                // TWO calls, sequentially, so the reading can tell a passthrough and a constant from a
                // real `id + 100`. One value cannot.
                let a = run.call_concurrent(acc, (1,)).await;
                let b = run.call_concurrent(acc, (2,)).await;
                Ok(format!("inline: run(1)={a:?} run(2)={b:?}"))
            } else if mode == "abandon" {
                // Start the call, then drop it once the guest has DEMONSTRABLY suspended. `select!`
                // drops the losing branch, so the arrival signal winning *is* the drop — and the
                // signal is the guest reaching `tick`, never a duration.
                //
                // This was built to be a CANCELLATION arm and is not one. Dropping the host future
                // does not cancel a started task in wasmtime 49; `TaskId::host_future_dropped`
                // cancels eagerly only when the parameters have NOT been lowered, and otherwise
                // merely marks the future dropped and defers deletion until the task's threads
                // finish. `Event::Cancelled` has exactly one producer, `subtask_cancel` — a GUEST
                // built-in, reachable only through `libcalls.rs`. So the mode measures abandonment,
                // and it is named for that.
                let outcome = {
                    let fut = run.call_concurrent(acc, (1,));
                    tokio::select! {
                        r = fut => {
                            // With a pending-forever `tick` a normal return is impossible, so this
                            // is reported as the finding rather than as a pass.
                            format!("call RESOLVED unexpectedly: {r:?}")
                        }
                        () = rv_cancel.arrived_sig.notified() => "host future dropped after guest arrival".into(),
                    }
                };
                // **Return immediately after the drop, and inspect the receipt outside.** Measured:
                // waiting for the receipt *here* observes nothing even in principle, because the
                // engine's own task machinery is driven by `run_concurrent` — the very future this
                // closure is suspended inside. Nothing can process the drop while the caller holds
                // the floor.
                Ok(format!("abandon: {outcome}"))
            } else if mode == "sequential" {
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
    if abandoning {
        // `abandoning`, not a second `mode ==` literal: the first version of this block tested
        // `mode == "cancel"` and silently stopped printing RECEIPT and TICKDROP when the mode was
        // renamed — two facts dropped from the reading with nothing failing. One predicate, one site.
        //
        // Read AFTER `run_concurrent` has returned, which is the only point the engine has had a
        // chance to process the dropped call future.
        match *rv.receipt.lock().unwrap() {
            Some(code) => println!("RECEIPT   observed, code={code}"),
            // Expected under abandonment, and part of the reading rather than a disappointment: the
            // guest's cancellation path never ran because the guest was never cancelled.
            None => println!("RECEIPT   none (expected: the task was abandoned, not cancelled)"),
        }
        // The reference fact amendment 3 added: what becomes of a host call left in flight when its
        // task is cancelled. A host function held pending forever is a resource, and whether the
        // engine tears it down or leaks it is a difference an embedder hits eventually — so it is
        // captured here rather than discovered in slice 3.
        println!("TICKDROP  {}", *rv.tick_dropped.lock().unwrap());
    }
    Ok(())
}
