// Echo WebSocket server built on tokio-tungstenite (https://github.com/snapview/tokio-tungstenite),
// the Tokio binding of tungstenite, Rust's most widely used WebSocket implementation. One
// multi-threaded Tokio runtime with a worker thread per CPU the process may run on, one listener
// per benchmark port, one task per connection - the Rust counterpart of a Go server's goroutine
// per connection on GOMAXPROCS threads.
//
// The /init and /ps routes replicate just enough of frameworks.HandleCommon (see
// frameworks/handlers.go) for the benchmark clients' resource reporting, the way the uwebsockets
// server does: /init starts a background CPU%/RSS sampler and returns the PID, /ps returns the
// samples as JSON. They are served on every benchmark port, so config.Ports' last port doubles as
// the control port. A connection's request head is peeked at, not read, to tell the two apart,
// so that an upgrade reaches tungstenite's handshake whole. There is no /taskpool: this server
// takes no -taskpool flag, and the reports show "-" for it as they do for every framework
// without a pool hook.
//
// # Echo
//
// WebSocketStream is a futures Stream + Sink, and Sink::start_send only frames a message into
// tungstenite's write buffer; poll_flush writes it out. So the echo loop feeds every message that
// is already readable - those parsed out of the same read, without waiting on the socket - and
// flushes once for the batch, which is one write where sending each message would have been one
// apiece. uWS corks the sends a message callback makes the same way.
use std::io;
use std::net::SocketAddr;
use std::sync::Mutex;
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::{Duration, Instant};

use futures_util::{FutureExt, SinkExt, StreamExt};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::{TcpListener, TcpStream};
use tokio_tungstenite::WebSocketStream;
use tokio_tungstenite::tungstenite::Message;
use tokio_tungstenite::tungstenite::protocol::WebSocketConfig;

// Must match config.Ports[config.TokioTungstenite] in config/config.go; config/native_ports_test.go
// holds the two to it.
const PORT_START: u16 = 32001;
const PORT_END: u16 = 32050;

// The longest request head read before giving up on a connection.
const MAX_REQUEST_HEAD: usize = 8192;

struct Flags {
    nodelay: bool,
    reuseport: bool,
    threads: usize,
    ignored: Vec<String>,
}

// Takes -name=value and a bare -name for true, as Go's flag package does for the flags the
// scripts pass. script/servers.sh hands every server the same set - -nodelay, -reuseport, -b, -m
// - and only the first two mean anything here, so the rest are reported and ignored rather than
// fatal: the Go servers each define the ones they read, and this one keeps tungstenite's own
// buffer sizes rather than -b's; see ws_config.
fn parse_flags() -> Flags {
    let mut flags = Flags {
        nodelay: true,
        reuseport: true,
        threads: 0,
        ignored: Vec::new(),
    };
    for arg in std::env::args().skip(1) {
        let trimmed = arg.trim_start_matches('-');
        let (name, value) = match trimmed.split_once('=') {
            Some((name, value)) => (name, Some(value)),
            None => (trimmed, None),
        };
        let as_bool = |value: Option<&str>| {
            matches!(
                value,
                None | Some("true")
                    | Some("1")
                    | Some("TRUE")
                    | Some("True")
                    | Some("t")
                    | Some("T")
            )
        };
        match name {
            "nodelay" => flags.nodelay = as_bool(value),
            "reuseport" => flags.reuseport = as_bool(value),
            "threads" => match value.and_then(|v| v.parse().ok()) {
                Some(threads) => flags.threads = threads,
                None => eprintln!("tokio_tungstenite: ignoring {arg}, want -threads=<count>"),
            },
            _ => flags.ignored.push(arg),
        }
    }
    flags
}

fn main() {
    let flags = parse_flags();

    // available_parallelism reads the affinity mask (and a cgroup CPU quota), so it counts the
    // CPUs script/env.sh pins the server to rather than the whole host - the count GOMAXPROCS
    // follows on the Go side, and the one Tokio sizes its workers by by default.
    let cpus = std::thread::available_parallelism()
        .map(|n| n.get())
        .unwrap_or(1);
    let threads = if flags.threads > 0 {
        flags.threads
    } else {
        cpus
    };

    eprintln!(
        "tokio_tungstenite benchmark config: threads={threads} cpus={cpus} nodelay={} reuseport={} ports={PORT_START}-{PORT_END}",
        flags.nodelay, flags.reuseport
    );
    if !flags.ignored.is_empty() {
        eprintln!(
            "tokio_tungstenite: ignoring flags this server does not define: {}",
            flags.ignored.join(" ")
        );
    }

    let runtime = tokio::runtime::Builder::new_multi_thread()
        .worker_threads(threads)
        .enable_all()
        .build()
        .expect("building the tokio runtime");

    runtime.block_on(async move {
        let mut accept_loops = Vec::new();
        for port in PORT_START..=PORT_END {
            let listener = match listen(port, flags.reuseport) {
                Ok(listener) => listener,
                Err(err) => {
                    eprintln!("tokio_tungstenite: failed to listen on port {port}: {err}");
                    std::process::exit(1);
                }
            };
            accept_loops.push(tokio::spawn(accept_loop(listener, flags.nodelay)));
        }
        for accept_loop in accept_loops {
            let _ = accept_loop.await;
        }
    });
}

fn listen(port: u16, reuseport: bool) -> io::Result<TcpListener> {
    use socket2::{Domain, Socket, Type};
    let addr = SocketAddr::from(([0, 0, 0, 0], port));
    let socket = Socket::new(Domain::IPV4, Type::STREAM, None)?;
    socket.set_reuse_address(true)?;
    #[cfg(unix)]
    if reuseport {
        socket.set_reuse_port(true)?;
    }
    #[cfg(not(unix))]
    let _ = reuseport;
    socket.set_nonblocking(true)?;
    socket.bind(&addr.into())?;
    // The kernel caps this at net.core.somaxconn, which is the backlog the Go servers listen
    // with too.
    socket.listen(65535)?;
    TcpListener::from_std(socket.into())
}

async fn accept_loop(listener: TcpListener, nodelay: bool) {
    loop {
        match listener.accept().await {
            Ok((stream, _)) => {
                let _ = stream.set_nodelay(nodelay);
                tokio::spawn(handle_connection(stream));
            }
            Err(err) => {
                // Out of file descriptors, most likely. Back off rather than spin on it; the
                // connections already accepted keep being served meanwhile.
                eprintln!("tokio_tungstenite: accept failed: {err}");
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
        }
    }
}

// What one request head said, copied out of the read buffer so the buffer can be taken apart.
struct RequestHead {
    head_len: usize,
    method: String,
    path: String,
    upgrade: bool,
    content_length: usize,
}

fn parse_head(buf: &[u8]) -> Result<Option<RequestHead>, ()> {
    let mut headers = [httparse::EMPTY_HEADER; 32];
    let mut req = httparse::Request::new(&mut headers);
    let head_len = match req.parse(buf) {
        Ok(httparse::Status::Complete(len)) => len,
        Ok(httparse::Status::Partial) => return Ok(None),
        Err(_) => return Err(()),
    };
    let mut upgrade = false;
    let mut content_length = 0;
    for header in req.headers.iter() {
        if header.name.eq_ignore_ascii_case("upgrade") {
            upgrade = std::str::from_utf8(header.value).is_ok_and(|v| {
                v.split(',')
                    .any(|t| t.trim().eq_ignore_ascii_case("websocket"))
            });
        } else if header.name.eq_ignore_ascii_case("content-length") {
            content_length = std::str::from_utf8(header.value)
                .ok()
                .and_then(|v| v.trim().parse().ok())
                .ok_or(())?;
        }
    }
    Ok(Some(RequestHead {
        head_len,
        method: req.method.unwrap_or("").to_string(),
        path: req.path.unwrap_or("/").to_string(),
        upgrade,
        content_length,
    }))
}

async fn handle_connection(mut stream: TcpStream) {
    // Peeked, not read: an upgrade is handed to tungstenite's handshake with its request still
    // in the socket, and a control request is read below once it is known to be one. A peek
    // returns at once while what it has already seen is unread, so a head that has not all
    // arrived yet is waited on a millisecond at a time rather than spun on.
    let mut buf = vec![0u8; MAX_REQUEST_HEAD];
    let mut seen = 0;
    let head = loop {
        let n = match stream.peek(&mut buf).await {
            Ok(0) | Err(_) => return,
            Ok(n) => n,
        };
        match parse_head(&buf[..n]) {
            Ok(Some(head)) => break head,
            Ok(None) if n < MAX_REQUEST_HEAD => {}
            _ => return,
        }
        if n == seen {
            tokio::time::sleep(Duration::from_millis(1)).await;
        }
        seen = n;
    };

    if head.upgrade {
        // The handshake - the method, Connection, the key and its answer, the version - is
        // tungstenite's.
        if let Ok(ws) = tokio_tungstenite::accept_async_with_config(stream, Some(ws_config())).await
        {
            echo(ws).await;
        }
        return;
    }

    let total = head.head_len + head.content_length;
    let mut request = vec![0u8; total];
    if stream.read_exact(&mut request).await.is_err() {
        return;
    }
    let (status, content_type, reply) =
        control(&head.method, &head.path, &request[head.head_len..]);
    let response = format!(
        "HTTP/1.1 {status}\r\nContent-Type: {content_type}\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{reply}",
        reply.len()
    );
    let _ = stream.write_all(response.as_bytes()).await;
    let _ = stream.shutdown().await;
}

// The control routes. Anything else is a 404, which is also what config.GetFrameworkTaskPool and
// benchcli-uwscpp read as "no pool" for /taskpool.
fn control(method: &str, path: &str, body: &[u8]) -> (&'static str, &'static str, String) {
    let path = path.split('?').next().unwrap_or(path);
    match (method, path) {
        ("POST", "/init") => {
            PS_SAMPLER.start(Duration::from_nanos(parse_ps_interval_nanos(body)));
            (
                "200 OK",
                "text/plain; charset=utf-8",
                std::process::id().to_string(),
            )
        }
        ("GET", "/ps") => ("200 OK", "application/json", PS_SAMPLER.ps_json()),
        _ => (
            "404 Not Found",
            "text/plain; charset=utf-8",
            "404 page not found\n".to_string(),
        ),
    }
}

// tungstenite's defaults, but for the payload limits: 64MiB for a message and a frame, as the
// uwebsockets server sets uWS's maxPayloadLength (tungstenite's own frame limit is 16MiB). The
// buffers stay as the library ships them - a 128KiB read buffer a connection, reserved up front
// and zero-filled on every read, and writes gathered up to 128KiB before they go out - since that
// is what an application on it gets; see README.md for what the read buffer does to the memory
// column. tungstenite sends no
// pings and has no idle timeout of its own, so a connection opened in the Connections phase stays
// up however long it sits idle, as the uwebsockets server has uWS keep it.
fn ws_config() -> WebSocketConfig {
    WebSocketConfig::default()
        .max_message_size(Some(64 << 20))
        .max_frame_size(Some(64 << 20))
}

async fn echo(mut ws: WebSocketStream<TcpStream>) {
    while let Some(first) = ws.next().await {
        let Ok(mut msg) = first else { return };
        loop {
            match msg {
                Message::Text(_) | Message::Binary(_) => {
                    if ws.feed(msg).await.is_err() {
                        return;
                    }
                }
                // tungstenite answers a Ping, and a Close, on its own; the answer goes out with
                // the flush below, and after a Close the stream ends on its own.
                Message::Ping(_) | Message::Pong(_) | Message::Close(_) | Message::Frame(_) => {}
            }
            // Take the next message only if it is already here: one the last read parsed out
            // along with this one, or bytes already sitting in the socket. Anything that would
            // wait on the client ends the batch, and the batch is written before waiting.
            match ws.next().now_or_never() {
                Some(Some(Ok(next))) => msg = next,
                Some(Some(Err(_))) | Some(None) => {
                    let _ = ws.flush().await;
                    return;
                }
                None => break,
            }
        }
        if ws.flush().await.is_err() {
            return;
        }
    }
}

// Minimal stand-in for gopsutil's process.Process.Percent/MemoryInfo, sampled at the interval
// /init asks for. Serves github.com/lesismal/perf's PSCounter JSON shape closely enough for
// benchcli-uwscpp's resourceStats() and benchcli-go's config.GetFrameworkPsInfo, which read only
// the "cpu" and "mem"[].rss fields. The same sampler as frameworks/uwebsockets/server.cpp's.
struct PsSampler {
    started: AtomicBool,
    samples: Mutex<(Vec<f64>, Vec<u64>)>,
}

static PS_SAMPLER: PsSampler = PsSampler {
    started: AtomicBool::new(false),
    samples: Mutex::new((Vec::new(), Vec::new())),
};

impl PsSampler {
    // Once, however many times /init arrives: a client that retried the request can deliver it
    // twice, and the Go servers and the uwebsockets one guard their samplers the same way.
    fn start(&'static self, interval: Duration) {
        if self.started.swap(true, Ordering::AcqRel) {
            return;
        }
        let interval = if interval.is_zero() {
            Duration::from_secs(1)
        } else {
            interval
        };
        std::thread::spawn(move || self.run(interval));
    }

    fn run(&self, interval: Duration) {
        // SAFETY: sysconf has no preconditions.
        let ticks_per_sec = unsafe { libc::sysconf(libc::_SC_CLK_TCK) }.max(1) as f64;
        let mut last_ticks = read_cpu_ticks().unwrap_or(0);
        let mut last_at = Instant::now();
        loop {
            std::thread::sleep(interval);
            let now = Instant::now();
            let wall = now.duration_since(last_at).as_secs_f64();
            let ticks = read_cpu_ticks();
            let percent = match ticks {
                Some(ticks) if wall > 0.0 => {
                    (ticks.saturating_sub(last_ticks) as f64 / ticks_per_sec) / wall * 100.0
                }
                _ => 0.0,
            };
            last_ticks = ticks.unwrap_or(last_ticks);
            last_at = now;
            let rss = read_rss_bytes();
            let mut samples = self.samples.lock().unwrap();
            samples.0.push(percent);
            samples.1.push(rss);
        }
    }

    fn ps_json(&self) -> String {
        let samples = self.samples.lock().unwrap();
        let cpu: Vec<String> = samples.0.iter().map(|c| c.to_string()).collect();
        let mem: Vec<String> = samples
            .1
            .iter()
            .map(|rss| format!("{{\"rss\":{rss}}}"))
            .collect();
        format!(
            "{{\"cpu\":[{}],\"mem\":[{}]}}",
            cpu.join(","),
            mem.join(",")
        )
    }
}

// utime + stime from /proc/self/stat, in clock ticks.
fn read_cpu_ticks() -> Option<u64> {
    let stat = std::fs::read_to_string("/proc/self/stat").ok()?;
    // The comm field (2nd, parenthesized) may itself contain spaces, so split what follows its
    // closing paren. The first field there is the state (3rd overall), which puts utime and
    // stime (14th and 15th overall) at 11 and 12.
    let rest = &stat[stat.rfind(')')? + 1..];
    let fields: Vec<&str> = rest.split_whitespace().collect();
    let utime: u64 = fields.get(11)?.parse().ok()?;
    let stime: u64 = fields.get(12)?.parse().ok()?;
    Some(utime + stime)
}

fn read_rss_bytes() -> u64 {
    let Ok(status) = std::fs::read_to_string("/proc/self/status") else {
        return 0;
    };
    status
        .lines()
        .find_map(|line| line.strip_prefix("VmRSS:"))
        .and_then(|kb| kb.trim().trim_end_matches("kB").trim().parse::<u64>().ok())
        .map_or(0, |kb| kb * 1024)
}

// Body is the JSON encoding/json produces for config.InitArgs, e.g. {"PsInterval":200000000}.
// Hand-rolled rather than pulling in a JSON crate for a single integer field.
fn parse_ps_interval_nanos(body: &[u8]) -> u64 {
    let body = String::from_utf8_lossy(body);
    let Some(pos) = body.find("PsInterval") else {
        return 0;
    };
    let rest = &body[pos + "PsInterval".len()..];
    let Some(colon) = rest.find(':') else {
        return 0;
    };
    let digits: String = rest[colon + 1..]
        .trim_start_matches([' ', '"'])
        .chars()
        .take_while(|c| c.is_ascii_digit())
        .collect();
    digits.parse().unwrap_or(0)
}
