wit_bindgen::generate!({
    path: "wit",
    world: "probe",
});

struct Component;

impl exports::test::probe::ops::Guest for Component {
    async fn compute(x: u32) -> u32 { x + 1 }
}

export!(Component);
