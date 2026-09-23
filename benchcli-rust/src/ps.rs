// Where a report's CPU and MEM columns - and so EER and EchoEER - come from.
//
// Asking the server over its /ps route is the only way when it is on another machine, and it
// is also a request that has to arrive while the server is buried under the connections it has
// just finished echoing to - precisely when it is most likely to be reset or answered too late.
// A run whose server is on this machine does not need the request: the client reads the
// process' own CPU time and resident memory from the operating system. Mirrors
// config/pssource.go and benchcli-uwscpp/pssample.hpp, down to how a sample becomes a
// percentage: the CPU time used over the interval divided by the interval, times 100, so 100 is
// one core busy - the number gopsutil gives the Go client.
use std::net::{IpAddr, ToSocketAddrs, UdpSocket};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::thread::JoinHandle;
use std::time::{Duration, Instant};

use serde_json::{Map, Value, json};

use crate::http::{CONTROL_ATTEMPTS, control_url, http_retry};
use crate::options::{Options, PS_MODE_AUTO, PS_MODE_LOCAL};

// The name every framework's server binary is built under: script/build.sh writes
// ./output/bin/<framework>.server, and script/killone.sh stops it by the same name.
fn server_process_name(framework: &str) -> String {
    format!("{framework}.server")
}

fn base_name(path: &str) -> &str {
    path.rsplit('/').next().unwrap_or(path)
}

// pid and executable base name from ps, for one pid or - with None - every process this user
// can see. Only where there is no /proc to read instead.
#[cfg(not(target_os = "linux"))]
fn ps_names(pid: Option<i32>) -> Result<Vec<(i32, String)>, String> {
    let mut command = std::process::Command::new("ps");
    command.args(["-o", "pid=,comm="]);
    match pid {
        Some(pid) => command.args(["-p", &pid.to_string()]),
        None => command.arg("-A"),
    };
    let out = command
        .output()
        .map_err(|e| format!("cannot run ps: {e}"))?;
    Ok(String::from_utf8_lossy(&out.stdout)
        .lines()
        .filter_map(|line| {
            let line = line.trim_start();
            let (pid, rest) = line.split_once(char::is_whitespace)?;
            Some((pid.parse().ok()?, base_name(rest.trim()).to_string()))
        })
        .collect())
}

// The base name of a pid's argv[0], which for a server started by script/server.sh is
// <framework>.server.
fn process_name(pid: i32) -> Result<String, String> {
    #[cfg(target_os = "linux")]
    {
        let cmdline = std::fs::read(format!("/proc/{pid}/cmdline"))
            .map_err(|_| format!("pid {pid}: no such process"))?;
        let argv0 = cmdline.split(|b| *b == 0).next().unwrap_or(&[]);
        if argv0.is_empty() {
            return Err(format!("pid {pid}: empty command line"));
        }
        Ok(base_name(&String::from_utf8_lossy(argv0)).to_string())
    }
    #[cfg(not(target_os = "linux"))]
    {
        ps_names(Some(pid))?
            .into_iter()
            .find(|(p, _)| *p == pid)
            .map(|(_, name)| name)
            .ok_or_else(|| format!("pid {pid}: no such process"))
    }
}

// The pid of this framework's server on this machine, found by the name it was built under.
// Two answering to one name is an error rather than a guess - a leftover from an earlier run
// next to the one being measured - and so is none, which is what a server in another container
// or on another machine looks like from here. Both leave the caller to ask the server instead.
fn find_server_process(framework: &str) -> Result<i32, String> {
    let name = server_process_name(framework);
    let mut matched = Vec::new();
    #[cfg(target_os = "linux")]
    {
        let dir = std::fs::read_dir("/proc").map_err(|_| "cannot read /proc".to_string())?;
        for entry in dir.flatten() {
            let Some(pid) = entry
                .file_name()
                .to_str()
                .and_then(|s| s.parse::<i32>().ok())
            else {
                continue;
            };
            // A process that exited between the listing and the read is simply not a match.
            if process_name(pid).is_ok_and(|n| n == name) {
                matched.push(pid);
            }
        }
    }
    #[cfg(not(target_os = "linux"))]
    for (pid, n) in ps_names(None)? {
        if n == name {
            matched.push(pid);
        }
    }
    match matched.as_slice() {
        [] => Err(format!("no {name} process on this machine")),
        [pid] => Ok(*pid),
        pids => Err(format!(
            "{} {name} processes on this machine ({}): stop the leftovers of earlier runs, e.g. with script/killall.sh",
            pids.len(),
            pids.iter()
                .map(|p| p.to_string())
                .collect::<Vec<_>>()
                .join(" ")
        )),
    }
}

// A pid is only as good as the namespace it came from: the one /init answers with is the
// server's own, a different process here when the server runs in another container.
fn verify_server_process(pid: i32, framework: &str) -> Result<(), String> {
    let (name, want) = (process_name(pid)?, server_process_name(framework));
    if name != want {
        return Err(format!("pid {pid} on this machine is {name}, not {want}"));
    }
    Ok(())
}

// A process' total CPU time in seconds and its resident memory in bytes.
fn read_process(pid: i32) -> Result<(f64, u64), String> {
    #[cfg(target_os = "linux")]
    {
        let stat = std::fs::read_to_string(format!("/proc/{pid}/stat"))
            .map_err(|_| format!("pid {pid}: no such process"))?;
        // The executable name is parenthesised and may contain spaces and parentheses, so the
        // fields are counted from the last ')'.
        let rest = &stat[stat
            .rfind(')')
            .ok_or_else(|| format!("pid {pid}: malformed stat"))?
            + 1..];
        let fields: Vec<&str> = rest.split_whitespace().collect();
        if fields.len() < 13 {
            return Err(format!("pid {pid}: short stat"));
        }
        // From the state field, field 3 of the line: utime is 14, stime 15 and
        // delayacct_blkio_ticks 42, which gopsutil adds to the CPU time, so the clients agree.
        let mut ticks: f64 =
            fields[11].parse::<f64>().unwrap_or(0.0) + fields[12].parse::<f64>().unwrap_or(0.0);
        if let Some(blkio) = fields.get(39).and_then(|f| f.parse::<f64>().ok()) {
            ticks += blkio;
        }
        // SAFETY: sysconf has no preconditions.
        let hz = unsafe { libc::sysconf(libc::_SC_CLK_TCK) };
        let statm = std::fs::read_to_string(format!("/proc/{pid}/statm"))
            .map_err(|_| format!("pid {pid}: cannot read statm"))?;
        let resident: u64 = statm
            .split_whitespace()
            .nth(1)
            .and_then(|v| v.parse().ok())
            .ok_or_else(|| format!("pid {pid}: cannot read statm"))?;
        // SAFETY: as above.
        let page = unsafe { libc::sysconf(libc::_SC_PAGESIZE) };
        Ok((
            ticks / (if hz > 0 { hz as f64 } else { 100.0 }),
            resident * if page > 0 { page as u64 } else { 4096 },
        ))
    }
    #[cfg(target_os = "macos")]
    {
        let mut usage: libc::rusage_info_v2 = unsafe { std::mem::zeroed() };
        // SAFETY: usage is a rusage_info_v2, which is what RUSAGE_INFO_V2 fills in.
        let rc = unsafe {
            libc::proc_pid_rusage(
                pid,
                libc::RUSAGE_INFO_V2,
                &mut usage as *mut _ as *mut libc::rusage_info_t,
            )
        };
        if rc != 0 {
            return Err(format!("pid {pid}: cannot read resource usage"));
        }
        // Mach absolute time units, which are nanoseconds only on Intel: an Apple Silicon tick is
        // 125/3 of one.
        #[repr(C)]
        struct MachTimebaseInfo {
            numer: u32,
            denom: u32,
        }
        unsafe extern "C" {
            fn mach_timebase_info(info: *mut MachTimebaseInfo) -> libc::c_int;
        }
        let mut timebase = MachTimebaseInfo { numer: 1, denom: 1 };
        // SAFETY: timebase is a valid out-pointer.
        if unsafe { mach_timebase_info(&mut timebase) } != 0 || timebase.denom == 0 {
            timebase = MachTimebaseInfo { numer: 1, denom: 1 };
        }
        let ticks = (usage.ri_user_time + usage.ri_system_time) as f64;
        Ok((
            ticks * timebase.numer as f64 / timebase.denom as f64 / 1e9,
            usage.ri_resident_size,
        ))
    }
    #[cfg(not(any(target_os = "linux", target_os = "macos")))]
    {
        let _ = pid;
        Err("sampling a process is not supported on this system".into())
    }
}

// Samples a server process on this machine at the run's -pi interval, the way the server's own
// /ps sampler does.
pub struct LocalSampler {
    samples: Arc<Mutex<(Vec<f64>, Vec<u64>)>>,
    stopping: Arc<AtomicBool>,
    thread: Option<JoinHandle<()>>,
}

impl LocalSampler {
    // A process gone mid-run - a server that exited or was killed - stops the sampling after this
    // many failures in a row; what was sampled before stays.
    const CONSECUTIVE_ERRORS: u32 = 5;

    fn new(pid: i32, interval: Duration) -> Result<LocalSampler, String> {
        // Read once before starting, so a process this client cannot sample at all fails here,
        // where the run can still ask the server instead, rather than at the end of it.
        let (mut last_cpu, _) = read_process(pid)?;
        let mut last_at = Instant::now();
        let samples = Arc::new(Mutex::new((Vec::new(), Vec::new())));
        let stopping = Arc::new(AtomicBool::new(false));
        let (s, stop) = (samples.clone(), stopping.clone());
        let thread = std::thread::spawn(move || {
            let mut errors = 0;
            while !stop.load(Ordering::Acquire) {
                // Short steps, so stopping does not wait out a whole interval.
                let wake = Instant::now() + interval;
                while Instant::now() < wake && !stop.load(Ordering::Acquire) {
                    std::thread::sleep(Duration::from_millis(5));
                }
                if stop.load(Ordering::Acquire) {
                    return;
                }
                let (cpu, rss) = match read_process(pid) {
                    Ok(v) => v,
                    Err(e) => {
                        errors += 1;
                        if errors >= Self::CONSECUTIVE_ERRORS {
                            eprintln!("sampling pid {pid} stopped after {errors} failures: {e}");
                            return;
                        }
                        continue;
                    }
                };
                errors = 0;
                let now = Instant::now();
                let elapsed = now.duration_since(last_at).as_secs_f64();
                let percent = if elapsed > 0.0 {
                    (cpu - last_cpu) / elapsed * 100.0
                } else {
                    0.0
                };
                (last_cpu, last_at) = (cpu, now);
                let mut s = s.lock().unwrap();
                s.0.push(percent);
                s.1.push(rss);
            }
        });
        Ok(LocalSampler {
            samples,
            stopping,
            thread: Some(thread),
        })
    }

    fn samples(&self) -> (Vec<f64>, Vec<u64>) {
        self.samples.lock().unwrap().clone()
    }
}

impl Drop for LocalSampler {
    fn drop(&mut self) {
        self.stopping.store(true, Ordering::Release);
        if let Some(thread) = self.thread.take() {
            let _ = thread.join();
        }
    }
}

// Whether the host the clients dial is this machine: a loopback or unspecified address, or one
// of this machine's own - which is what binding a socket to it tells. It is what tells a
// single-node run from a two-node one without either having to say so. A host that is this
// machine's may still be another container's server; that is why sampling it is attempted
// rather than assumed.
fn local_host(host: &str) -> bool {
    let host = host
        .strip_prefix('[')
        .and_then(|h| h.strip_suffix(']'))
        .unwrap_or(host);
    if host.is_empty() {
        return false;
    }
    let Ok(addrs) = (host, 0).to_socket_addrs() else {
        return false;
    };
    addrs.into_iter().any(|a| {
        let ip: IpAddr = a.ip();
        ip.is_loopback() || ip.is_unspecified() || UdpSocket::bind((ip, 0)).is_ok()
    })
}

// How this run reads the server's CPU and memory.
pub struct PsSetup {
    // The sampler here, or None when only the server samples.
    local: Option<LocalSampler>,
    // Whether /init reached the server, i.e. whether /ps has anything to answer with - the
    // fallback for a sampler here with no samples yet.
    server_sampling: bool,
}

// Settles where the samples come from and starts collecting them. Mirrors config.SetupPS: a
// server sampled from here is not asked to sample itself, and whatever goes wrong with that
// ends with the server sampling itself as it always has.
pub fn setup_ps(o: &Options) -> PsSetup {
    let mut ps = PsSetup {
        local: None,
        server_sampling: false,
    };
    let interval_ns = o.i("pi") as i64 * 1_000_000;
    let interval = Duration::from_nanos(if interval_ns > 0 {
        interval_ns as u64
    } else {
        1_000_000_000
    });
    let mode = o.get("ps");
    let want_local = mode == PS_MODE_LOCAL || (mode == PS_MODE_AUTO && local_host(o.get("ip")));
    if want_local {
        match find_server_process(o.get("f"))
            .and_then(|pid| LocalSampler::new(pid, interval).map(|s| (pid, s)))
        {
            Ok((pid, sampler)) => {
                ps.local = Some(sampler);
                println!(
                    "Server PID: {pid} (sampled here, so it is not asked to sample itself)\npprof: {}/debug/pprof/profile",
                    control_url(o)
                );
                return ps;
            }
            Err(e) => eprintln!(
                "cannot sample the server from this machine, asking it over HTTP instead: {e}"
            ),
        }
    }
    let mut pid = -1;
    let body = json!({ "PsInterval": interval_ns }).to_string();
    // A failed /init is not just a missing pid: it is a server that never started sampling, so
    // every CPU and MEM column of the run would read 0.
    match http_retry(
        &format!("{}/init", control_url(o)),
        Some(&body),
        CONTROL_ATTEMPTS,
    ) {
        Ok(reply) => {
            let reply = String::from_utf8_lossy(&reply).to_string();
            ps.server_sampling = true;
            println!(
                "Server PID: {reply}\npprof: {}/debug/pprof/profile",
                control_url(o)
            );
            pid = reply.trim().parse().unwrap_or(-1);
        }
        Err(e) => eprintln!("server initialization: {e}"),
    }
    // The pid the server gave is from its own namespace, so it names this framework's server
    // here only when the two share one. Where it does, sample it from here as well, with /ps as
    // the fallback.
    if want_local
        && pid > 0
        && verify_server_process(pid, o.get("f")).is_ok()
        && let Ok(sampler) = LocalSampler::new(pid, interval)
    {
        ps.local = Some(sampler);
        println!("sampling pid {pid} here as well, with the server's own /ps as the fallback");
    }
    ps
}

// Fills the CPU, MEM and EER columns from a set of samples, whoever took them. Min and Avg skip
// the first sample, and MEM sorts before it does, the way github.com/lesismal/perf's PSCounter
// computes the same columns.
fn apply_resource_stats(r: &mut Map<String, Value>, cpu: &[f64], mem: &mut [u64], rate: bool) {
    if !cpu.is_empty() {
        let tail = &cpu[(cpu.len() > 1) as usize..];
        r.insert(
            "CPUMin".into(),
            json!(tail.iter().cloned().fold(f64::INFINITY, f64::min)),
        );
        r.insert(
            "CPUAvg".into(),
            json!(tail.iter().sum::<f64>() / tail.len() as f64),
        );
        r.insert(
            "CPUMax".into(),
            json!(cpu.iter().cloned().fold(f64::NEG_INFINITY, f64::max)),
        );
    }
    if !mem.is_empty() {
        mem.sort_unstable();
        let tail = &mem[(mem.len() > 1) as usize..];
        r.insert("MEMMin".into(), json!(tail[0]));
        r.insert(
            "MEMAvg".into(),
            json!(tail.iter().sum::<u64>() / tail.len() as u64),
        );
        r.insert("MEMMax".into(), json!(*mem.last().unwrap()));
    }
    let num = |r: &Map<String, Value>, k: &str| r.get(k).and_then(Value::as_f64).unwrap_or(0.0);
    let avg = num(r, "CPUAvg");
    let tps = if rate {
        num(r, "RecvTimes") / (num(r, "Duration") / 1e9)
    } else {
        num(r, "TPS")
    };
    let eer = if avg > 0.0 && tps.is_finite() {
        tps / avg
    } else {
        0.0
    };
    r.insert((if rate { "EchoEER" } else { "EER" }).into(), json!(eer));
}

pub fn resource_stats(r: &mut Map<String, Value>, o: &Options, rate: bool, ps: &PsSetup) {
    let (mut cpu, mut mem) = ps
        .local
        .as_ref()
        .map(LocalSampler::samples)
        .unwrap_or_default();
    let mut trouble = String::new();
    // The server's own samples: either it is the only one sampling, or the sampler here has
    // nothing yet - a phase shorter than one -pi interval - and the server samples as well.
    if cpu.is_empty() && (ps.local.is_none() || ps.server_sampling) {
        match http_retry(&format!("{}/ps", control_url(o)), None, CONTROL_ATTEMPTS)
            .and_then(|body| serde_json::from_slice::<Value>(&body).map_err(|e| e.to_string()))
        {
            Ok(answer) => {
                if let Some(values) = answer["cpu"].as_array() {
                    cpu = values.iter().filter_map(Value::as_f64).collect();
                }
                if let Some(values) = answer["mem"].as_array() {
                    mem = values
                        .iter()
                        .filter(|v| v.is_object())
                        .map(|v| v["rss"].as_u64().unwrap_or(0))
                        .collect();
                }
            }
            Err(e) => trouble = e,
        }
    }
    apply_resource_stats(r, &cpu, &mut mem, rate);
    if cpu.is_empty() {
        let column = if rate { "EchoEER" } else { "EER" };
        if trouble.is_empty() {
            eprintln!(
                "server resource statistics unavailable, {column} reads 0: nothing was sampled, so either the \
                 sampling never started or the phase was shorter than the -pi sampling interval"
            );
        } else {
            eprintln!("server resource statistics unavailable, {column} reads 0: {trouble}");
        }
    }
}
