wit_bindgen::generate!({ path: "wit", world: "job" });

struct C;

/// A receipt guard **local to the async function body**, which is the point of this guest.
///
/// # Why a host import and not stdout
///
/// The receipt has to show the cancellation *reached the guest*. A stdout line is the guest's claim
/// about itself; a host import is a call the harness owns and records, so the harness is the witness
/// rather than the subject.
///
/// # Why it must be disarmed on success
///
/// A guard dropped on every path fires on **normal completion too**, and then its firing says nothing
/// about cancellation — the channel would be present in both arms and discriminate neither. So the
/// guard is armed by construction and `disarm`ed once the await returns, which is exactly the shape
/// `wit_bindgen`'s own `TaskCancelOnDrop` uses (it has a `forget`).
///
/// # This whole mechanism is a hypothesis until run
///
/// Dropping the call future is expected to drop the body and therefore its locals. That is plausible
/// because `TaskCancelOnDrop` already depends on the future being dropped on cancellation — but "the
/// generated guard runs" and "a *user* local in the async body is dropped" are different claims, and
/// only the second is what this needs. A second unknown rides along: whether a cancelled task may
/// still call an import at all. Both are answered by running, and if either fails that is reported
/// rather than worked around by moving the receipt somewhere that happens to fire.
struct Receipt;

impl Receipt {
    fn disarm(self) {
        core::mem::forget(self);
    }
}

impl Drop for Receipt {
    fn drop(&mut self) {
        note(RECEIPT_CANCELLED);
    }
}

/// The code the guest reports on cancellation. A distinct non-zero value so a receipt cannot be
/// confused with a zero-initialised read on the host side.
const RECEIPT_CANCELLED: u32 = 1;

impl Guest for C {
    /// Awaits a host-provided async import, so the task SUSPENDS — without a suspension point there
    /// is nothing for the host to cancel. Identical to the `suspending` guest apart from the guard,
    /// deliberately: both arms of the cancellation witness are cut from THIS guest, so the guest
    /// itself must not be a variable between them.
    async fn run(id: u32) -> u32 {
        let receipt = Receipt;
        let v = tick(id).await;
        receipt.disarm();
        v
    }
}

export!(C);
