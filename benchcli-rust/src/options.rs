// The command line, as benchcli-go's flags and benchcli-uwscpp's Options take it: the same
// names and defaults, -flag=value, -flag value and a bare -bool, and the same checks up front,
// so that a misspelled flag fails before the run spends the whole benchmark rather than after.
use std::collections::BTreeMap;

use crate::metadata::metadata;

// Where a run's CPU and MEM samples come from; -ps takes these names. See ps.rs, and
// config/pssource.go for the same three in the Go client.
pub const PS_MODE_AUTO: &str = "auto";
pub const PS_MODE_LOCAL: &str = "local";
pub const PS_MODE_REMOTE: &str = "remote";

// The order a report table's rows come out in; -sort takes these names. Mirrors
// report.SortResult and report.SortFramework in benchcli-go, down to which is the default.
pub const SORT_RESULT: &str = "result";
pub const SORT_FRAMEWORK: &str = "framework";

const DEFAULTS: &[(&str, &str)] = &[
    ("nodelay", "true"),
    ("m", "4294967296"),
    ("f", "nbio_std"),
    ("ip", "127.0.0.1"),
    ("c", "10000"),
    ("dc", "2000"),
    ("dt", "5s"),
    ("dr", "5"),
    ("dri", "100ms"),
    ("b", "1024"),
    ("check", "false"),
    ("pi", "1000"),
    ("ps", PS_MODE_AUTO),
    ("tpn", "true"),
    ("ec", "10000"),
    ("en", "2000000"),
    ("el", "0"),
    ("ep", "false"),
    ("epd", "5"),
    ("rate", "false"),
    ("rc", "10000"),
    ("rd", "10"),
    ("rr", "200"),
    ("rbs", "16384"),
    ("rpl", "0"),
    ("rl", "0"),
    ("rp", "false"),
    ("rpd", "5"),
    ("r", "false"),
    ("sort", SORT_RESULT),
    ("preffix", ""),
    ("project", "GO-WEBSOCKET-BENCHMARK"),
    ("suffix", ""),
    ("threads", "0"),
    ("io-timeout", "30s"),
];

const BOOLS: &[&str] = &["nodelay", "check", "tpn", "ep", "rate", "rp", "r"];

pub struct Options {
    values: BTreeMap<String, String>,
    pub help: bool,
}

pub type Result<T> = std::result::Result<T, String>;

impl Options {
    pub fn parse(args: &[String]) -> Result<Options> {
        let mut values: BTreeMap<String, String> = DEFAULTS
            .iter()
            .map(|(k, v)| (k.to_string(), v.to_string()))
            .collect();
        let mut i = 0;
        while i < args.len() {
            let arg = &args[i];
            if arg == "-h" || arg == "--help" || arg == "-help" {
                return Ok(Options { values, help: true });
            }
            if !arg.starts_with('-') {
                return Err(format!("unexpected argument: {arg}"));
            }
            let key = arg.strip_prefix("--").unwrap_or(&arg[1..]);
            let (key, value) = match key.split_once('=') {
                Some((k, v)) => (k.to_string(), Some(v.to_string())),
                None => (key.to_string(), None),
            };
            if !values.contains_key(&key) {
                return Err(format!("unknown flag -{key}"));
            }
            let value = match value {
                Some(v) => v,
                None if BOOLS.contains(&key.as_str()) => "true".to_string(),
                None => {
                    i += 1;
                    args.get(i)
                        .cloned()
                        .ok_or_else(|| format!("missing value -{key}"))?
                }
            };
            values.insert(key, value);
            i += 1;
        }
        let o = Options {
            values,
            help: false,
        };
        o.validate()?;
        Ok(o)
    }

    fn validate(&self) -> Result<()> {
        for key in BOOLS {
            self.boolean(key)?;
        }
        for key in [
            "c", "dc", "dr", "b", "pi", "ec", "en", "el", "epd", "rc", "rd", "rr", "rbs", "rpl",
            "rl", "rpd", "threads",
        ] {
            self.integer(key)?;
        }
        if self.number("m")? < 0 {
            return Err("-m must be nonnegative".into());
        }
        for key in ["dt", "dri", "io-timeout"] {
            self.duration(key)?;
        }
        if metadata()["ports"].get(self.get("f")).is_none() {
            return Err(format!("unknown framework: {}", self.get("f")));
        }
        for key in ["preffix", "suffix"] {
            if self.get(key).contains(['/', '\\']) {
                return Err("report prefix/suffix must not contain paths".into());
            }
        }
        let ip = self.get("ip");
        if ip.is_empty() || ip.contains(['\r', '\n', ' ', '/', '?', '#', '@']) {
            return Err("invalid -ip".into());
        }
        let ps = self.get("ps");
        if ps != PS_MODE_AUTO && ps != PS_MODE_LOCAL && ps != PS_MODE_REMOTE {
            return Err(format!(
                "unsupported -ps value {ps} (want {PS_MODE_AUTO}, {PS_MODE_LOCAL} or {PS_MODE_REMOTE})"
            ));
        }
        let sort = self.get("sort");
        if sort != SORT_RESULT && sort != SORT_FRAMEWORK {
            return Err(format!(
                "unsupported -sort value {sort} (want {SORT_RESULT} or {SORT_FRAMEWORK})"
            ));
        }
        Ok(())
    }

    pub fn get(&self, key: &str) -> &str {
        &self.values[key]
    }

    pub fn boolean(&self, key: &str) -> Result<bool> {
        match self.get(key) {
            "true" | "1" | "t" | "TRUE" | "True" | "T" => Ok(true),
            "false" | "0" | "f" | "FALSE" | "False" | "F" => Ok(false),
            v => Err(format!("invalid boolean -{key}={v}")),
        }
    }

    pub fn number(&self, key: &str) -> Result<i64> {
        self.get(key)
            .parse()
            .map_err(|_| format!("invalid integer -{key}"))
    }

    pub fn integer(&self, key: &str) -> Result<i32> {
        let n = self.number(key)?;
        if n < 0 || n > i32::MAX as i64 {
            return Err(format!("out of range -{key}"));
        }
        Ok(n as i32)
    }

    // Go's time.ParseDuration syntax, in nanoseconds: "0", or a sequence of decimal numbers each
    // with a unit - ns, us (µs, μs), ms, s, m, h - such as "1.5s" or "1m30s".
    pub fn duration(&self, key: &str) -> Result<i64> {
        parse_duration(self.get(key)).ok_or_else(|| format!("invalid duration -{key}"))
    }

    // The flags whose values are checked by validate, read without the Result.
    pub fn b(&self, key: &str) -> bool {
        self.boolean(key).unwrap()
    }
    pub fn i(&self, key: &str) -> i32 {
        self.integer(key).unwrap()
    }
    pub fn d(&self, key: &str) -> i64 {
        self.duration(key).unwrap()
    }

    pub fn usage(&self) {
        println!("benchcli-rust: Rust benchmark client");
        println!("Flags match benchcli-go (use -flag=value or -flag value; booleans use =false).");
        for (k, v) in &self.values {
            println!("  -{k}={v}");
        }
        println!(
            "-sort: report row order: result (default) ranks the best result first, framework keeps\n\
             \x20      the config.FrameworkList order. Both carry the same rows and numbers.\n\
             -ps: where the server's CPU and MEM samples come from: auto samples the server here when\n\
             \x20    it runs on this machine and asks it over HTTP when it does not, local always samples\n\
             \x20    here, remote always asks.\n\
             -threads: event-loop threads (0: available CPUs, capped by concurrency).\n\
             -io-timeout: maximum echo response wait; -dt: TCP + upgrade timeout.\n\
             -m: memory limit in bytes (Linux: address space; macOS: sampled RSS; 0: unlimited)."
        );
    }
}

fn parse_duration(v: &str) -> Option<i64> {
    if v == "0" {
        return Some(0);
    }
    if v.is_empty() {
        return None;
    }
    let mut rest = v;
    let mut total = 0f64;
    while !rest.is_empty() {
        let digits = rest
            .find(|c: char| !(c.is_ascii_digit() || c == '.'))
            .unwrap_or(rest.len());
        let number = &rest[..digits];
        if number.is_empty() || number == "." || number.matches('.').count() > 1 {
            return None;
        }
        let value: f64 = number.parse().ok()?;
        rest = &rest[digits..];
        let (scale, len) = [
            ("ns", 1.0),
            ("us", 1e3),
            ("µs", 1e3),
            ("μs", 1e3),
            ("ms", 1e6),
            ("s", 1e9),
            ("m", 60e9),
            ("h", 3600e9),
        ]
        .iter()
        .find(|(unit, _)| rest.starts_with(unit))
        .map(|(unit, scale)| (*scale, unit.len()))?;
        total += value * scale;
        rest = &rest[len..];
    }
    if !total.is_finite() || total >= i64::MAX as f64 / 2.0 {
        return None;
    }
    Some(total as i64)
}

#[cfg(test)]
mod tests {
    use super::parse_duration;

    #[test]
    fn durations() {
        assert_eq!(parse_duration("0"), Some(0));
        assert_eq!(parse_duration("200ms"), Some(200_000_000));
        assert_eq!(parse_duration("1m30s"), Some(90_000_000_000));
        assert_eq!(parse_duration("1.5s"), Some(1_500_000_000));
        assert_eq!(parse_duration(".5us"), Some(500));
        assert_eq!(parse_duration("oops"), None);
        assert_eq!(parse_duration("5"), None);
        assert_eq!(parse_duration(""), None);
    }
}
