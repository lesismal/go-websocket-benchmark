// The load generator: a fixed set of worker threads, each running its own mio event loop over
// its share of the connections, which only that thread touches - the arrangement
// benchcli-uwscpp has with one uSockets loop per thread. A worker moves through the stages
// main publishes (Dial, Warmup, Echo, Rate, Stop), and says it is done with one by storing its
// number in `done`, after publishing that stage's statistics; release/acquire on the two makes
// main's reads of the statistics and the worker's reads of its assignments safe.
//
// Everything a stage does follows benchcli-uwscpp's engine.hpp, so that the two native clients
// load a server the same way:
//
//   Dial     at most -dc connections dialing at once across the workers, each attempt with -dt
//            for its TCP connect and upgrade together, and -dr attempts -dri apart, on the
//            framework's ports in turn
//   Echo     exactly the target number of round trips, with at most the worker's share of -ec
//            outstanding at once, rotating through the idle connections; a response that takes
//            longer than -io-timeout closes its connection and counts as failed
//   Rate     the live connections divided into -rc groups, each group's connections sent one
//            batch of Pipeline frames every Pipeline/-rr seconds, a connection skipped while it
//            has a write pending or five batches unanswered
//
// -el and -rl are a token bucket shared by every worker.
use std::cmp::Reverse;
use std::collections::{BinaryHeap, VecDeque};
use std::io::{ErrorKind, Read, Write};
use std::net::{SocketAddr, ToSocketAddrs};
use std::sync::atomic::{AtomicBool, AtomicI32, AtomicI64, Ordering};
use std::sync::{Arc, LazyLock, Mutex};
use std::time::{Duration, Instant};

use mio::net::TcpStream;
use mio::{Events, Interest, Poll, Token};

use crate::metadata::ports;
use crate::options::Options;
use crate::protocol::{
    self, MAX_HANDSHAKE, OP_BINARY, OP_CLOSE, OP_PING, OP_PONG, Outcome, Parser, Rng, client_frame,
};

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
    // The same 1024-message payload pool benchcli-go uses, and each one's frame.
    pub payloads: Vec<Vec<u8>>,
    frames: Vec<Vec<u8>>,
    batch_frame: Vec<u8>,
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
        let mut payloads = Vec::with_capacity(1024);
        let mut frames = Vec::with_capacity(1024);
        for _ in 0..1024 {
            let mut data = vec![0u8; size];
            rng.fill(&mut data);
            frames.push(client_frame(&data, OP_BINARY, &mut rng));
            payloads.push(data);
        }
        // Messages per write, as protocol.Pipeline picks them: -rpl when set, or else as many as
        // -rbs bytes hold; at least one, at most -rr and -rl, and a divisor of -rr.
        let rate = o.i("rr").max(1);
        let mut batch = if o.i("rpl") > 0 {
            o.i("rpl")
        } else {
            (o.i("rbs") as usize / frames[0].len()) as i32
        };
        batch = batch.min(rate).max(1);
        if o.i("rl") > 0 {
            batch = batch.min(o.i("rl"));
        }
        while rate % batch != 0 {
            batch -= 1;
        }
        let batch_frame = frames[0].repeat(batch as usize);
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

struct Conn {
    // Which of the run's connections this is: it picks the port each attempt dials.
    id: usize,
    stream: Option<TcpStream>,
    // The TCP connect has completed; ready: so has the upgrade.
    connected: bool,
    ready: bool,
    completed: bool,
    inflight: bool,
    attempt: i32,
    payload: usize,
    port: u16,
    generation: u64,
    started: i64,
    echo_started: i64,
    sent: i64,
    received: i64,
    pending: Vec<u8>,
    offset: usize,
    handshake: Vec<u8>,
    accept: String,
    parser: Parser,
    // The outstanding list, oldest first, through the connections themselves.
    prev: usize,
    next: usize,
}

struct DialEvent {
    due: i64,
    conn: usize,
    generation: u64,
    retry: bool,
}

impl PartialEq for DialEvent {
    fn eq(&self, other: &Self) -> bool {
        self.due == other.due
    }
}
impl Eq for DialEvent {}
impl PartialOrd for DialEvent {
    fn partial_cmp(&self, other: &Self) -> Option<std::cmp::Ordering> {
        Some(self.cmp(other))
    }
}
impl Ord for DialEvent {
    // Earliest first out of a max-heap.
    fn cmp(&self, other: &Self) -> std::cmp::Ordering {
        other.due.cmp(&self.due)
    }
}

pub struct Worker {
    ctl: Arc<WorkerCtl>,
    shared: Arc<Shared>,
    count: usize,
    first_port: u16,
    port_span: usize,
    host: String,
    ip: Option<std::net::IpAddr>,
    nodelay: bool,
    check: bool,
    tpn: bool,
    retries: i32,
    send_rate: i32,
    io_timeout: i64,
    dial_timeout: i64,
    retry_interval: i64,
    max_payload: usize,

    poll: Option<Poll>,
    conns: Vec<Conn>,
    read_buf: Vec<u8>,
    rng: Rng,
    stage: i32,
    stopping: bool,
    filling: bool,
    next: usize,
    connecting: i32,
    completed: usize,
    issued: i64,
    target: i64,
    echo_concurrency: usize,
    rate_concurrency: usize,
    dial_stats: Stats,
    echo_stats: Stats,
    rate_stats: Stats,
    idle: VecDeque<usize>,
    outstanding_head: usize,
    outstanding_tail: usize,
    outstanding: usize,
    dial_events: BinaryHeap<DialEvent>,
    teams: Vec<Vec<usize>>,
    team_events: BinaryHeap<Reverse<(i64, usize)>>,
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
        let conns = (0..count)
            .map(|i| Conn {
                id: id + i * workers,
                stream: None,
                connected: false,
                ready: false,
                completed: false,
                inflight: false,
                attempt: 0,
                payload: 0,
                port: 0,
                generation: 0,
                started: 0,
                echo_started: 0,
                sent: 0,
                received: 0,
                pending: Vec::new(),
                offset: 0,
                handshake: Vec::new(),
                accept: String::new(),
                parser: Parser::default(),
                prev: NIL,
                next: NIL,
            })
            .collect();
        Worker {
            max_payload: shared.payloads[0].len(),
            ctl,
            shared,
            count,
            first_port: first,
            port_span: (last - first + 1) as usize,
            host,
            ip: None,
            nodelay: o.b("nodelay"),
            check: o.b("check"),
            tpn: o.b("tpn"),
            retries: if o.i("dr") != 0 { o.i("dr") } else { 3 },
            send_rate: o.i("rr").max(1),
            io_timeout: or(o.d("io-timeout"), 30_000_000_000),
            dial_timeout: or(o.d("dt"), 1_000_000_000),
            retry_interval: or(o.d("dri"), 100_000_000),
            poll: None,
            conns,
            read_buf: vec![0; 512 * 1024],
            rng: Rng::new(),
            stage: DIAL,
            stopping: false,
            filling: false,
            next: 0,
            connecting: 0,
            completed: 0,
            issued: 0,
            target: 0,
            echo_concurrency: 0,
            rate_concurrency: 0,
            dial_stats: Stats::default(),
            echo_stats: Stats::default(),
            rate_stats: Stats::default(),
            idle: VecDeque::new(),
            outstanding_head: NIL,
            outstanding_tail: NIL,
            outstanding: 0,
            dial_events: BinaryHeap::new(),
            teams: Vec::new(),
            team_events: BinaryHeap::new(),
        }
    }

    pub fn run(mut self) {
        if let Err(e) = self.run_loop() {
            *self.ctl.error.lock().unwrap() = e;
            self.shared.fatal.store(true, Ordering::Release);
        }
        self.stopping = true;
        for i in 0..self.conns.len() {
            self.close(i);
        }
    }

    fn run_loop(&mut self) -> Result<(), String> {
        self.poll = Some(Poll::new().map_err(|e| format!("cannot create the event loop: {e}"))?);
        // Resolved once rather than on every dial: the host is the same for all of them.
        self.ip = Some(
            (self.host.as_str(), 0)
                .to_socket_addrs()
                .map_err(|e| format!("cannot resolve {}: {e}", self.host))?
                .next()
                .ok_or_else(|| format!("cannot resolve {}", self.host))?
                .ip(),
        );
        let mut events = Events::with_capacity(1024);
        let tick_every = Duration::from_millis(1);
        let mut last_tick = Instant::now() - tick_every;
        loop {
            let wait = tick_every.saturating_sub(last_tick.elapsed());
            if let Err(e) = self.poll.as_mut().unwrap().poll(&mut events, Some(wait))
                && e.kind() != ErrorKind::Interrupted
            {
                return Err(format!("event loop failed: {e}"));
            }
            for event in events.iter() {
                let i = event.token().0;
                // A connection closed earlier in this batch has nothing left to handle; one is
                // only dialed again from tick, after the batch.
                if self.conns[i].stream.is_none() {
                    continue;
                }
                if !self.conns[i].connected {
                    self.connect_event(i);
                    if !self.conns[i].connected {
                        continue;
                    }
                }
                if event.is_writable() {
                    self.flush(i);
                }
                if event.is_readable() || event.is_read_closed() || event.is_error() {
                    self.read(i, event.is_read_closed() || event.is_error());
                }
            }
            if last_tick.elapsed() >= tick_every {
                last_tick = Instant::now();
                if !self.tick() {
                    return Ok(());
                }
            }
        }
    }

    // The connect's outcome, on the first event of a dialing socket.
    fn connect_event(&mut self, i: usize) {
        let stream = self.conns[i].stream.as_ref().unwrap();
        match stream.take_error() {
            Ok(None) => {}
            _ => return self.close(i),
        }
        match stream.peer_addr() {
            Ok(_) => {}
            Err(e) if e.kind() == ErrorKind::NotConnected => return,
            Err(_) => return self.close(i),
        }
        let _ = stream.set_nodelay(self.nodelay);
        self.conns[i].connected = true;
        let key = protocol::handshake_key(&mut self.rng);
        self.conns[i].accept = protocol::accept_key(&key);
        let mut host = self.host.clone();
        if host.contains(':') {
            host = format!("[{host}]");
        }
        let request = protocol::upgrade_request(&host, self.conns[i].port, &key);
        self.write(i, &request);
    }

    // Reads what the socket has. The events are edge-triggered, so that means reading until the
    // socket has nothing left - but a read that comes back short of the buffer has already
    // emptied it, and data arriving after it raises an edge of its own, so it ends the reading
    // there rather than paying one more read for the EAGAIN, the way Tokio does - uSockets, which
    // benchcli-uwscpp runs on, reads once per event too. It measured within noise of reading to
    // the EAGAIN, but it is one syscall fewer per event for the same answer. An event
    // that says the peer closed, or the socket failed, is read to the end regardless: its edge
    // has been spent, and a short read could leave the close unseen until -io-timeout.
    fn read(&mut self, i: usize, to_the_end: bool) {
        let mut buf = std::mem::take(&mut self.read_buf);
        while let Some(stream) = self.conns[i].stream.as_mut() {
            match stream.read(&mut buf) {
                Ok(0) => {
                    self.close(i);
                    break;
                }
                Ok(n) => {
                    self.consume(i, &buf[..n]);
                    if n < buf.len() && !to_the_end {
                        break;
                    }
                }
                Err(e) if e.kind() == ErrorKind::WouldBlock => break,
                Err(e) if e.kind() == ErrorKind::Interrupted => continue,
                Err(_) => {
                    self.close(i);
                    break;
                }
            }
        }
        self.read_buf = buf;
    }

    fn write(&mut self, i: usize, data: &[u8]) {
        let c = &mut self.conns[i];
        let Some(stream) = c.stream.as_mut() else {
            return;
        };
        if !c.pending.is_empty() {
            c.pending.extend_from_slice(data);
            return;
        }
        let mut written = 0;
        while written < data.len() {
            match stream.write(&data[written..]) {
                Ok(0) => return self.close(i),
                Ok(n) => written += n,
                Err(e) if e.kind() == ErrorKind::WouldBlock => break,
                Err(e) if e.kind() == ErrorKind::Interrupted => continue,
                Err(_) => return self.close(i),
            }
        }
        if written < data.len() {
            c.pending.extend_from_slice(&data[written..]);
            c.offset = 0;
        }
    }

    fn flush(&mut self, i: usize) {
        let c = &mut self.conns[i];
        let Some(stream) = c.stream.as_mut() else {
            return;
        };
        while c.offset < c.pending.len() {
            match stream.write(&c.pending[c.offset..]) {
                Ok(0) => return self.close(i),
                Ok(n) => c.offset += n,
                Err(e) if e.kind() == ErrorKind::WouldBlock => return,
                Err(e) if e.kind() == ErrorKind::Interrupted => continue,
                Err(_) => return self.close(i),
            }
        }
        c.pending.clear();
        c.offset = 0;
    }

    fn close(&mut self, i: usize) {
        let Some(mut stream) = self.conns[i].stream.take() else {
            return;
        };
        if let Some(poll) = self.poll.as_ref() {
            let _ = poll.registry().deregister(&mut stream);
        }
        drop(stream);
        self.lost(i);
    }

    fn attempt_done(&mut self, i: usize, success: bool) {
        self.conns[i].completed = true;
        self.completed += 1;
        self.connecting -= 1;
        if success {
            self.dial_stats.success += 1;
            if self.tpn {
                self.dial_stats
                    .latency
                    .push(now_ns() - self.conns[i].started);
            }
        } else {
            self.dial_stats.failed += 1;
        }
    }

    // A connection gone, whoever closed it: dial it again, or count what it was doing as failed.
    fn lost(&mut self, i: usize) {
        let c = &mut self.conns[i];
        c.stream = None;
        c.connected = false;
        c.pending.clear();
        c.offset = 0;
        c.generation += 1;
        if c.ready {
            c.ready = false;
            self.ctl.live.fetch_sub(1, Ordering::AcqRel);
        }
        if self.stopping {
            return;
        }
        if !c.completed {
            if c.attempt < self.retries {
                let event = DialEvent {
                    due: now_ns() + self.retry_interval,
                    conn: i,
                    generation: c.generation,
                    retry: true,
                };
                self.dial_events.push(event);
            } else {
                self.attempt_done(i, false);
            }
        } else if c.inflight {
            self.finish_echo(i, false);
        }
    }

    fn dial(&mut self, i: usize) {
        let c = &mut self.conns[i];
        c.attempt += 1;
        c.generation += 1;
        c.handshake.clear();
        c.parser = Parser::default();
        c.port = self.first_port + ((c.id + c.attempt as usize) % self.port_span) as u16;
        let addr = SocketAddr::new(self.ip.unwrap(), c.port);
        let mut stream = match TcpStream::connect(addr) {
            Ok(s) => s,
            Err(_) => return self.lost(i),
        };
        let registered = self.poll.as_ref().unwrap().registry().register(
            &mut stream,
            Token(i),
            Interest::READABLE | Interest::WRITABLE,
        );
        if registered.is_err() {
            return self.lost(i);
        }
        let c = &mut self.conns[i];
        c.stream = Some(stream);
        c.connected = false;
        let event = DialEvent {
            due: now_ns() + self.dial_timeout,
            conn: i,
            generation: c.generation,
            retry: false,
        };
        self.dial_events.push(event);
    }

    fn outstanding_push(&mut self, i: usize) {
        self.conns[i].prev = self.outstanding_tail;
        self.conns[i].next = NIL;
        if self.outstanding_tail == NIL {
            self.outstanding_head = i;
        } else {
            self.conns[self.outstanding_tail].next = i;
        }
        self.outstanding_tail = i;
        self.outstanding += 1;
    }

    fn outstanding_remove(&mut self, i: usize) {
        let (prev, next) = (self.conns[i].prev, self.conns[i].next);
        if prev == NIL {
            self.outstanding_head = next;
        } else {
            self.conns[prev].next = next;
        }
        if next == NIL {
            self.outstanding_tail = prev;
        } else {
            self.conns[next].prev = prev;
        }
        self.conns[i].prev = NIL;
        self.conns[i].next = NIL;
        self.outstanding -= 1;
    }

    fn finish_echo(&mut self, i: usize, valid: bool) {
        if !self.conns[i].inflight {
            return;
        }
        self.conns[i].inflight = false;
        self.outstanding_remove(i);
        if valid {
            self.echo_stats.success += 1;
            if self.tpn && self.stage == ECHO {
                self.echo_stats
                    .latency
                    .push(now_ns() - self.conns[i].echo_started);
            }
        } else {
            self.echo_stats.failed += 1;
        }
        if self.conns[i].ready {
            self.idle.push_back(i);
        }
        self.fill_echo();
    }

    fn stage_done(&self) -> bool {
        self.ctl.done.load(Ordering::Acquire) == self.stage
    }

    fn fill_echo(&mut self) {
        if self.filling || self.stage_done() || (self.stage != WARMUP && self.stage != ECHO) {
            return;
        }
        self.filling = true;
        while self.issued < self.target && self.outstanding < self.echo_concurrency {
            let Some(&i) = self.idle.front() else { break };
            if !self.conns[i].ready {
                self.idle.pop_front();
                continue;
            }
            if !self.shared.limiter.take(1) {
                break;
            }
            self.idle.pop_front();
            self.issued += 1;
            let payload = (self.rng.next() % self.shared.payloads.len() as u64) as usize;
            let c = &mut self.conns[i];
            c.payload = payload;
            c.echo_started = now_ns();
            c.inflight = true;
            self.outstanding_push(i);
            let shared = self.shared.clone();
            self.write(i, &shared.frames[payload]);
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

    fn receive(&mut self, i: usize, opcode: u8, data: &[u8]) {
        match opcode {
            OP_CLOSE => return self.close(i),
            OP_PING => {
                if self.conns[i].pending.len() > self.shared.batch_frame.len() + 1024 {
                    return self.close(i);
                }
                let pong = client_frame(data, OP_PONG, &mut self.rng);
                return self.write(i, &pong);
            }
            OP_PONG => return,
            _ => {}
        }
        if self.stage_done() {
            return;
        }
        if self.stage == ECHO || self.stage == WARMUP {
            if !self.conns[i].inflight {
                return self.close(i);
            }
            let valid = !self.check
                || (opcode == OP_BINARY && data == self.shared.payloads[self.conns[i].payload]);
            self.finish_echo(i, valid);
        } else if self.stage == RATE
            && (!self.check || (opcode == OP_BINARY && data == self.shared.payloads[0]))
        {
            self.conns[i].received += 1;
            self.rate_stats.received += 1;
            self.rate_stats.recv_bytes += data.len() as i64;
        }
    }

    fn consume(&mut self, i: usize, data: &[u8]) {
        if self.conns[i].ready {
            return self.parse(i, data);
        }
        let c = &mut self.conns[i];
        c.handshake.extend_from_slice(data);
        let Some(end) = c.handshake.windows(4).position(|w| w == b"\r\n\r\n") else {
            if c.handshake.len() > MAX_HANDSHAKE {
                self.close(i);
            }
            return;
        };
        if end > MAX_HANDSHAKE || !protocol::valid_upgrade(&c.handshake[..end + 4], &c.accept) {
            return self.close(i);
        }
        c.ready = true;
        c.generation += 1;
        let remaining = c.handshake.split_off(end + 4);
        c.handshake = Vec::new();
        self.ctl.live.fetch_add(1, Ordering::AcqRel);
        self.attempt_done(i, true);
        if !remaining.is_empty() {
            self.parse(i, &remaining);
        }
    }

    fn parse(&mut self, i: usize, data: &[u8]) {
        let generation = self.conns[i].generation;
        let mut parser = std::mem::take(&mut self.conns[i].parser);
        let max_payload = self.max_payload;
        let outcome = parser.consume(data, max_payload, &mut |opcode, payload| {
            self.receive(i, opcode, payload);
            self.conns[i].stream.is_some() && self.conns[i].generation == generation
        });
        if self.conns[i].stream.is_some() && self.conns[i].generation == generation {
            self.conns[i].parser = parser;
            if outcome == Outcome::Violation {
                self.close(i);
            }
        }
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
                .extend((0..self.conns.len()).filter(|&i| self.conns[i].ready));
            self.fill_echo();
        } else if next == RATE {
            self.rate_stats = Stats::default();
            self.rate_concurrency = self.ctl.rate_concurrency.load(Ordering::Acquire) as usize;
            let n = self
                .rate_concurrency
                .min(self.ctl.live.load(Ordering::Acquire).max(0) as usize);
            self.teams = vec![Vec::new(); n];
            if n > 0 {
                let mut at = 0;
                for i in 0..self.conns.len() {
                    if self.conns[i].ready {
                        self.conns[i].sent = 0;
                        self.conns[i].received = 0;
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
            if self.dial_stats.begin == 0 {
                self.dial_stats.begin = now;
            }
            while self.dial_events.peek().is_some_and(|e| e.due <= now) {
                let event = self.dial_events.pop().unwrap();
                let c = &self.conns[event.conn];
                if c.generation != event.generation || c.completed {
                    continue;
                }
                if event.retry {
                    self.dial(event.conn)
                } else {
                    self.close(event.conn)
                }
            }
            while self.next < self.count && self.connecting < self.ctl.dial_concurrency {
                let i = self.next;
                self.next += 1;
                self.connecting += 1;
                self.conns[i].started = now_ns();
                self.dial(i);
            }
            if self.completed == self.count {
                self.dial_events.clear();
                self.dial_stats.end = now_ns();
                *self.ctl.dial_stats.lock().unwrap() = std::mem::take(&mut self.dial_stats);
                self.ctl.done.store(DIAL, Ordering::Release);
            }
        } else if self.stage == WARMUP || self.stage == ECHO {
            while self.outstanding_head != NIL
                && now - self.conns[self.outstanding_head].echo_started >= self.io_timeout
            {
                self.close(self.outstanding_head);
            }
            self.fill_echo();
        } else if self.stage == RATE && !self.stage_done() {
            let batch = self.shared.batch;
            let interval = (1_000_000_000i64 * batch as i64 / self.send_rate as i64).max(1);
            let rate_end = self.shared.rate_end.load(Ordering::Acquire);
            let send_through = now.min(rate_end - 1);
            let shared = self.shared.clone();
            while self
                .team_events
                .peek()
                .is_some_and(|Reverse((due, _))| *due <= send_through)
            {
                let Reverse((due, team)) = self.team_events.pop().unwrap();
                let members = std::mem::take(&mut self.teams[team]);
                for &i in &members {
                    let c = &self.conns[i];
                    if !c.ready
                        || !c.pending.is_empty()
                        || c.sent - c.received + batch as i64 >= batch as i64 * 5
                    {
                        continue;
                    }
                    if !shared.limiter.take(batch) {
                        continue;
                    }
                    self.write(i, &shared.batch_frame);
                    if self.conns[i].ready {
                        self.conns[i].sent += batch as i64;
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
