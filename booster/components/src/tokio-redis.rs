#[link(wasm_import_module = "bosbase_redis")]
unsafe extern "C" {
    fn redis_get(kptr: i32, klen: i32, out_ptr: i32, out_len: i32) -> i32;
    fn redis_set(kptr: i32, klen: i32, vptr: i32, vlen: i32) -> i32;
    fn redis_set_ex(kptr: i32, klen: i32, vptr: i32, vlen: i32, ttl_s: i64) -> i32;
    fn redis_exists(kptr: i32, klen: i32) -> i32;
    fn redis_del(kptr: i32, klen: i32) -> i32;
}

fn main() {
    let name = std::env::var("NAME").unwrap();
    println!("Hi Bosabase ! My name is {name}");

    let key = format!("booster:demo:{name}");
    let value = format!("hello-from-{name}");

    let set_rc = unsafe {
        redis_set(
            key.as_ptr() as i32,
            key.len() as i32,
            value.as_ptr() as i32,
            value.len() as i32,
        )
    };
    println!("redis_set rc={set_rc}");

    let set_ex_rc = unsafe {
        redis_set_ex(
            key.as_ptr() as i32,
            key.len() as i32,
            value.as_ptr() as i32,
            value.len() as i32,
            60,
        )
    };
    println!("redis_set_ex ttl=60 rc={set_ex_rc}");

    let exists_rc = unsafe { redis_exists(key.as_ptr() as i32, key.len() as i32) };
    println!("redis_exists rc={exists_rc}");

    let mut out = vec![0u8; 256];
    let n = unsafe {
        redis_get(
            key.as_ptr() as i32,
            key.len() as i32,
            out.as_mut_ptr() as i32,
            out.len() as i32,
        )
    };
    if n >= 0 {
        let got = String::from_utf8_lossy(&out[..n as usize]);
        println!("redis_get ok len={n} val={got}");
    } else {
        println!("redis_get err rc={n}");
    }

    let del_rc = unsafe { redis_del(key.as_ptr() as i32, key.len() as i32) };
    println!("redis_del rc={del_rc}");

    println!("Goodbye from {name}");
}
