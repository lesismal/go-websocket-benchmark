// benchcli-rust: the benchmark client in Rust, and the one script/config.sh runs by default.
//
// It takes benchcli-go's flags, loads a server the way benchcli-uwscpp does - a fixed set of
// event-loop threads, each owning its share of the connections, here a single-threaded Tokio
// runtime apiece with a tokio-tungstenite stream per connection - and writes the same JSON
// reports and markdown tables as both, from the same schemas: build.rs generates them from
// config/config.go and benchcli-go/report. See README.md.
mod engine;
mod http;
mod metadata;
mod options;
mod ps;
mod report;
mod stream;
mod upgrade;

use std::sync::atomic::{AtomicBool, AtomicI32, AtomicI64, Ordering};
use std::sync::{Arc, Mutex};
use std::thread::JoinHandle;
use std::time::Duration;

use serde_json::json;

use engine::{DIAL, ECHO, RATE, STOP, Shared, Stats, WARMUP, Worker, WorkerCtl, now_ns};
use options::Options;
use report::{Report, empty_report, fill_rate_tps, generate_reports, profile, save_report};

static INTERRUPTED: AtomicBool = AtomicBool::new(false);

extern "C" fn on_signal(_: libc::c_int) {
    INTERRUPTED.store(true, Ordering::Relaxed);
}

// The CPUs this process may run on: the affinity mask script/env.sh pins the client to, not the
// whole host.
fn available_cpus() -> i32 {
    std::thread::available_parallelism()
        .map(|n| n.get() as i32)
        .unwrap_or(1)
}

struct Runner {
    shared: Arc<Shared>,
    ctls: Vec<Arc<WorkerCtl>>,
    threads: Vec<JoinHandle<()>>,
    memory_budget: i64,
}

impl Runner {
    fn new(o: &Options) -> Result<Runner, String> {
        let shared = Arc::new(Shared::new(o)?);
        let n = match o.i("c") {
            0 => 1000,
            n => n,
        };
        let dc = match o.i("dc") {
            0 => available_cpus() * 1000,
            dc => dc,
        }
        .min(n);
        let ec = match o.i("ec") {
            0 => available_cpus() * 1000,
            ec => ec,
        };
        let rc = match o.i("rc") {
            0 => 50000,
            rc => rc,
        };
        let threads = match o.i("threads") {
            0 => available_cpus(),
            t => t,
        }
        .min(n)
        .min(dc)
        .min(ec)
        .min(if o.b("rate") { rc } else { n });
        let mut runner = Runner {
            shared,
            ctls: Vec::new(),
            threads: Vec::new(),
            memory_budget: o.number("m")?,
        };
        for i in 0..threads {
            let share = |total: i32| total / threads + (i < total % threads) as i32;
            let ctl = Arc::new(WorkerCtl {
                done: AtomicI32::new(0),
                live: AtomicI32::new(0),
                dial_concurrency: share(dc),
                echo_concurrency: AtomicI32::new(0),
                rate_concurrency: AtomicI32::new(0),
                target: AtomicI64::new(0),
                dial_stats: Mutex::new(Stats::default()),
                echo_stats: Mutex::new(Stats::default()),
                rate_stats: Mutex::new(Stats::default()),
                error: Mutex::new(String::new()),
            });
            let worker = Worker::new(
                o,
                runner.shared.clone(),
                ctl.clone(),
                i as usize,
                threads as usize,
                share(n) as usize,
            );
            runner.ctls.push(ctl);
            runner.threads.push(
                std::thread::Builder::new()
                    .name(format!("worker-{i}"))
                    .spawn(move || worker.run())
                    .map_err(|e| format!("cannot start a worker thread: {e}"))?,
            );
        }
        println!("Client: benchcli-rust, event-loop threads: {threads}");
        Ok(runner)
    }

    fn live(&self) -> i32 {
        self.ctls
            .iter()
            .map(|c| c.live.load(Ordering::Acquire))
            .sum()
    }

    fn wait(&self, stage: i32) -> Result<(), String> {
        let mut log_at = now_ns() + 1_000_000_000;
        let mut memory_at = now_ns();
        loop {
            if cfg!(target_os = "macos") && self.memory_budget != 0 && now_ns() >= memory_at {
                // SAFETY: usage is a valid out-pointer.
                let mut usage: libc::rusage = unsafe { std::mem::zeroed() };
                if unsafe { libc::getrusage(libc::RUSAGE_SELF, &mut usage) } == 0
                    && usage.ru_maxrss as i64 > self.memory_budget
                {
                    return Err("client exceeded -m RSS limit".into());
                }
                memory_at = now_ns() + 100_000_000;
            }
            if INTERRUPTED.load(Ordering::Relaxed) {
                return Err("interrupted".into());
            }
            if self.shared.fatal.load(Ordering::Acquire) {
                let errors: Vec<String> = self
                    .ctls
                    .iter()
                    .map(|c| c.error.lock().unwrap().clone())
                    .filter(|e| !e.is_empty())
                    .collect();
                return Err(format!("worker failed: {}", errors.join("; ")));
            }
            if self
                .ctls
                .iter()
                .all(|c| c.done.load(Ordering::Acquire) == stage)
            {
                return Ok(());
            }
            if stage == DIAL && now_ns() >= log_at {
                println!("Connections: {} connected", self.live());
                log_at = now_ns() + 1_000_000_000;
            }
            std::thread::sleep(Duration::from_millis(2));
        }
    }

    // Spreads a concurrency over the workers one at a time, no worker taking more than it has
    // live connections, and returns what was allocated.
    fn allocate(&self, requested: i32, rate: bool) -> i32 {
        let live: Vec<i32> = self
            .ctls
            .iter()
            .map(|c| c.live.load(Ordering::Acquire))
            .collect();
        let mut given = vec![0; self.ctls.len()];
        let wanted = requested.min(live.iter().sum());
        let mut remaining = wanted;
        while remaining > 0 {
            let mut progress = false;
            for (i, g) in given.iter_mut().enumerate() {
                if remaining > 0 && *g < live[i] {
                    *g += 1;
                    remaining -= 1;
                    progress = true;
                }
            }
            if !progress {
                break;
            }
        }
        for (c, g) in self.ctls.iter().zip(&given) {
            let quota = if rate {
                &c.rate_concurrency
            } else {
                &c.echo_concurrency
            };
            quota.store(*g, Ordering::Release);
        }
        wanted - remaining
    }

    fn echo(&self, stage: i32, total: i64, concurrency: i32, limit: i32) -> Result<(), String> {
        let (mut assigned, mut quota) = (0i64, 0i64);
        for c in &self.ctls {
            quota += c.echo_concurrency.load(Ordering::Acquire) as i64;
            let end = if concurrency > 0 {
                total * quota / concurrency as i64
            } else {
                0
            };
            c.target.store(end - assigned, Ordering::Release);
            assigned = end;
        }
        self.shared.limiter.reset(limit);
        self.shared.stage.store(stage, Ordering::Release);
        self.wait(stage)
    }

    fn collect(&self, which: fn(&WorkerCtl) -> &Mutex<Stats>) -> Stats {
        let mut result = Stats::default();
        for c in &self.ctls {
            let v = which(c).lock().unwrap();
            result.success += v.success;
            result.failed += v.failed;
            result.sent += v.sent;
            result.received += v.received;
            result.recv_bytes += v.recv_bytes;
            if result.begin == 0 || (v.begin != 0 && v.begin < result.begin) {
                result.begin = v.begin;
            }
            result.end = result.end.max(v.end);
            result.latency.extend_from_slice(&v.latency);
        }
        result
    }
}

impl Drop for Runner {
    fn drop(&mut self) {
        self.shared.stage.store(STOP, Ordering::Release);
        for thread in self.threads.drain(..) {
            let _ = thread.join();
        }
    }
}

fn set_latency(r: &mut Report, s: &mut Stats, tpn: bool) {
    r.insert("Success".into(), json!(s.success));
    r.insert("Failed".into(), json!(s.failed));
    let used = (s.end - s.begin).max(1);
    r.insert("Used".into(), json!(used));
    r.insert(
        "TPS".into(),
        json!((s.success as f64 * 1e9 / used as f64) as i64),
    );
    if tpn && !s.latency.is_empty() {
        s.latency.sort_unstable();
        let lat = &s.latency;
        r.insert("Min".into(), json!(lat[0]));
        r.insert("Max".into(), json!(lat[lat.len() - 1]));
        let sum: i128 = lat.iter().map(|v| *v as i128).sum();
        r.insert("Avg".into(), json!((sum / lat.len() as i128) as i64));
        for p in [50usize, 75, 90, 95, 99] {
            let at = ((lat.len() * p).div_ceil(100))
                .saturating_sub(1)
                .min(lat.len() - 1);
            r.insert(format!("TP{p}"), json!(lat[at]));
        }
    }
}

// -m: Linux holds the process to it as an address-space ceiling; macOS has no such limit, so
// Runner::wait checks the peak RSS every 100ms instead.
fn memory_limit(o: &Options) -> Result<(), String> {
    let limit = o.number("m")?;
    if limit == 0 || cfg!(target_os = "macos") {
        return Ok(());
    }
    // SAFETY: existing is a valid out-pointer, and setrlimit only reads it.
    unsafe {
        let mut existing: libc::rlimit = std::mem::zeroed();
        if libc::getrlimit(libc::RLIMIT_AS, &mut existing) != 0 {
            return Err("cannot read process memory limit".into());
        }
        existing.rlim_cur = existing.rlim_max.min(limit as libc::rlim_t);
        if libc::setrlimit(libc::RLIMIT_AS, &existing) != 0 {
            return Err("cannot set process memory limit; use -m=0 for unlimited".into());
        }
    }
    Ok(())
}

fn join_profile(handle: Option<JoinHandle<()>>) {
    if let Some(handle) = handle {
        let _ = handle.join();
    }
}

fn benchmark(o: &Options) -> Result<i32, String> {
    memory_limit(o)?;
    let runner = Runner::new(o)?;
    runner.wait(DIAL)?;
    let mut connections = empty_report("Connections", o);
    let mut dial = runner.collect(|c| &c.dial_stats);
    set_latency(&mut connections, &mut dial, o.b("tpn"));
    connections.insert("Total".into(), json!(dial.success + dial.failed));
    connections.insert(
        "Concurrency".into(),
        json!(runner.ctls.iter().map(|c| c.dial_concurrency).sum::<i32>()),
    );
    save_report(o, "Connections", &connections)?;
    if dial.success == 0 {
        return Err("no WebSocket connections established".into());
    }

    // Where this run's CPU and MEM samples come from: on a run whose server is on this machine
    // the client samples the process itself and the server is never asked.
    let ps = ps::setup_ps(o);
    let ec = match o.i("ec") {
        0 => available_cpus() * 1000,
        ec => ec,
    };
    let concurrency = runner.allocate(ec, false);
    if concurrency == 0 {
        return Err("all connections closed before echo benchmark".into());
    }
    let echo_profile = profile(o, "BenchEcho", o.b("ep"), o.i("epd").max(1));
    let warmup = (dial.success * 5).min(2_000_000);
    println!("BenchEcho warmup: {warmup}");
    runner.echo(WARMUP, warmup, concurrency, o.i("el"))?;
    println!("BenchEcho: {} messages", o.i("en"));
    runner.echo(ECHO, o.i("en") as i64, concurrency, o.i("el"))?;
    let mut echo = empty_report("BenchEcho", o);
    let mut stats = runner.collect(|c| &c.echo_stats);
    set_latency(&mut echo, &mut stats, o.b("tpn"));
    echo.insert("Total".into(), json!(o.i("en")));
    echo.insert("Conns".into(), json!(dial.success));
    echo.insert("Concurrency".into(), json!(concurrency));
    echo.insert("Payload".into(), json!(runner.shared.payloads[0].len()));
    ps::resource_stats(&mut echo, o, false, &ps);
    join_profile(echo_profile);
    save_report(o, "BenchEcho", &echo)?;

    if o.b("rate") {
        let rc = match o.i("rc") {
            0 => 50000,
            rc => rc,
        };
        let rate_concurrency = runner.allocate(rc, true);
        runner.shared.limiter.reset(o.i("rl"));
        let seconds = match o.i("rd") {
            0 => 10,
            s => s,
        };
        let start = now_ns();
        runner.shared.rate_start.store(start, Ordering::Release);
        runner
            .shared
            .rate_end
            .store(start + seconds as i64 * 1_000_000_000, Ordering::Release);
        let rate_profile = profile(o, "BenchRate", o.b("rp"), o.i("rpd").max(1));
        println!("BenchRate: {seconds} seconds");
        runner.shared.stage.store(RATE, Ordering::Release);
        runner.wait(RATE)?;
        let mut rate = empty_report("BenchRate", o);
        let s = runner.collect(|c| &c.rate_stats);
        let payload = runner.shared.payloads[0].len() as i64;
        rate.insert("Duration".into(), json!(seconds as i64 * 1_000_000_000));
        rate.insert("Conns".into(), json!(dial.success));
        rate.insert("Concurrency".into(), json!(rate_concurrency));
        rate.insert("Pipeline".into(), json!(runner.shared.batch));
        rate.insert("SendRate".into(), json!(o.i("rr").max(1)));
        rate.insert("Payload".into(), json!(payload));
        rate.insert("SendTimes".into(), json!(s.sent));
        rate.insert("SendBytes".into(), json!(s.sent * payload));
        rate.insert("RecvTimes".into(), json!(s.received));
        rate.insert("RecvBytes".into(), json!(s.recv_bytes));
        fill_rate_tps(&mut rate);
        ps::resource_stats(&mut rate, o, true, &ps);
        join_profile(rate_profile);
        save_report(o, "BenchRate", &rate)?;
    }
    Ok(if stats.failed != 0 || dial.failed != 0 {
        1
    } else {
        0
    })
}

fn main() {
    // SAFETY: on_signal only stores to an atomic, which is async-signal-safe.
    unsafe {
        libc::signal(libc::SIGINT, on_signal as *const () as libc::sighandler_t);
        libc::signal(libc::SIGTERM, on_signal as *const () as libc::sighandler_t);
    }
    let args: Vec<String> = std::env::args().skip(1).collect();
    let result = Options::parse(&args).and_then(|o| {
        if o.help {
            o.usage();
            return Ok(0);
        }
        if o.b("r") {
            return generate_reports(&o).map(|_| 0);
        }
        benchmark(&o)
    });
    let code = match result {
        Ok(code) => code,
        Err(e) => {
            eprintln!("benchcli-rust: {e}");
            if INTERRUPTED.load(Ordering::Relaxed) {
                130
            } else {
                1
            }
        }
    };
    std::process::exit(code);
}
