// The reports: a JSON file per framework and benchmark, the block each benchmark prints as it
// finishes, and with -r the markdown tables and the Summary in front of them. All of it reads
// its fields off the schemas generated from benchcli-go/report, and writes them the way
// benchcli-go and benchcli-uwscpp do, down to the padding: the three clients' reports are read
// by one another and diffed against one another.
use std::path::Path;
use std::thread::JoinHandle;
use std::time::Duration;

use serde_json::{Map, Value, json};

use crate::http::{control_url, framework_task_pool, http};
use crate::metadata::{frameworks, lang, schema, summary_order};
use crate::options::{Options, SORT_RESULT};

pub type Report = Map<String, Value>;

pub fn empty_report(kind: &str, o: &Options) -> Report {
    let mut r = Report::new();
    for field in schema(kind) {
        let key = field["key"].as_str().unwrap().to_string();
        r.insert(
            key,
            if field["string"].as_bool().unwrap() {
                json!("")
            } else {
                json!(0)
            },
        );
    }
    r.insert("Framework".into(), json!(o.get("f")));
    r.insert("Lang".into(), json!(lang(o.get("f"))));
    r.insert("BenchClient".into(), json!("benchcli-rust"));
    r.insert("TaskPool".into(), json!(framework_task_pool(o)));
    r
}

pub fn filename(o: &Options, base: &str, ext: &str) -> String {
    format!(
        "output/report/{}{base}{}{ext}",
        o.get("preffix"),
        o.get("suffix")
    )
}

pub fn write_file(path: &str, data: &[u8]) -> Result<(), String> {
    if let Some(dir) = Path::new(path).parent() {
        std::fs::create_dir_all(dir)
            .map_err(|e| format!("cannot create {}: {e}", dir.display()))?;
    }
    std::fs::write(path, data).map_err(|e| format!("cannot write {path}: {e}"))
}

fn fixed(v: f64) -> String {
    format!("{v:.2}")
}

// Whether a field is printed and tabled at all: a hidden one (md:"-") is only in the JSON, and
// a latency percentile only when -tpn is on.
fn shown_field(field: &Value, tpn: bool) -> bool {
    !field["hidden"].as_bool().unwrap() && (!field["optional"].as_bool().unwrap() || tpn)
}

// Whether a field is a column of its table: a run parameter, tagged summary:"<name>", is shown
// once in the Summary table instead.
fn table_column(field: &Value, tpn: bool) -> bool {
    shown_field(field, tpn) && field["summary"].as_str().unwrap().is_empty()
}

// report.clientName: the Client row reads language-framework, as report.ClientNames maps
// the three clients; the JSON keeps the full name.
fn client_name(s: &str) -> String {
    match s {
        "benchcli-go" => "go-nbio".into(),
        "benchcli-rust" => "rust-tokio_tungstenite".into(),
        "benchcli-uwscpp" => "cpp-uwebsockets".into(),
        _ => s.strip_prefix("benchcli-").unwrap_or(s).to_string(),
    }
}

fn format_field(r: &Report, field: &Value) -> String {
    let key = field["key"].as_str().unwrap();
    let fmt = field["fmt"].as_str().unwrap();
    // Missing - a report written before the field existed - reads as Go's zero value.
    let Some(value) = r.get(key).filter(|v| !v.is_null()) else {
        return if field["string"].as_bool().unwrap() {
            String::new()
        } else {
            "0".into()
        };
    };
    if let Some(s) = value.as_str() {
        return if fmt == "client" {
            client_name(s)
        } else {
            s.to_string()
        };
    }
    let n = value.as_f64().unwrap_or(0.0);
    match fmt {
        "duration" if n >= 1e9 => format!("{}s", fixed(n / 1e9)),
        "duration" if n >= 1e6 => format!("{}ms", fixed(n / 1e6)),
        "duration" if n >= 1e3 => format!("{}us", fixed(n / 1e3)),
        "duration" => format!("{value}ns"),
        "mem" if n >= 1073741824.0 => format!("{}G", fixed(n / 1073741824.0)),
        "mem" if n >= 1048576.0 => format!("{}M", fixed(n / 1048576.0)),
        "mem" => format!("{}K", fixed(n / 1024.0)),
        _ if field["floating"].as_bool().unwrap() => fixed(n),
        _ => value.to_string(),
    }
}

// Writes a report's JSON file, and prints it the way benchcli-go's ObjString does.
pub fn save_report(o: &Options, kind: &str, r: &Report) -> Result<(), String> {
    let mut data = serde_json::to_vec(r).unwrap();
    data.push(b'\n');
    write_file(
        &filename(o, &format!("{}-{kind}", o.get("f")), ".json"),
        &data,
    )?;
    let tpn = o.b("tpn");
    let lines: Vec<(String, String)> = schema(kind)
        .iter()
        .filter(|f| shown_field(f, tpn))
        .map(|f| (f["title"].as_str().unwrap().to_string(), format_field(r, f)))
        .collect();
    let width = lines
        .iter()
        .map(|(t, _)| t.len())
        .max()
        .unwrap_or(0)
        .max("BenchType".len());
    let mut out = format!("{:width$}: {kind}\n", "BenchType");
    for (title, value) in lines {
        out.push_str(&format!("{title:width$}: {value}\n"));
    }
    print!("{out}");
    Ok(())
}

// A cell's width in characters rather than bytes: the [↓1] and [↓2] on the rank columns'
// titles are three bytes each for one character.
fn text_width(s: &str) -> usize {
    s.chars().count()
}

// github.com/lesismal/perf's padding: the first column pushed right no further than its title
// and padded on the right, every other cell centred.
fn pad_cell(s: &str, max_len: usize, first: bool, title_left_padding: usize) -> String {
    let width = text_width(s);
    if width >= max_len {
        return s.to_string();
    }
    let mut padding = max_len - width;
    if first {
        let mut out = s.to_string();
        let mut i = 0;
        while i < padding / 2 && i < title_left_padding {
            out.insert(0, ' ');
            padding -= 1;
            i += 1;
        }
        out.push_str(&" ".repeat(padding));
        out
    } else {
        let half = padding / 2;
        format!("{}{s}{}", " ".repeat(half), " ".repeat(padding - half))
    }
}

// Left-aligned: one space before the cell, the rest after.
fn pad_left(s: &str, max_len: usize, _: bool, _: usize) -> String {
    let width = text_width(s);
    if width >= max_len {
        return s.to_string();
    }
    format!(" {s}{}", " ".repeat(max_len - width - 1))
}

// report.markdownTableAligned: with left set, every cell is padded on the right, so the table
// is left-aligned in the console; the separators stay "---".
fn markdown_table(title: &[String], rows: Vec<Vec<String>>, left: bool) -> String {
    let pad = if left { pad_left } else { pad_cell };
    let mut columns = title.len();
    let mut max_len: Vec<usize> = title.iter().map(|t| text_width(t)).collect();
    let mut all = vec![vec!["---".to_string(); columns]];
    all.extend(rows);
    for row in &all {
        for (j, cell) in row.iter().enumerate() {
            if max_len.len() < j + 1 {
                max_len.push(text_width(cell));
            } else {
                max_len[j] = max_len[j].max(text_width(cell));
            }
        }
        columns = columns.max(row.len());
    }
    let mut title = title.to_vec();
    title.resize(columns, String::new());
    for row in &mut all {
        row.resize(columns, String::new());
    }
    for m in &mut max_len {
        *m += 2;
    }
    let mut out = String::from("|");
    let mut title_left_padding = 0;
    for (i, t) in title.iter().enumerate() {
        let aligned = pad(t, max_len[i], false, 0);
        if i == 0 {
            // The last character that is not a space, as perf has it.
            for (k, ch) in aligned.chars().enumerate() {
                if ch != ' ' {
                    title_left_padding = k;
                }
            }
        }
        out.push_str(&aligned);
        out.push('|');
    }
    out.push('\n');
    for row in &all {
        out.push('|');
        for (j, cell) in row.iter().enumerate() {
            out.push_str(&pad(cell, max_len[j], j == 0, title_left_padding));
            out.push('|');
        }
        out.push('\n');
    }
    out
}

// What -sort=result ranks a report by, most significant first: the fields tagged rank:"1",
// rank:"2" and so on in benchcli-go/report.
fn rank_fields(kind: &str) -> Vec<&'static Value> {
    let mut fields: Vec<&Value> = schema(kind)
        .iter()
        .filter(|f| f["rank"].as_i64().unwrap() > 0)
        .collect();
    fields.sort_by_key(|f| f["rank"].as_i64().unwrap());
    fields
}

fn rank_value(r: &Report, field: &Value) -> f64 {
    r.get(field["key"].as_str().unwrap())
        .and_then(Value::as_f64)
        .unwrap_or(0.0)
}

// report.Percent: floored, so only the best shows 100%. The best is 100% by comparison, since
// value*100/value can come out a hair under 100 for a float EER, and the rest get the same
// nudge up against the division's rounding, held under 100.
fn percent(value: f64, best: f64) -> String {
    if best <= 0.0 || value <= 0.0 {
        return "0%".into();
    }
    if value >= best {
        return "100%".into();
    }
    let p = (value * 100.0 / best + 1e-9).floor() as i64;
    format!("{}%", p.min(99))
}

// Each cell of column col gets its row's share of the best after it, the value and the
// percentage each right-aligned.
fn with_percent(rows: &mut [Vec<String>], col: usize, values: &[f64]) {
    let best = values.iter().cloned().fold(0.0, f64::max);
    let percents: Vec<String> = values.iter().map(|v| percent(*v, best)).collect();
    let value_len = rows.iter().map(|r| r[col].len()).max().unwrap_or(0);
    let percent_len = percents.iter().map(String::len).max().unwrap_or(0);
    for (row, p) in rows.iter_mut().zip(&percents) {
        row[col] = format!("{:>value_len$} {p:>percent_len$}", row[col]);
    }
}

// A rate run's TPS is the packets the clients read back per second of its duration, floored. A
// report written before it had one gets it here, so an earlier run still ranks by it.
pub fn fill_rate_tps(r: &mut Report) {
    let num = |r: &Report, k: &str| r.get(k).and_then(Value::as_f64).unwrap_or(0.0);
    if num(r, "TPS") != 0.0 || num(r, "RecvTimes") <= 0.0 {
        return;
    }
    let duration = num(r, "Duration");
    let tps = if duration > 0.0 {
        (num(r, "RecvTimes") / (duration / 1e9)).floor() as i64
    } else {
        0
    };
    r.insert("TPS".into(), json!(tps));
}

fn read_reports(o: &Options, kind: &str) -> Result<Vec<Report>, String> {
    let mut rows = Vec::new();
    for f in frameworks() {
        let path = filename(o, &format!("{f}-{kind}"), ".json");
        let Ok(data) = std::fs::read(&path) else {
            continue;
        };
        let mut row: Report = serde_json::from_slice(&data).map_err(|e| format!("{path}: {e}"))?;
        // A report written before the Lang column existed gets it from the config here.
        if row
            .get("Lang")
            .and_then(Value::as_str)
            .is_none_or(str::is_empty)
        {
            row.insert("Lang".into(), json!(lang(f)));
        }
        if kind == "BenchRate" {
            fill_rate_tps(&mut row);
        }
        rows.push(row);
    }
    Ok(rows)
}

// report.poolSummary: the pools that ran, once each, without the frameworks, the "-" of those
// that installed none, or a value's "(...)" suffix.
fn pool_summary(values: &[(String, Vec<String>)]) -> String {
    let mut pools: Vec<String> = Vec::new();
    for (value, _) in values {
        let pool = match value.find('(') {
            Some(i) if i > 0 => &value[..i],
            _ => value.as_str(),
        };
        if pool == "-" || pool.is_empty() || pools.iter().any(|p| p == pool) {
            continue;
        }
        pools.push(pool.to_string());
    }
    if pools.is_empty() {
        "-".into()
    } else {
        pools.join(", ")
    }
}

fn summary_description(name: &str) -> String {
    summary_order()
        .iter()
        .find(|p| p["name"] == name)
        .map(|p| p["description"].as_str().unwrap().to_string())
        .unwrap_or_default()
}

// report.Summary: the run's parameters, the summary-tagged fields of every row of every report,
// left-aligned. One value where the rows agree; otherwise each value followed by the frameworks
// that had it, "20000 (fib, fnet); 19998 (fasthttp)" - except Pool - and a Description column,
// all headed by -project's Project row unless it is empty.
fn summary_table(o: &Options) -> Result<String, String> {
    let mut values: std::collections::HashMap<String, Vec<(String, Vec<String>)>> =
        Default::default();
    let mut names: Vec<String> = Vec::new();
    for kind in ["Connections", "BenchEcho", "BenchRate"] {
        for r in read_reports(o, kind)? {
            let framework = r
                .get("Framework")
                .and_then(Value::as_str)
                .unwrap_or("")
                .to_string();
            for field in schema(kind) {
                let name = field["summary"].as_str().unwrap();
                if name.is_empty() {
                    continue;
                }
                if !values.contains_key(name) {
                    names.push(name.to_string());
                }
                let list = values.entry(name.to_string()).or_default();
                let value = format_field(&r, field);
                match list.iter_mut().find(|(v, _)| *v == value) {
                    None => list.push((value, vec![framework.clone()])),
                    Some((_, fs)) => {
                        if !fs.contains(&framework) {
                            fs.push(framework.clone());
                        }
                    }
                }
            }
        }
    }
    if names.is_empty() {
        return Ok(String::new());
    }
    let mut ordered: Vec<String> = summary_order()
        .iter()
        .map(|p| p["name"].as_str().unwrap().to_string())
        .filter(|n| values.contains_key(n))
        .collect();
    for name in names {
        if !ordered.contains(&name) {
            ordered.push(name);
        }
    }
    let project = o.get("project");
    let project_row = (!project.is_empty()).then(|| {
        vec![
            "Project".to_string(),
            project.to_string(),
            summary_description("Project"),
        ]
    });
    // A parameter no report carries has no row, as report.Summary has it.
    ordered.retain(|name| !(values[name].len() == 1 && values[name][0].0.is_empty()));
    let rows = project_row
        .into_iter()
        .chain(ordered.iter().map(|name| {
            let list = &values[name];
            let text = if name == "Pool" {
                pool_summary(list)
            } else if list.len() == 1 {
                list[0].0.clone()
            } else {
                list.iter()
                    .map(|(v, fs)| format!("{v} ({})", fs.join(", ")))
                    .collect::<Vec<_>>()
                    .join("; ")
            };
            vec![name.clone(), text, summary_description(name)]
        }))
        .collect();
    let title = ["Parameter", "Value", "Description"].map(String::from);
    Ok(markdown_table(&title, rows, true))
}

fn console_section(name: &str, table: &str) -> String {
    let table = if table.is_empty() {
        "(no results)\n"
    } else {
        table
    };
    format!("{}\n[{name}]\n\n{table}\n", "-".repeat(100))
}

pub fn generate_reports(o: &Options) -> Result<(), String> {
    let (preffix, suffix) = (o.get("preffix"), o.get("suffix"));
    let summary = summary_table(o)?;
    write_file(&filename(o, "Summary", ".md"), summary.as_bytes())?;
    print!(
        "{}",
        console_section(&format!("{preffix}Summary{suffix}"), &summary)
    );
    let tpn = o.b("tpn");
    for kind in ["Connections", "BenchEcho", "BenchRate"] {
        let mut rows = read_reports(o, kind)?;
        // Read in config.FrameworkList order, so -sort=framework is already what they are in.
        // The sort is stable, which leaves a tie in framework order.
        let ranks = rank_fields(kind);
        if o.get("sort") == SORT_RESULT {
            rows.sort_by(|a, b| {
                for field in &ranks {
                    let (x, y) = (rank_value(a, field), rank_value(b, field));
                    if x != y {
                        return y.partial_cmp(&x).unwrap_or(std::cmp::Ordering::Equal);
                    }
                }
                std::cmp::Ordering::Equal
            });
        }
        let mut md = String::new();
        if !rows.is_empty() {
            let fields: Vec<&Value> = schema(kind)
                .iter()
                .filter(|f| table_column(f, tpn))
                .collect();
            let titles: Vec<String> = fields
                .iter()
                .map(|f| {
                    let title = f["title"].as_str().unwrap();
                    match f["rank"].as_i64().unwrap() {
                        0 => title.to_string(),
                        rank => format!("{title} [↓{rank}]"),
                    }
                })
                .collect();
            let mut table: Vec<Vec<String>> = rows
                .iter()
                .map(|r| fields.iter().map(|f| format_field(r, f)).collect())
                .collect();
            for rank in &ranks {
                if let Some(col) = fields.iter().position(|f| f["key"] == rank["key"]) {
                    let values: Vec<f64> = rows.iter().map(|r| rank_value(r, rank)).collect();
                    with_percent(&mut table, col, &values);
                }
            }
            md = markdown_table(&titles, table, false);
        }
        write_file(&filename(o, kind, ".md"), md.as_bytes())?;
        print!(
            "{}",
            console_section(&format!("{preffix}{kind}{suffix}"), &md)
        );
    }
    println!("{}", "-".repeat(100));
    Ok(())
}

// Fetches the server's CPU and heap profiles two seconds into a benchmark, on a thread of its
// own; the caller joins it before writing the report.
pub fn profile(o: &Options, kind: &str, enabled: bool, seconds: i32) -> Option<JoinHandle<()>> {
    if !enabled || !crate::metadata::serves_pprof(o.get("f")) {
        return None;
    }
    let base = control_url(o);
    let cpu_path = filename(o, &format!("{}-{kind}", o.get("f")), ".pprof.cpu");
    let mem_path = filename(o, &format!("{}-{kind}", o.get("f")), ".pprof.mem");
    let kind = kind.to_string();
    Some(std::thread::spawn(move || {
        std::thread::sleep(Duration::from_secs(2));
        let result = (|| -> Result<(), String> {
            let cpu = http(
                &format!("{base}/debug/pprof/profile?seconds={seconds}"),
                None,
                Duration::from_secs(seconds as u64 + 15),
            )
            .map_err(|e| e.to_string())?;
            let mem = http(
                &format!("{base}/debug/pprof/heap"),
                None,
                Duration::from_secs(15),
            )
            .map_err(|e| e.to_string())?;
            write_file(&cpu_path, &cpu)?;
            write_file(&mem_path, &mem)
        })();
        if let Err(e) = result {
            eprintln!("{kind} pprof: {e}");
        }
    }))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn percents() {
        assert_eq!(percent(40.0, 40.0), "100%");
        assert_eq!(percent(10.0, 40.0), "25%");
        assert_eq!(percent(697.86464300696265, 1395.7292860139253), "50%");
        assert_eq!(percent(0.0, 1.0), "0%");
        assert_eq!(percent(99.999, 100.0), "99%");
    }

    // What benchcli-go's markdownTable and markdownTableAligned write for the same cells.
    #[test]
    fn tables_match_benchcli_go() {
        let title = ["Framework", "TPS"].map(String::from);
        let rows = vec![
            vec!["gorilla".to_string(), "10".to_string()],
            vec!["nbio_std".to_string(), "200".to_string()],
        ];
        assert_eq!(
            markdown_table(&title, rows, false),
            "| Framework | TPS |\n|   ---     | --- |\n| gorilla   | 10  |\n| nbio_std  | 200 |\n"
        );
        let title = ["Parameter", "Value"].map(String::from);
        let rows = vec![vec!["Client".to_string(), "rust".to_string()]];
        assert_eq!(
            markdown_table(&title, rows, true),
            "| Parameter | Value |\n| ---       | ---   |\n| Client    | rust  |\n"
        );
    }
}
