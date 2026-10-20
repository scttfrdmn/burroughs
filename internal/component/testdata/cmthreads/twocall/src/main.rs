//! #1040's wasmtime reading: two calls on ONE instance of trap-then-call. The first (`run`) traps in a
//! Thread that is not its task's main thread; the reading is what the second (`ok`) does.
use wasmtime::component::{Component, Linker};
use wasmtime::{Config, Engine, Result, Store};

fn show<T: std::fmt::Debug>(r: &Result<T>) -> String {
    match r {
        Ok(v) => format!("returned {v:?}"),
        Err(e) => format!("error: {}", format!("{e:?}").replace('\n', " | ")),
    }
}

#[tokio::main]
async fn main() -> Result<()> {
    let path = std::env::args().nth(1).expect("usage: twocall <component.wasm>");
    let mut cfg = Config::new();
    cfg.async_support(true);
    cfg.wasm_component_model_async(true);
    cfg.wasm_component_model_threading(true);
    cfg.concurrency_support(true);
    let engine = Engine::new(&cfg)?;
    let component = Component::from_file(&engine, &path)?;
    let linker: Linker<()> = Linker::new(&engine);
    let mut store = Store::new(&engine, ());
    let instance = linker.instantiate_async(&mut store, &component).await?;
    let run = instance.get_typed_func::<(), (u32,)>(&mut store, "run")?;
    let ok = instance.get_typed_func::<(), (u32,)>(&mut store, "ok")?;

    let first = run.call_async(&mut store, ()).await;
    println!("call 1 run: {}", show(&first));
    if first.is_ok() {
        println!("call 1 post_return: {}", show(&run.post_return_async(&mut store).await));
    }
    let second = ok.call_async(&mut store, ()).await;
    println!("call 2 ok:  {}", show(&second));
    if second.is_ok() {
        println!("call 2 post_return: {}", show(&ok.post_return_async(&mut store).await));
    }
    // A control on a FRESH instance of the same component: `ok` alone must return 7.
    let mut s2 = Store::new(&engine, ());
    let i2 = linker.instantiate_async(&mut s2, &component).await?;
    let ok2 = i2.get_typed_func::<(), (u32,)>(&mut s2, "ok")?;
    println!("control ok on a fresh instance: {}", show(&ok2.call_async(&mut s2, ()).await));
    Ok(())
}
