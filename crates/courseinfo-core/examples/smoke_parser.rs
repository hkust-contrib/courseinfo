//! Native boundary for the read-only live-source smoke test.

use std::env;
use std::fmt::Display;
use std::io::{self, Read, Write};
use std::time::{SystemTime, UNIX_EPOCH};

use courseinfo_core::{parse_department, semester_for_timestamp, serialize_department};
use serde::Serialize;

const MAX_INPUT_BYTES: u64 = 4 * 1024 * 1024;
const USAGE: &str = "usage: smoke_parser semester | smoke_parser parse SEMESTER DEPARTMENT";

type CliResult<T> = Result<T, (i32, String)>;

fn runner_error(error: impl Display) -> (i32, String) {
    (2, error.to_string())
}

fn parse_error(error: impl Display) -> (i32, String) {
    (1, error.to_string())
}

#[derive(Serialize)]
struct Summary<'a> {
    departments: &'a [String],
    courses: usize,
    snapshot_bytes: usize,
}

fn run() -> CliResult<()> {
    let mut args = env::args().skip(1);
    match args.next().as_deref() {
        Some("semester") if args.next().is_none() => {
            let elapsed = SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .map_err(runner_error)?;
            let timestamp = i64::try_from(elapsed.as_millis()).map_err(runner_error)?;
            let semester = semester_for_timestamp(timestamp).map_err(runner_error)?;
            writeln!(io::stdout().lock(), "{semester}").map_err(runner_error)
        }
        Some("parse") => {
            let semester = args.next().ok_or_else(|| runner_error(USAGE))?;
            let department = args.next().ok_or_else(|| runner_error(USAGE))?;
            if args.next().is_some() {
                return Err(runner_error(USAGE));
            }
            let mut bytes = Vec::new();
            io::stdin()
                .lock()
                .take(MAX_INPUT_BYTES + 1)
                .read_to_end(&mut bytes)
                .map_err(runner_error)?;
            if bytes.len() as u64 > MAX_INPUT_BYTES {
                return Err(parse_error("HTML input exceeds the 4 MiB limit"));
            }
            let html = String::from_utf8(bytes).map_err(parse_error)?;
            let page = parse_department(&html, &semester, &department).map_err(parse_error)?;
            let snapshot = serialize_department(&page.courses).map_err(parse_error)?;
            let summary = Summary {
                departments: &page.departments,
                courses: page.courses.len(),
                snapshot_bytes: snapshot.len(),
            };
            let mut stdout = io::stdout().lock();
            serde_json::to_writer(&mut stdout, &summary).map_err(runner_error)?;
            writeln!(stdout).map_err(runner_error)
        }
        _ => Err(runner_error(USAGE)),
    }
}

fn main() {
    if let Err((status, message)) = run() {
        eprintln!("{message}");
        std::process::exit(status);
    }
}
