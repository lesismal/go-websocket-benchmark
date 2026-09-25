// The logic thread pool: the workers the message callback runs on when it runs off the event
// loops, built the way the uwebsockets server's logic pool and taskpool's uws pool are - worker
// threads started up front, each with a queue of its own, and a connection always given to the
// same one.
//
// That last part is what keeps one connection's messages in order. A connection hands its
// batches to one shard, whose single worker takes them in the order they were queued and sends
// each reply back down the connection's own channel, which the connection's loop reads in order
// too. Nothing needs a drain flag the way the Go pools' adapters do: the shard's queue is the
// connection's queue, since a worker never runs two batches at once.
//
// The replies go back through a channel rather than being written by the worker because a
// connection belongs to the loop that registered its socket: the worker only produces the
// messages, and the loop writes them, as uWS's workers defer their sends back to the loop.
use std::sync::OnceLock;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::mpsc;

use tokio::sync::mpsc::UnboundedSender;
use tokio_tungstenite::tungstenite::Message;

// One batch of a connection's messages, and where the answer goes.
pub struct Job {
    pub messages: Vec<Message>,
    pub reply: UnboundedSender<Vec<Message>>,
}

static SHARDS: OnceLock<Vec<mpsc::Sender<Job>>> = OnceLock::new();
static NEXT: AtomicUsize = AtomicUsize::new(0);

// Starts the workers. Called once, before the loops accept anything.
pub fn start(workers: usize) {
    let shards = (0..workers.max(1))
        .map(|i| {
            let (tx, rx) = mpsc::channel::<Job>();
            std::thread::Builder::new()
                .name(format!("logicpool-{i}"))
                .spawn(move || run(rx))
                .expect("starting a logic pool worker");
            tx
        })
        .collect();
    if SHARDS.set(shards).is_err() {
        panic!("logic pool started twice");
    }
}

// The queue a new connection is to hand its batches to, round-robin over the shards.
pub fn shard() -> mpsc::Sender<Job> {
    let shards = SHARDS.get().expect("logic pool not started");
    shards[NEXT.fetch_add(1, Ordering::Relaxed) % shards.len()].clone()
}

fn run(jobs: mpsc::Receiver<Job>) {
    for job in jobs {
        // The logic: an echo answers with what it was given. A connection that has gone has
        // dropped its receiver, and its answer goes with it.
        let _ = job.reply.send(job.messages);
    }
}
