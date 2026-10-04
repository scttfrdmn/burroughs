wit_bindgen::generate!({ path: "wit", world: "parent" });

use futures::{future, select_biased, FutureExt};

struct C;

impl Guest for C {
    /// Start the child, let it reach its suspension point, then **drop** the in-flight call — which is
    /// how a real Rust guest cancels a subtask.
    ///
    /// # Why a race and not a manual poll
    ///
    /// The case this witness is for is cancellation *after the child has started*. Dropping the future
    /// without driving it at all cancels **before the subtask exists** — `start_cancelled`, which never
    /// issues `subtask.cancel` and would measure nothing.
    ///
    /// A first version polled the future by hand with `Waker::noop()` from a **sync** export. It started
    /// the child (the harness saw `enter(1) -> suspend(1)`) and then **panicked** inside
    /// `poll_complete_with_code` at drop time. The reason is the export's shape, not the poll: an async
    /// import's waker and task machinery is set up by `start_task`, which only an **async** export
    /// establishes, so driving one from a sync lift leaves the operation's bookkeeping in a state its own
    /// cancel path calls `unreachable!()` on. Hence `export go: async func()`.
    ///
    /// `select_biased!` with the child **first** is what reaches the state under test: the child is polled
    /// once — lowering its arguments, starting the subtask, and returning `Pending` because it is awaiting
    /// its own `tick` — and then the already-ready arm wins and the child's future is dropped. Ordering
    /// matters: with the ready arm first the child is never polled, never starts, and the reading would be
    /// of `start_cancelled` instead.
    ///
    /// # What cancels, and what this parent cannot see
    ///
    /// Dropping the future drops the `WaitableOperation` inside it, whose `Drop` calls `cancel()` →
    /// `subtask.cancel`. **That is issuing a cancellation, which this parent does. Observing its status
    /// is what it cannot do**: `Drop` discards `cancel()`'s return value (`waitable.rs:456`), the numeric
    /// status is private, and `STATUS_STARTED_CANCELLED` and `STATUS_RETURNED_CANCELLED` collapse to one
    /// Rust value (`subtask.rs:176-203`). The WAT parent beside this one reports the status; this one is
    /// what shows a real canceller produces the same observable effects.
    async fn go() {
        // `pin_mut!` because `select_biased!` requires `Unpin` futures and the generated `run()`'s
        // `impl Future` is not — `Pin<&mut F>` is. This is the mechanical cost of racing a generated
        // async import, not a second design choice.
        let child = run(1).fuse();
        let ready = future::ready(()).fuse();
        futures::pin_mut!(child, ready);
        select_biased! {
            // Polled FIRST, so the subtask starts and parks.
            _ = child => {}
            // Wins, so `child` is dropped in flight — the cancellation.
            _ = ready => {}
        }
    }
}

export!(C);
