// The load generator: a fixed set of worker threads, each running a single-threaded Tokio runtime
// over its share of the connections, which only that thread touches - the arrangement
// benchcli-uwscpp has with one uSockets loop per thread. Every connection is a task on its
// worker's runtime holding a tokio-tungstenite WebSocketStream, so the parsing of what the
// server sends, the reassembly of fragments and the answers to pings are all tungstenite's; see
// upgrade.rs for the one part of the upgrade that is not.
//
// What the client sends is not framed per message: every payload is encoded once, masked, at
// startup, and a Rate batch is those frames back to back, as benchcli-uwscpp builds its own. An
// echo or a batch is then one write of bytes that never change, queued on the socket under the
// WebSocketStream once tungstenite has flushed anything of its own (a pong), and drained there
// while the connection goes on reading; see stream.rs. Framing each message through tungstenite
// instead - a header, a fresh random mask, a copy and the masking of the copy, for over two
// million frames a second in BenchPipeline - and awaiting each write whole kept this client's four
// cores saturated while benchcli-uwscpp's used three, left a connection whose socket was full
// unread until it drained, and so had it read a third less than benchcli-uwscpp did against the
// same fib server, which held what the client was slow to read: about 1GB at 50000
// connections, where it holds 100MB now.
//
// What a stage asks of the connections - which of them sends, when, and how many round trips are
// outstanding - is decided in one place per worker, a State the worker's tasks share (Rc and
// RefCell: it never leaves its thread), which hands a connection's task what to send over a
// channel, and hears back from it what arrived. A worker moves through the stages main publishes
// (Dial, Warmup, Echo, Rate, Stop), and says it is done with one by storing its number in `done`,
// after publishing that stage's statistics; release/acquire on the two makes main's reads of the
// statistics and the worker's reads of its assignments safe.
//
// Every stage follows benchcli-uwscpp's engine.hpp, so that the two native clients load a server
// the same way:
//
//   Dial     at most -dc connections dialing at once across the workers, each attempt with -dt
//            for its TCP connect and upgrade together, and -dr attempts -dri apart, on the
//            framework's ports in turn
//   Echo     exactly the target number of round trips, with at most the worker's share of -ec
//            outstanding at once, rotating through the idle connections; a response that takes
//            longer than -io-timeout closes its connection and counts as failed
//   Rate     the live connections divided into -rc groups, each group's connections sent one
//            batch of Pipeline frames - Shared::batch_frame, written as one - every
//            Pipeline/-rr seconds, a connection skipped while its last batch is still being
//            written or five batches are unanswered
//
// -el and -rl are a token bucket shared by every worker.
use std::cell::RefCell;
use std::cmp::Reverse;
use std::collections::{BinaryHeap, VecDeque};
use std::net::{IpAddr, SocketAddr};
use std::rc::Rc;
use std::sync::atomic::{AtomicBool, AtomicI32, AtomicI64, Ordering};
use std::sync::{Arc, LazyLock, Mutex};
use std::task::Poll;
use std::time::{Duration, Instant};

use bytes::Bytes;
use futures_util::{SinkExt, StreamExt};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpStream;
use tokio::sync::mpsc;
use tokio::task::JoinHandle;
use tokio_tungstenite::WebSocketStream;
use tokio_tungstenite::tungstenite::Message;
use tokio_tungstenite::tungstenite::handshake::client::generate_key;
use tokio_tungstenite::tungstenite::handshake::derive_accept_key;
use tokio_tungstenite::tungstenite::protocol::{Role, WebSocketConfig};

use crate::metadata::ports;
use crate::options::Options;
use crate::stream::Stream;
use crate::upgrade;

pub const DIAL: i32 = 1;
pub const WARMUP: i32 = 2;
pub const ECHO: i32 = 3;
pub const RATE: i32 = 4;
pub const STOP: i32 = 5;

static EPOCH: LazyLock<Instant> = LazyLock::new(Instant::now);

// Nanoseconds on a monotonic clock; only ever subtracted from one another.
pub fn now_ns() -> i64 {
    EPOCH.elapsed().as_nanos() as i64
}

// splitmix64, for the payloads and for picking which of them an echo carries: nothing here needs
// to be unpredictable to more than a server's caching. The masks are tungstenite's.
pub struct Rng(u64);

impl Rng {
    pub fn new() -> Rng {
        use std::hash::{BuildHasher, Hasher};
        let mut hasher = std::collections::hash_map::RandomState::new().build_hasher();
        hasher.write_u128(now_ns() as u128);
        Rng(hasher.finish())
    }

    pub fn next(&mut self) -> u64 {
        self.0 = self.0.wrapping_add(0x9E3779B97F4A7C15);
        let mut z = self.0;
        z = (z ^ (z >> 30)).wrapping_mul(0xBF58476D1CE4E5B9);
        z = (z ^ (z >> 27)).wrapping_mul(0x94D049BB133111EB);
        z ^ (z >> 31)
    }

    pub fn fill(&mut self, out: &mut [u8]) {
        for chunk in out.chunks_mut(8) {
            let bytes = self.next().to_le_bytes();
            chunk.copy_from_slice(&bytes[..chunk.len()]);
        }
    }
}

// Messages per second, with an initial one-second burst; 0 is no limit.
pub struct TokenBucket {
    limit: AtomicI32,
    state: Mutex<(f64, i64)>,
}

impl TokenBucket {
    fn new() -> TokenBucket {
        TokenBucket {
            limit: AtomicI32::new(0),
            state: Mutex::new((0.0, 0)),
        }
    }

    pub fn reset(&self, limit: i32) {
        let mut state = self.state.lock().unwrap();
        self.limit.store(limit, Ordering::Release);
        *state = (limit as f64, now_ns());
    }

    fn take(&self, n: i32) -> bool {
        let limit = self.limit.load(Ordering::Acquire);
        if limit == 0 {
            return true;
        }
        let mut state = self.state.lock().unwrap();
        let now = now_ns();
        state.0 = (limit as f64).min(state.0 + (now - state.1) as f64 * limit as f64 / 1e9);
        state.1 = now;
        if state.0 < n as f64 {
            return false;
        }
        state.0 -= n as f64;
        true
    }
}

#[derive(Default, Clone)]
pub struct Stats {
    pub success: i64,
    pub failed: i64,
    pub sent: i64,
    pub received: i64,
    pub recv_bytes: i64,
    pub begin: i64,
    pub end: i64,
    pub latency: Vec<i64>,
}

// What every worker reads and nothing changes during a stage.
pub struct Shared {
    pub stage: AtomicI32,
    pub fatal: AtomicBool,
    pub limiter: TokenBucket,
    // The same 1024-message payload pool benchcli-go uses, which echoes are checked against;
    // static, like the frames, since it lives as long as the process.
    pub payloads: Vec<Bytes>,
    // Each payload as the masked binary frame an echo writes, and the Rate batch: `batch` copies
    // of the first frame, written as one. Static and never shared by reference count: a Bytes
    // made from a Vec is one, and every worker cloning the same count once a frame bounced its
    // cache line between the client's cores, 10% of their CPU in atomic adds.
    pub frames: Vec<&'static [u8]>,
    pub batch_frame: &'static [u8],
    // Messages per write in Rate: the report's Pipeline.
    pub batch: i32,
    pub rate_start: AtomicI64,
    pub rate_end: AtomicI64,
}

impl Shared {
    pub fn new(o: &Options) -> Result<Shared, String> {
        let mut rng = Rng::new();
        let size = match o.i("b") {
            0 => 1024,
            n => n as usize,
        };
        let budget = o.number("m")?;
        if size > i32::MAX as usize - 14 || (budget != 0 && size as i64 * 2050 > budget) {
            return Err("payload pool exceeds -m memory budget or maximum frame size".into());
        }
        let payloads = (0..1024)
            .map(|_| {
                let mut data = vec![0u8; size];
                rng.fill(&mut data);
                Bytes::from_static(Box::leak(data.into_boxed_slice()))
            })
            .collect::<Vec<_>>();
        let frames: Vec<&'static [u8]> = payloads
            .iter()
            .map(|p| &*Box::leak(masked_frame(p, &mut rng).into_boxed_slice()))
            .collect();
        // Messages per write, as protocol.Pipeline picks them: -rpl when set, or else as many as
        // -rbs bytes of frames hold; at least one, at most -rr and -rl, and a divisor of -rr.
        let frame_len = size
            + 6
            + if size < 126 {
                0
            } else if size < 65536 {
                2
            } else {
                8
            };
        let rate = o.i("rr").max(1);
        let mut batch = if o.i("rpl") > 0 {
            o.i("rpl")
        } else {
            (o.i("rbs") as usize / frame_len) as i32
        };
        batch = batch.min(rate).max(1);
        if o.i("rl") > 0 {
            batch = batch.min(o.i("rl"));
        }
        while rate % batch != 0 {
            batch -= 1;
        }
        let batch_frame = Box::leak(frames[0].repeat(batch as usize).into_boxed_slice());
        Ok(Shared {
            stage: AtomicI32::new(DIAL),
            fatal: AtomicBool::new(false),
            limiter: TokenBucket::new(),
            payloads,
            frames,
            batch_frame,
            batch,
            rate_start: AtomicI64::new(0),
            rate_end: AtomicI64::new(0),
        })
    }
}

// A worker's side of what main reads and writes. The assignments are written by main only
// while the worker is done with its previous stage, and read at the start of the next.
pub struct WorkerCtl {
    pub done: AtomicI32,
    pub live: AtomicI32,
    pub dial_concurrency: i32,
    pub echo_concurrency: AtomicI32,
    pub rate_concurrency: AtomicI32,
    pub target: AtomicI64,
    pub dial_stats: Mutex<Stats>,
    pub echo_stats: Mutex<Stats>,
    pub rate_stats: Mutex<Stats>,
    pub error: Mutex<String>,
}

const NIL: usize = usize::MAX;

// What a connection's task is told to send.
enum Cmd {
    // One echo request, of this payload.
    Echo(usize),
    // One Rate batch: Shared::batch_frame, as one write.
    Batch,
}

// A connection as the State sees it; the WebSocketStream itself is its task's.
#[derive(Default)]
struct Slot {
    tx: Option<mpsc::UnboundedSender<Cmd>>,
    task: Option<JoinHandle<()>>,
    // Bumped whenever the connection goes, so that its task's last word, arriving after the
    // State has already closed it, is recognised as late and ignored.
    generation: u64,
    ready: bool,
    inflight: bool,
    // A Rate batch is queued or being written.
    writing: bool,
    payload: usize,
    echo_started: i64,
    sent: i64,
    received: i64,
    // The outstanding list, oldest first, through the slots themselves.
    prev: usize,
    next: usize,
}

// How a worker dials: what every one of its connection attempts shares.
#[derive(Clone)]
struct Dialing {
    id: usize,
    workers: usize,
    first_port: u16,
    port_span: usize,
    host: String,
    nodelay: bool,
    retries: i32,
    dial_timeout: Duration,
    retry_interval: Duration,
    config: WebSocketConfig,
}

struct State {
    ctl: Arc<WorkerCtl>,
    shared: Arc<Shared>,
    check: bool,
    tpn: bool,
    send_rate: i32,
    io_timeout: i64,
    count: usize,
    slots: Vec<Slot>,
    rng: Rng,
    stage: i32,
    stopping: bool,
    filling: bool,
    next_dial: usize,
    completed: usize,
    issued: i64,
    target: i64,
    echo_concurrency: usize,
    dial_stats: Stats,
    echo_stats: Stats,
    rate_stats: Stats,
    idle: VecDeque<usize>,
    outstanding_head: usize,
    outstanding_tail: usize,
    outstanding: usize,
    teams: Vec<Vec<usize>>,
    team_events: BinaryHeap<Reverse<(i64, usize)>>,
}

pub struct Worker {
    state: State,
    dialing: Dialing,
}

impl Worker {
    pub fn new(
        o: &Options,
        shared: Arc<Shared>,
        ctl: Arc<WorkerCtl>,
        id: usize,
        workers: usize,
        count: usize,
    ) -> Worker {
        let (first, last) = ports(o.get("f"));
        let mut host = o.get("ip").to_string();
        if host.starts_with('[') && host.ends_with(']') {
            host = host[1..host.len() - 1].to_string();
        }
        let or = |v: i64, default: i64| if v == 0 { default } else { v };
        // The server may send nothing longer than the payload being echoed - or 125 bytes, which
        // any control frame may be - as benchcli-uwscpp's parser holds it to. The read buffer is
        // tungstenite's recommendation for many connections rather than its default: it is
        // reserved for every connection up front, and at its default 128KiB a million of them
        // would want 128GB. It is also the most tungstenite reads at a time, which is why the
        // socket under it reads through a per-thread buffer instead; see stream.rs.
        let max_payload = shared.payloads[0].len().max(125);
        let config = WebSocketConfig::default()
            .read_buffer_size(4096)
            .max_message_size(Some(max_payload))
            .max_frame_size(Some(max_payload));
        Worker {
            dialing: Dialing {
                id,
                workers,
                first_port: first,
                port_span: (last - first + 1) as usize,
                host,
                nodelay: o.b("nodelay"),
                retries: if o.i("dr") != 0 { o.i("dr") } else { 3 },
                dial_timeout: Duration::from_nanos(or(o.d("dt"), 1_000_000_000) as u64),
                retry_interval: Duration::from_nanos(or(o.d("dri"), 100_000_000) as u64),
                config,
            },
            state: State {
                ctl,
                shared,
                check: o.b("check"),
                tpn: o.b("tpn"),
                send_rate: o.i("rr").max(1),
                io_timeout: or(o.d("io-timeout"), 30_000_000_000),
                count,
                slots: (0..count)
                    .map(|_| Slot {
                        prev: NIL,
                        next: NIL,
                        ..Slot::default()
                    })
                    .collect(),
                rng: Rng::new(),
                stage: DIAL,
                stopping: false,
                filling: false,
                next_dial: 0,
                completed: 0,
                issued: 0,
                target: 0,
                echo_concurrency: 0,
                dial_stats: Stats::default(),
                echo_stats: Stats::default(),
                rate_stats: Stats::default(),
                idle: VecDeque::new(),
                outstanding_head: NIL,
                outstanding_tail: NIL,
                outstanding: 0,
                teams: Vec::new(),
                team_events: BinaryHeap::new(),
            },
        }
    }

    pub fn run(self) {
        let ctl = self.state.ctl.clone();
        let shared = self.state.shared.clone();
        let result = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .map_err(|e| format!("cannot create the event loop: {e}"))
            .and_then(|runtime| {
                tokio::task::LocalSet::new()
                    .block_on(&runtime, run_worker(self.state, self.dialing))
            });
        if let Err(e) = result {
            *ctl.error.lock().unwrap() = e;
            shared.fatal.store(true, Ordering::Release);
        }
    }
}

async fn run_worker(state: State, mut dialing: Dialing) -> Result<(), String> {
    let dialers = state.ctl.dial_concurrency;
    let state = Rc::new(RefCell::new(state));
    // Resolved once rather than on every dial: the host is the same for all of them.
    let ip = tokio::net::lookup_host((dialing.host.as_str(), 0))
        .await
        .map_err(|e| format!("cannot resolve {}: {e}", dialing.host))?
        .next()
        .ok_or_else(|| format!("cannot resolve {}", dialing.host))?
        .ip();
    if dialing.host.contains(':') {
        dialing.host = format!("[{}]", dialing.host);
    }
    state.borrow_mut().dial_stats.begin = now_ns();
    for _ in 0..dialers {
        tokio::task::spawn_local(dialer(state.clone(), dialing.clone(), ip));
    }
    let mut ticker = tokio::time::interval(Duration::from_millis(1));
    ticker.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
    loop {
        ticker.tick().await;
        if !state.borrow_mut().tick() {
            break;
        }
    }
    state.borrow_mut().stop();
    Ok(())
}

// One of the worker's -dc dialing slots: dials the next connection nobody has taken, with its
// retries, until there are none left.
async fn dialer(state: Rc<RefCell<State>>, dialing: Dialing, ip: IpAddr) {
    loop {
        let i = {
            let mut s = state.borrow_mut();
            if s.stopping || s.next_dial >= s.count {
                return;
            }
            s.next_dial += 1;
            s.next_dial - 1
        };
        let started = now_ns();
        let mut attempt = 0;
        let ws = loop {
            attempt += 1;
            // Which of the run's connections this is picks the port each attempt dials.
            let id = dialing.id + i * dialing.workers;
            let port = dialing.first_port + ((id + attempt as usize) % dialing.port_span) as u16;
            if let Ok(Ok(ws)) =
                tokio::time::timeout(dialing.dial_timeout, connect(&dialing, ip, port)).await
            {
                break Some(ws);
            }
            if attempt >= dialing.retries {
                break None;
            }
            tokio::time::sleep(dialing.retry_interval).await;
        };
        let mut s = state.borrow_mut();
        s.completed += 1;
        match ws {
            Some(ws) => {
                s.dial_stats.success += 1;
                if s.tpn {
                    s.dial_stats.latency.push(now_ns() - started);
                }
                s.open(&state, i, ws);
            }
            None => s.dial_stats.failed += 1,
        }
    }
}

async fn connect(
    dialing: &Dialing,
    ip: IpAddr,
    port: u16,
) -> Result<WebSocketStream<Stream>, String> {
    let mut stream = TcpStream::connect(SocketAddr::new(ip, port))
        .await
        .map_err(|e| e.to_string())?;
    let _ = stream.set_nodelay(dialing.nodelay);
    let key = generate_key();
    stream
        .write_all(upgrade::request(&dialing.host, port, &key).as_bytes())
        .await
        .map_err(|e| e.to_string())?;
    let mut answer = Vec::with_capacity(1024);
    let end = loop {
        if stream
            .read_buf(&mut answer)
            .await
            .map_err(|e| e.to_string())?
            == 0
        {
            return Err("closed during the upgrade".into());
        }
        if let Some(end) = answer.windows(4).position(|w| w == b"\r\n\r\n") {
            break end + 4;
        }
        if answer.len() > upgrade::MAX_HANDSHAKE {
            return Err("upgrade answer too long".into());
        }
    };
    if end > upgrade::MAX_HANDSHAKE
        || !upgrade::accepted(&answer[..end], &derive_accept_key(key.as_bytes()))
    {
        return Err("upgrade refused".into());
    }
    // Frames the server wrote straight after its answer arrive in the same read.
    let leftover = answer.split_off(end);
    Ok(WebSocketStream::from_partially_read(
        Stream::new(stream),
        leftover,
        Role::Client,
        Some(dialing.config),
    )
    .await)
}

// A connection's task: sends what the State tells it to, and tells the State what arrived,
// until either side is done with it. Everything happens in one poll, so that a write the socket
// cannot take yet drains in the background - see stream.rs - while the connection goes on
// reading; a new command is taken only once the last one is written, which is the one-at-a-time
// order the State hands them out in.
async fn connection(
    state: Rc<RefCell<State>>,
    i: usize,
    generation: u64,
    mut ws: WebSocketStream<Stream>,
    mut rx: mpsc::UnboundedReceiver<Cmd>,
) {
    enum Event {
        Command(Cmd),
        Written,
        Received(Message),
        Closed,
    }
    let shared = state.borrow().shared.clone();
    // Frames waiting for tungstenite's own buffered bytes, if any, to go first.
    let mut next: Option<&'static [u8]> = None;
    // A Rate batch is being written, so the State hears when it is done.
    let mut batch = false;
    loop {
        let event = std::future::poll_fn(|cx| {
            if let Some(frames) = next {
                match ws.poll_flush_unpin(cx) {
                    Poll::Ready(Ok(())) => {
                        ws.get_mut().queue(frames);
                        next = None;
                    }
                    Poll::Ready(Err(_)) => return Poll::Ready(Event::Closed),
                    Poll::Pending => {}
                }
            }
            if ws.get_ref().draining() {
                match ws.get_mut().poll_drain(cx) {
                    Poll::Ready(Ok(())) => return Poll::Ready(Event::Written),
                    Poll::Ready(Err(_)) => return Poll::Ready(Event::Closed),
                    Poll::Pending => {}
                }
            }
            match ws.poll_next_unpin(cx) {
                Poll::Ready(Some(Ok(msg))) => return Poll::Ready(Event::Received(msg)),
                Poll::Ready(_) => return Poll::Ready(Event::Closed),
                Poll::Pending => {}
            }
            if next.is_none() && !ws.get_ref().draining() {
                match rx.poll_recv(cx) {
                    Poll::Ready(Some(cmd)) => return Poll::Ready(Event::Command(cmd)),
                    Poll::Ready(None) => return Poll::Ready(Event::Closed),
                    Poll::Pending => {}
                }
            }
            Poll::Pending
        })
        .await;
        match event {
            Event::Command(Cmd::Echo(payload)) => next = Some(shared.frames[payload]),
            Event::Command(Cmd::Batch) => {
                next = Some(shared.batch_frame);
                batch = true;
            }
            Event::Written => {
                if std::mem::take(&mut batch) {
                    state.borrow_mut().written(i, generation);
                }
            }
            Event::Received(msg) => {
                if !state.borrow_mut().receive(i, generation, msg) {
                    break;
                }
            }
            Event::Closed => break,
        }
    }
    state.borrow_mut().lost(i, generation);
}

// A client's binary frame: FIN, a length in the form RFC 6455 gives it, and the payload masked
// with a key of its own - chosen once, since the frame is written as it is for the whole run.
fn masked_frame(payload: &[u8], rng: &mut Rng) -> Vec<u8> {
    let mut frame = Vec::with_capacity(payload.len() + 14);
    frame.push(0x82);
    match payload.len() {
        n if n < 126 => frame.push(0x80 | n as u8),
        n if n < 65536 => {
            frame.push(0x80 | 126);
            frame.extend_from_slice(&(n as u16).to_be_bytes());
        }
        n => {
            frame.push(0x80 | 127);
            frame.extend_from_slice(&(n as u64).to_be_bytes());
        }
    }
    let mask = (rng.next() as u32).to_be_bytes();
    frame.extend_from_slice(&mask);
    frame.extend(payload.iter().enumerate().map(|(i, b)| b ^ mask[i % 4]));
    frame
}

impl State {
    fn stage_done(&self) -> bool {
        self.ctl.done.load(Ordering::Acquire) == self.stage
    }

    fn open(&mut self, state: &Rc<RefCell<State>>, i: usize, ws: WebSocketStream<Stream>) {
        if self.stopping {
            return;
        }
        let (tx, rx) = mpsc::unbounded_channel();
        let slot = &mut self.slots[i];
        slot.generation += 1;
        slot.ready = true;
        slot.tx = Some(tx);
        slot.task = Some(tokio::task::spawn_local(connection(
            state.clone(),
            i,
            slot.generation,
            ws,
            rx,
        )));
        self.ctl.live.fetch_add(1, Ordering::AcqRel);
    }

    // The State closing a connection: an echo that took too long, or the run ending.
    fn close(&mut self, i: usize) {
        if let Some(task) = self.slots[i].task.take() {
            task.abort();
        }
        if self.slots[i].tx.is_some() {
            self.gone(i);
        }
    }

    // The task's own end, for whatever reason; late if the State has closed it already.
    fn lost(&mut self, i: usize, generation: u64) {
        if self.slots[i].generation == generation && self.slots[i].tx.is_some() {
            self.slots[i].task = None;
            self.gone(i);
        }
    }

    fn gone(&mut self, i: usize) {
        let slot = &mut self.slots[i];
        slot.generation += 1;
        slot.tx = None;
        slot.writing = false;
        if slot.ready {
            slot.ready = false;
            self.ctl.live.fetch_sub(1, Ordering::AcqRel);
        }
        if !self.stopping && self.slots[i].inflight {
            self.finish_echo(i, false);
        }
    }

    fn stop(&mut self) {
        self.stopping = true;
        for i in 0..self.slots.len() {
            self.close(i);
        }
    }

    fn send(&mut self, i: usize, cmd: Cmd) -> bool {
        let sent = self.slots[i]
            .tx
            .as_ref()
            .is_some_and(|tx| tx.send(cmd).is_ok());
        if !sent {
            self.close(i);
        }
        sent
    }

    fn written(&mut self, i: usize, generation: u64) {
        if self.slots[i].generation == generation {
            self.slots[i].writing = false;
        }
    }

    fn outstanding_push(&mut self, i: usize) {
        self.slots[i].prev = self.outstanding_tail;
        self.slots[i].next = NIL;
        if self.outstanding_tail == NIL {
            self.outstanding_head = i;
        } else {
            self.slots[self.outstanding_tail].next = i;
        }
        self.outstanding_tail = i;
        self.outstanding += 1;
    }

    fn outstanding_remove(&mut self, i: usize) {
        let (prev, next) = (self.slots[i].prev, self.slots[i].next);
        if prev == NIL {
            self.outstanding_head = next;
        } else {
            self.slots[prev].next = next;
        }
        if next == NIL {
            self.outstanding_tail = prev;
        } else {
            self.slots[next].prev = prev;
        }
        self.slots[i].prev = NIL;
        self.slots[i].next = NIL;
        self.outstanding -= 1;
    }

    fn finish_echo(&mut self, i: usize, valid: bool) {
        if !self.slots[i].inflight {
            return;
        }
        self.slots[i].inflight = false;
        self.outstanding_remove(i);
        if valid {
            self.echo_stats.success += 1;
            if self.tpn && self.stage == ECHO {
                self.echo_stats
                    .latency
                    .push(now_ns() - self.slots[i].echo_started);
            }
        } else {
            self.echo_stats.failed += 1;
        }
        if self.slots[i].ready {
            self.idle.push_back(i);
        }
        self.fill_echo();
    }

    fn fill_echo(&mut self) {
        if self.filling || self.stage_done() || (self.stage != WARMUP && self.stage != ECHO) {
            return;
        }
        self.filling = true;
        while self.issued < self.target && self.outstanding < self.echo_concurrency {
            let Some(&i) = self.idle.front() else { break };
            if !self.slots[i].ready {
                self.idle.pop_front();
                continue;
            }
            if !self.shared.limiter.take(1) {
                break;
            }
            self.idle.pop_front();
            self.issued += 1;
            let payload = (self.rng.next() % self.shared.payloads.len() as u64) as usize;
            let slot = &mut self.slots[i];
            slot.payload = payload;
            slot.echo_started = now_ns();
            slot.inflight = true;
            self.outstanding_push(i);
            self.send(i, Cmd::Echo(payload));
        }
        if self.ctl.live.load(Ordering::Acquire) == 0 || self.echo_concurrency == 0 {
            self.echo_stats.failed += self.target - self.issued;
            self.issued = self.target;
        }
        if self.issued == self.target && self.outstanding == 0 {
            self.echo_stats.end = now_ns();
            *self.ctl.echo_stats.lock().unwrap() = std::mem::take(&mut self.echo_stats);
            self.ctl.done.store(self.stage, Ordering::Release);
        }
        self.filling = false;
    }

    // What arrived on a connection. Returns false to close it.
    fn receive(&mut self, i: usize, generation: u64, msg: Message) -> bool {
        if self.slots[i].generation != generation {
            return false;
        }
        let (binary, data) = match msg {
            Message::Binary(data) => (true, data),
            Message::Text(text) => (false, Bytes::from(text)),
            Message::Close(_) => return false,
            // tungstenite answers pings on its own.
            Message::Ping(_) | Message::Pong(_) | Message::Frame(_) => return true,
        };
        if self.stage_done() {
            return true;
        }
        if self.stage == ECHO || self.stage == WARMUP {
            if !self.slots[i].inflight {
                return false;
            }
            let valid =
                !self.check || (binary && data == self.shared.payloads[self.slots[i].payload]);
            self.finish_echo(i, valid);
        } else if self.stage == RATE && (!self.check || (binary && data == self.shared.payloads[0]))
        {
            self.slots[i].received += 1;
            self.rate_stats.received += 1;
            self.rate_stats.recv_bytes += data.len() as i64;
        }
        true
    }

    fn begin_stage(&mut self, next: i32) {
        self.stage = next;
        if next == WARMUP || next == ECHO {
            self.echo_stats = Stats {
                begin: now_ns(),
                ..Stats::default()
            };
            self.issued = 0;
            self.target = self.ctl.target.load(Ordering::Acquire);
            self.echo_concurrency = self.ctl.echo_concurrency.load(Ordering::Acquire) as usize;
            self.idle.clear();
            while self.outstanding_head != NIL {
                let head = self.outstanding_head;
                self.outstanding_remove(head);
            }
            self.idle
                .extend((0..self.slots.len()).filter(|&i| self.slots[i].ready));
            self.fill_echo();
        } else if next == RATE {
            self.rate_stats = Stats::default();
            let rate_concurrency = self.ctl.rate_concurrency.load(Ordering::Acquire) as usize;
            let n = rate_concurrency.min(self.ctl.live.load(Ordering::Acquire).max(0) as usize);
            self.teams = vec![Vec::new(); n];
            if n > 0 {
                let mut at = 0;
                for i in 0..self.slots.len() {
                    if self.slots[i].ready {
                        self.slots[i].sent = 0;
                        self.slots[i].received = 0;
                        self.teams[at % n].push(i);
                        at += 1;
                    }
                }
            }
            // Rate covers [start, end): the first batch at start, then one each interval. A
            // one-second run therefore sends one full second's quota and leaves time for the
            // last echoes to arrive before its snapshot.
            let start = self.shared.rate_start.load(Ordering::Acquire);
            self.team_events = (0..n).map(|j| Reverse((start, j))).collect();
        }
    }

    // Runs every millisecond. Returns false once the worker is to stop.
    fn tick(&mut self) -> bool {
        let desired = self.shared.stage.load(Ordering::Acquire);
        if desired == STOP || self.shared.fatal.load(Ordering::Acquire) {
            return false;
        }
        if self.stage != desired {
            self.begin_stage(desired);
        }
        let now = now_ns();
        if self.stage == DIAL && !self.stage_done() {
            if self.completed == self.count {
                self.dial_stats.end = now;
                *self.ctl.dial_stats.lock().unwrap() = std::mem::take(&mut self.dial_stats);
                self.ctl.done.store(DIAL, Ordering::Release);
            }
        } else if self.stage == WARMUP || self.stage == ECHO {
            while self.outstanding_head != NIL
                && now - self.slots[self.outstanding_head].echo_started >= self.io_timeout
            {
                self.close(self.outstanding_head);
            }
            self.fill_echo();
        } else if self.stage == RATE && !self.stage_done() {
            let batch = self.shared.batch;
            let interval = (1_000_000_000i64 * batch as i64 / self.send_rate as i64).max(1);
            let rate_end = self.shared.rate_end.load(Ordering::Acquire);
            let send_through = now.min(rate_end - 1);
            let limiter = self.shared.clone();
            while self
                .team_events
                .peek()
                .is_some_and(|Reverse((due, _))| *due <= send_through)
            {
                let Reverse((due, team)) = self.team_events.pop().unwrap();
                let members = std::mem::take(&mut self.teams[team]);
                for &i in &members {
                    let slot = &self.slots[i];
                    if !slot.ready
                        || slot.writing
                        || slot.sent - slot.received + batch as i64 >= batch as i64 * 5
                    {
                        continue;
                    }
                    if !limiter.limiter.take(batch) {
                        continue;
                    }
                    self.slots[i].writing = true;
                    if self.send(i, Cmd::Batch) {
                        self.slots[i].sent += batch as i64;
                        self.rate_stats.sent += batch as i64;
                    }
                }
                self.teams[team] = members;
                self.team_events
                    .push(Reverse(((due + interval).max(now + 1), team)));
            }
            if now >= rate_end {
                *self.ctl.rate_stats.lock().unwrap() = std::mem::take(&mut self.rate_stats);
                self.ctl.done.store(RATE, Ordering::Release);
            }
        }
        true
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    // The three length forms, each unmasking back to its payload.
    #[test]
    fn masked_frames() {
        let mut rng = Rng::new();
        for (len, header) in [(125usize, 2usize), (126, 4), (65535, 4), (65536, 10)] {
            let payload: Vec<u8> = (0..len).map(|i| i as u8).collect();
            let frame = masked_frame(&payload, &mut rng);
            assert_eq!(frame[0], 0x82);
            assert_eq!(frame[1] & 0x80, 0x80, "unmasked");
            let n = match frame[1] & 0x7f {
                126 => u16::from_be_bytes([frame[2], frame[3]]) as usize,
                127 => u64::from_be_bytes(frame[2..10].try_into().unwrap()) as usize,
                n => n as usize,
            };
            assert_eq!((n, frame.len()), (len, header + 4 + len));
            let mask = &frame[header..header + 4];
            let unmasked: Vec<u8> = frame[header + 4..]
                .iter()
                .enumerate()
                .map(|(i, b)| b ^ mask[i % 4])
                .collect();
            assert_eq!(unmasked, payload);
        }
    }
}
