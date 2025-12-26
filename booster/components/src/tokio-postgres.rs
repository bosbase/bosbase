
#[link(wasm_import_module = "bosbase_postgres")]
unsafe extern "C" {
    fn pg_exec(sql_ptr: i32, sql_len: i32) -> i32;
    fn pg_query(sql_ptr: i32, sql_len: i32, out_ptr: i32, out_len: i32) -> i32;
}

fn exec(sql: &str) -> i32 {
    unsafe { pg_exec(sql.as_ptr() as i32, sql.len() as i32) }
}

fn query(sql: &str) -> Result<String, i32> {
    let mut out = vec![0u8; 64 * 1024];
    let n = unsafe {
        pg_query(
            sql.as_ptr() as i32,
            sql.len() as i32,
            out.as_mut_ptr() as i32,
            out.len() as i32,
        )
    };

    if n >= 0 {
        Ok(String::from_utf8_lossy(&out[..n as usize]).to_string())
    } else {
        Err(n)
    }
}

fn main() {
    let name = std::env::var("NAME").unwrap();
    println!("Hi Bosabase ! My name is {name}");

    let rc = exec(
        "CREATE TABLE IF NOT EXISTS booster_demo (\n            id SERIAL PRIMARY KEY,\n            name TEXT NOT NULL\n        )",
    );
    println!("pg_exec create rc={rc}");

    let safe_name = name.replace('\'', "''");
    let rc = exec(&format!(
        "INSERT INTO booster_demo (name) VALUES ('{}')",
        safe_name
    ));
    println!("pg_exec insert rc={rc}");

    let updated_name = format!("{safe_name}-updated");
    let rc = exec(&format!(
        "UPDATE booster_demo SET name = '{}' WHERE name = '{}'",
        updated_name, safe_name
    ));
    println!("pg_exec update rc={rc}");

    match query("SELECT id, name FROM booster_demo ORDER BY id DESC LIMIT 5") {
        Ok(json) => println!("pg_query ok json={json}"),
        Err(rc) => println!("pg_query err rc={rc}"),
    }

    match query(&format!(
        "SELECT id, name FROM booster_demo WHERE name = '{}' ORDER BY id DESC LIMIT 5",
        updated_name
    )) {
        Ok(json) => println!("pg_query updated ok json={json}"),
        Err(rc) => println!("pg_query updated err rc={rc}"),
    }

    let rc = exec(&format!("DELETE FROM booster_demo WHERE name = '{}'", updated_name));
    println!("pg_exec delete rc={rc}");

    println!("Goodbye from {name}");
}

