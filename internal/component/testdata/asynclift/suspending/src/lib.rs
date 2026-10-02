wit_bindgen::generate!({ path: "wit", world: "job" });

struct C;

impl Guest for C {
    // Awaits a host-provided async import, so the task SUSPENDS. Without a suspension point two
    // concurrent calls could never be observed overlapping, whatever the host does.
    async fn run(id: u32) -> u32 { tick(id).await }
}

export!(C);
