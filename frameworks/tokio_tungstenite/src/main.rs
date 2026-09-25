// Echo WebSocket server built on tokio-tungstenite (https://github.com/snapview/tokio-tungstenite),
// the Tokio binding of tungstenite, Rust's most widely used WebSocket implementation.
//
// # Event loops
//
// One current-thread Tokio runtime per CPU the process may run on, each on a thread of its own:
// an event loop per thread, the way uWS and the Go event loop servers have one per poller. A
// listener per benchmark port accepts on one of them, and every connection it accepts is handed
// to the next loop round-robin, which registers its socket and serves it for the rest of its
// life - there is no work stealing between the loops, so a connection's reads, its handshake and
// its writes all happen on the one thread.
//
// # Logic thread pool
//
// -logicpool=true, the default, runs the message callback off the loops, on the pool in pool.rs:
// the loop reads a batch, hands it to the connection's shard, and writes the answer the worker
// sends back. That is the tokio_tungstenite entry. -logicpool=false answers on the loop that read
// the frame instead, on the same ports. -workers sizes the pool.
//
// The /init and /ps routes replicate just enough of frameworks.HandleCommon (see
// frameworks/handlers.go) for the benchmark clients' resource reporting, the way the uwebsockets
// server does: /init starts a background CPU%/RSS sampler and returns the PID, /ps returns the
// samples as JSON. They are served on every benchmark port, so config.Ports' last port doubles as
// the control port. A connection's request head is peeked at, not read, to tell the two apart,
// so that an upgrade reaches tungstenite's handshake whole. /taskpool answers the report's Pool:
// "logicpool" with the pool on and "inline" without it, as the uwebsockets server does. This
// server takes no -taskpool flag: none of the Go pools can run under it.
//
// # Echo
//
// WebSocketStream is a futures Stream + Sink, and Sink::start_send only frames a message, which
// gathers in the socket wrapper (stream.rs); poll_flush writes it out. So the echo loop feeds
// every message that is already readable - those parsed out of the same read, without waiting on
// the socket - and flushes once for the batch, which is one write where sending each message
// would have been one apiece. uWS corks the sends a message callback makes the same way.
use std::io;
use std::net::SocketAddr;
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::sync::{Mutex, OnceLock};
use std::task::Poll;
use std::time::{Duration, Instant};

use futures_util::{FutureExt, SinkExt, StreamExt};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::{TcpListener, TcpStream};
use tokio::runtime::Handle;
use tokio_tungstenite::WebSocketStream;
use tokio_tungstenite::tungstenite::Message;
use tokio_tungstenite::tungstenite::protocol::WebSocketConfig;

mod pool;
mod stream;
use stream::Stream;

// Must match config.Ports[config.TokioTungstenite] in config/config.go;
// config/native_ports_test.go holds the two to it.
const PORT_START: u16 = 17301;
const PORT_END: u16 = 17350;

// One CPU's worth of logic pool workers per this many, and at least one, when -workers does not
// say: the uwebsockets server's default, which was the best of what that server measured, since
// a worker only moves a batch and hands it back while a loop does the poll, the read, the parse
// and the write.
const CORES_PER_WORKER: usize = 4;

// The longest request head read before giving up on a connection.
const MAX_REQUEST_HEAD: usize = 8192;

struct Flags {
    nodelay: bool,
    reuseport: bool,
    threads: usize,
    logic_pool: bool,
    workers: usize,
    ignored: Vec<String>,
}

// Takes -name=value and a bare -name for true, as Go's flag package does for the flags the
// scripts pass. script/servers.sh hands every server the same set - -nodelay, -reuseport, -b, -m
// - and only the first two mean anything here, so the rest are reported and ignored rather than
// fatal: the Go servers each define the ones they read, and this one sizes its buffers in
// ws_config rather than by -b.
fn parse_flags() -> Flags {
    let mut flags = Flags {
        nodelay: true,
        reuseport: true,
        threads: 0,
        logic_pool: true,
        workers: 0,
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
            "logicpool" => flags.logic_pool = as_bool(value),
            "workers" => match value.and_then(|v| v.parse().ok()) {
                Some(workers) => flags.workers = workers,
                None => eprintln!("tokio_tungstenite: ignoring {arg}, want -workers=<count>"),
            },
            _ => flags.ignored.push(arg),
        }
    }
    flags
}

// The loops' handles, in the order connections are handed out to them, and whether the logic
// pool is on. Both are set once, before the first listener accepts.
static LOOPS: OnceLock<Vec<Handle>> = OnceLock::new();
static NEXT_LOOP: AtomicUsize = AtomicUsize::new(0);
static LOGIC_POOL: AtomicBool = AtomicBool::new(true);

fn main() {
    let flags = parse_flags();

    // available_parallelism reads the affinity mask (and a cgroup CPU quota), so it counts the
    // CPUs script/env.sh pins the server to rather than the whole host - the count GOMAXPROCS
    // follows on the Go side.
    let cpus = std::thread::available_parallelism()
        .map(|n| n.get())
        .unwrap_or(1);
    let loops = if flags.threads > 0 {
        flags.threads
    } else {
        cpus
    };
    // The pool's workers sit on top of the loops rather than taking CPUs from them, as the
    // uwebsockets server's do.
    let workers = if !flags.logic_pool {
        0
    } else if flags.workers > 0 {
        flags.workers
    } else {
        (cpus / CORES_PER_WORKER).max(1)
    };
    let (port_start, port_end) = (PORT_START, PORT_END);

    eprintln!(
        "tokio_tungstenite benchmark config: loops={loops} workers={workers} cpus={cpus} logicpool={} nodelay={} reuseport={} ports={port_start}-{port_end}",
        flags.logic_pool, flags.nodelay, flags.reuseport
    );
    if !flags.ignored.is_empty() {
        eprintln!(
            "tokio_tungstenite: ignoring flags this server does not define: {}",
            flags.ignored.join(" ")
        );
    }

    LOGIC_POOL.store(flags.logic_pool, Ordering::Relaxed);
    if flags.logic_pool {
        pool::start(workers);
    }

    // Each loop is a current-thread runtime driven by a thread of its own, which it never leaves:
    // anything spawned onto its handle, from whichever thread, runs there.
    let mut threads = Vec::with_capacity(loops);
    let mut handles = Vec::with_capacity(loops);
    for i in 0..loops {
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .expect("building a tokio event loop");
        handles.push(runtime.handle().clone());
        threads.push(
            std::thread::Builder::new()
                .name(format!("loop-{i}"))
                .spawn(move || runtime.block_on(std::future::pending::<()>()))
                .expect("starting a tokio event loop"),
        );
    }
    let loops = LOOPS.get_or_init(|| handles);

    // The listeners are spread over the loops too, so that accepting is not one loop's work.
    for port in port_start..=port_end {
        let handle = &loops[usize::from(port - port_start) % loops.len()];
        let listener = {
            let _entered = handle.enter();
            match listen(port, flags.reuseport) {
                Ok(listener) => listener,
                Err(err) => {
                    eprintln!("tokio_tungstenite: failed to listen on port {port}: {err}");
                    std::process::exit(1);
                }
            }
        };
        handle.spawn(accept_loop(listener, flags.nodelay));
    }
    for thread in threads {
        let _ = thread.join();
    }
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
                // To the next loop, which registers the socket with its own reactor: a tokio
                // TcpStream belongs to the runtime it was registered with, so it goes across as
                // the std socket under it, which into_std leaves non-blocking.
                let Ok(stream) = stream.into_std() else {
                    continue;
                };
                let loops = LOOPS.get().expect("loops not started");
                let target = &loops[NEXT_LOOP.fetch_add(1, Ordering::Relaxed) % loops.len()];
                target.spawn(async move {
                    if let Ok(stream) = TcpStream::from_std(stream) {
                        handle_connection(stream).await;
                    }
                });
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
    // Dropped here rather than at the end of the function: it would otherwise be part of the
    // task's state for as long as the connection lasts, 8KiB of zeroes a connection.
    drop(buf);

    if head.upgrade {
        // The handshake - the method, Connection, the key and its answer, the version - is
        // tungstenite's.
        if let Ok(ws) =
            tokio_tungstenite::accept_async_with_config(Stream::new(stream), Some(ws_config()))
                .await
        {
            if LOGIC_POOL.load(Ordering::Relaxed) {
                echo_on_pool(ws).await;
            } else {
                echo(ws).await;
            }
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

// The control routes. Anything else is a 404.
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
        // The report's Pool, as config.GetFrameworkTaskPool reads it.
        ("GET", "/taskpool") => (
            "200 OK",
            "text/plain; charset=utf-8",
            if LOGIC_POOL.load(Ordering::Relaxed) {
                "logicpool"
            } else {
                "inline"
            }
            .to_string(),
        ),
        _ => (
            "404 Not Found",
            "text/plain; charset=utf-8",
            "404 page not found\n".to_string(),
        ),
    }
}

// tungstenite's defaults, but for the payload limits - 64MiB for a message and a frame, as the
// uwebsockets server sets uWS's maxPayloadLength (tungstenite's own frame limit is 16MiB) - and
// the read buffer: 4KiB, the size tungstenite's documentation suggests where there are many
// connections, rather than 128KiB reserved and zero-filled for every one - and the write buffer,
// 0, so that each frame goes straight to the socket under it, which gathers a batch's frames in
// a pooled buffer until the echo loop's flush; see stream.rs for both. tungstenite sends no
// pings and has no idle timeout of its own, so a connection opened in the Connections phase stays
// up however long it sits idle, as the uwebsockets server has uWS keep it.
fn ws_config() -> WebSocketConfig {
    WebSocketConfig::default()
        .read_buffer_size(4096)
        .write_buffer_size(0)
        .max_message_size(Some(64 << 20))
        .max_frame_size(Some(64 << 20))
}

async fn echo(mut ws: WebSocketStream<Stream>) {
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

// The echo with the logic pool on: the loop reads a batch - every message already readable, as
// echo takes them - and hands it to the connection's shard, and writes out whatever answers the
// worker has sent back, in the order it sent them. Reading does not wait for the answers, so a
// connection can have a batch on the pool while the next one arrives, as a uWS connection can.
async fn echo_on_pool(mut ws: WebSocketStream<Stream>) {
    enum Event {
        Read(Option<Result<Message, tokio_tungstenite::tungstenite::Error>>),
        Answer(Vec<Message>),
    }
    let shard = pool::shard();
    let (reply, mut answers) = tokio::sync::mpsc::unbounded_channel::<Vec<Message>>();
    loop {
        // Answers first, so that a connection that keeps sending still has its echoes written.
        let event = std::future::poll_fn(|cx| {
            if let Poll::Ready(Some(answer)) = answers.poll_recv(cx) {
                return Poll::Ready(Event::Answer(answer));
            }
            ws.poll_next_unpin(cx).map(Event::Read)
        })
        .await;
        match event {
            Event::Answer(answer) => {
                for msg in answer {
                    if ws.feed(msg).await.is_err() {
                        return;
                    }
                }
                // And the ones that are back already, in the same write.
                while let Ok(answer) = answers.try_recv() {
                    for msg in answer {
                        if ws.feed(msg).await.is_err() {
                            return;
                        }
                    }
                }
                if ws.flush().await.is_err() {
                    return;
                }
            }
            Event::Read(first) => {
                let Some(Ok(mut msg)) = first else {
                    let _ = ws.flush().await;
                    return;
                };
                let mut batch = Vec::new();
                let mut ended = false;
                loop {
                    if matches!(msg, Message::Text(_) | Message::Binary(_)) {
                        batch.push(msg);
                    }
                    match ws.next().now_or_never() {
                        Some(Some(Ok(next))) => msg = next,
                        Some(Some(Err(_))) | Some(None) => {
                            ended = true;
                            break;
                        }
                        None => break,
                    }
                }
                if batch.is_empty() {
                    // Pings and Closes only: tungstenite has queued their answers, which go out
                    // now rather than waiting for an echo to carry them.
                    if ws.flush().await.is_err() {
                        return;
                    }
                } else if shard
                    .send(pool::Job {
                        messages: batch,
                        reply: reply.clone(),
                    })
                    .is_err()
                {
                    return;
                }
                if ended {
                    let _ = ws.flush().await;
                    return;
                }
            }
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
