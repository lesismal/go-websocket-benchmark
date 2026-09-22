// Echo WebSocket server built on uWebSockets (https://github.com/uNetworking/uWebSockets),
// the same C++ networking library used by benchcli-uwscpp. One uWS::App per hardware thread,
// all listening on every benchmark port via uSockets' default SO_REUSEPORT behavior.
//
// The /init and /ps routes replicate just enough of frameworks.HandleCommon (see
// frameworks/handlers.go) for the benchmark clients' resource reporting: /init starts a
// background CPU%/RSS sampler and returns the PID, /ps returns the samples as JSON. uSockets
// always enables TCP_NODELAY on accepted sockets and does not expose a way to turn it off, so
// -nodelay=false cannot be honored here (unlike the Go frameworks in this repo).
//
// # Task pool
//
// Like the Go servers (see taskpool/taskpool.go), this one runs its message callback off the
// reactor: it hands each connection's frames to a fixed pool of worker threads, leaving the
// loop threads with the reads, the parse and the writes.
//
// -taskpool takes the same names the Go servers take, and none of them names anything that
// can run under a C++ server, so what this one reads out of the name is where a server answers
// from: "default" and "inline" install no pool, which for uWS means echoing straight from the
// loop callback - what this server did before the pool existed - and every other mode hands
// the callback to a goroutine off the event loop, which this pool stands in for. See
// kPoolModes, the /taskpool route that serves the decision to the report's Pool column, and
// the same table from the other side in script/config.sh.
//
// The pool keeps one connection's messages in order the way the Go frameworks do: a
// connection carries a queue of frames and a drain flag, and a drain is submitted only when
// none is in flight, so the pool never holds two tasks for the same connection. What the pool
// takes as a task is the connection itself, as fib does.
//
// Two things the Go pools do not have to deal with:
//
//   - uWS is single threaded per loop, so a worker cannot write: it appends the echo to its
//     loop's Outbox, which the loop drains in order from a single deferred flush. Since one
//     connection has at most one drain in flight, its batches reach the outbox in the order
//     they were taken, and the loop writes them in that order.
//   - std::string_view from the message callback points into the loop's read buffer, which
//     uWS reuses as soon as the callback returns, so a frame's payload is copied on the way
//     into the queue. fnet's adapter copies for the same reason.
#include "App.h"

#include <atomic>
#include <chrono>
#include <condition_variable>
#include <csignal>
#include <cstdio>
#include <cstdlib>
#include <deque>
#include <fstream>
#include <memory>
#include <mutex>
#include <sstream>
#include <string>
#include <string_view>
#include <thread>
#include <utility>
#include <vector>

#include <unistd.h>
#ifdef __linux__
#include <sched.h>
#endif

namespace {

// Must match config.Ports[config.Uwebsockets] in config/config.go.
constexpr int kPortStart = 31001;
constexpr int kPortEnd = 31050;

// The CPUs this process may actually run on, which is what the thread counts have to be sized
// against: script/env.sh pins the server to about half the host's CPUs with taskset, and
// std::thread::hardware_concurrency() counts every online CPU instead of the ones in the
// affinity mask, so sizing by it built twice the loops this server had CPUs for. On its own
// that costs little - a loop thread with nothing to read sits in the poller - but it doubles
// again once a pool is added, and that is where it hurt: 20 threads on 5 CPUs echoed at
// 536k/s where 5 threads on 5 CPUs echoed at 848k/s (the measurement is in the README).
//
// The Go servers never had the problem: runtime.NumCPU reads the mask and GOMAXPROCS follows
// it. benchcli-uwscpp already picks its own thread count this way too; see availableCPUs in
// benchcli-uwscpp/main.cpp.
unsigned availableCPUs() {
#ifdef __linux__
    cpu_set_t set;
    if (sched_getaffinity(0, sizeof(set), &set) == 0 && CPU_COUNT(&set) > 0) {
        return unsigned(CPU_COUNT(&set));
    }
#endif
    unsigned online = std::thread::hardware_concurrency();
    return online > 0 ? online : 1;
}

bool parseBoolFlag(int argc, char **argv, const char *name, bool defaultValue) {
    std::string prefix = std::string("-") + name + "=";
    for (int i = 1; i < argc; ++i) {
        std::string arg(argv[i]);
        if (arg.rfind(prefix, 0) == 0) {
            std::string value = arg.substr(prefix.size());
            return value == "true" || value == "1" || value == "TRUE" || value == "True";
        }
    }
    return defaultValue;
}

std::string parseStringFlag(int argc, char **argv, const char *name, const char *defaultValue) {
    std::string prefix = std::string("-") + name + "=";
    for (int i = 1; i < argc; ++i) {
        std::string arg(argv[i]);
        if (arg.rfind(prefix, 0) == 0) {
            return arg.substr(prefix.size());
        }
    }
    return defaultValue;
}

// Returns defaultValue for a missing flag and for one whose value does not parse, matching
// the way the -tp* flags treat 0 as "the implementation's own default" rather than an error.
long parseIntFlag(int argc, char **argv, const char *name, long defaultValue) {
    std::string prefix = std::string("-") + name + "=";
    for (int i = 1; i < argc; ++i) {
        std::string arg(argv[i]);
        if (arg.rfind(prefix, 0) == 0) {
            const std::string value = arg.substr(prefix.size());
            char *end = nullptr;
            long parsed = std::strtol(value.c_str(), &end, 10);
            if (end == value.c_str() || *end != '\0') {
                std::fprintf(stderr, "uwebsockets: ignoring -%s=%s, want an integer\n", name,
                             value.c_str());
                return defaultValue;
            }
            return parsed;
        }
    }
    return defaultValue;
}

// ---------------------------------------------------------------------------
// Task pool
// ---------------------------------------------------------------------------

struct ConnState;

struct PerSocketData {
    // Created in the open handler, released when uWS destructs the user data after the close
    // handler. A worker holds a reference of its own, so the state outlives the socket.
    std::shared_ptr<ConnState> state;
};

using EchoWebSocket = uWS::WebSocket<false, true, PerSocketData>;

// One message taken off the loop. The payload is a copy: the view the message callback gets
// points into the loop's read buffer.
struct Frame {
    std::string payload;
    uWS::OpCode opCode;
};

struct Outbox;

// ConnState is a connection's queue, as the pool sees it.
struct ConnState {
    // Written in the open handler, before the state can be reached from a worker, and read
    // only on the loop's own thread afterwards.
    EchoWebSocket *ws = nullptr;
    uWS::Loop *loop = nullptr;
    Outbox *outbox = nullptr;  // the loop's, for the worker to hand finished batches back

    // Cleared in the close handler. A worker reads it to drop work early, but the read that
    // guards ws itself is the one inside the deferred send: that runs on the same thread as
    // the close handler, so it cannot catch the socket halfway gone.
    std::atomic<bool> open{true};

    std::mutex mutex;
    std::vector<Frame> queue;  // guarded by mutex
    bool draining = false;     // guarded by mutex: a drain is queued or running
};

void drainConnection(const std::shared_ptr<ConnState> &state);

// TaskPool is a fixed population of worker threads over sharded queues, the shape of the uws
// pool in taskpool/pool_uws.go: workers are started up front, each shard has its own queue and
// lock so that submissions from different loops rarely meet, and a submission that finds its
// shard full is refused rather than waited on. What it queues is a connection, not a closure,
// so a submission costs a reference count rather than an allocation.
class TaskPool {
public:
    TaskPool(unsigned workerCount, unsigned pending)
        : workerCount_(workerCount), pending_(pending) {
        shards_.reserve(workerCount_);
        for (unsigned index = 0; index < workerCount_; ++index) {
            auto shard = std::make_unique<Shard>();
            // Split the queue over the shards, giving the remainder to the lowest-numbered
            // ones, so that -tpqueue means the same total it means for the Go pools.
            shard->capacity = pending_ / workerCount_ + (index < pending_ % workerCount_ ? 1 : 0);
            if (shard->capacity == 0) shard->capacity = 1;
            shards_.push_back(std::move(shard));
        }
        workers_.reserve(workerCount_);
        for (unsigned index = 0; index < workerCount_; ++index) {
            workers_.emplace_back([this, index] { run(*shards_[index]); });
        }
    }

    ~TaskPool() { stop(); }

    TaskPool(const TaskPool &) = delete;
    TaskPool &operator=(const TaskPool &) = delete;

    // go queues state on one of the shards and reports whether the pool took it. A refusal
    // leaves the connection's queue untouched for the caller to drain itself.
    bool go(std::shared_ptr<ConnState> state) {
        Shard &shard = *shards_[nextShard()];
        {
            std::lock_guard<std::mutex> lock(shard.mutex);
            if (shard.stopped || shard.queue.size() >= shard.capacity) {
                return false;
            }
            shard.queue.push_back(std::move(state));
        }
        shard.ready.notify_one();
        return true;
    }

    unsigned workers() const { return workerCount_; }
    unsigned pending() const { return pending_; }

    void stop() {
        for (auto &shard : shards_) {
            std::lock_guard<std::mutex> lock(shard->mutex);
            shard->stopped = true;
            shard->ready.notify_all();
        }
        for (auto &worker : workers_) {
            if (worker.joinable()) worker.join();
        }
    }

private:
    struct Shard {
        std::mutex mutex;
        std::condition_variable ready;
        std::deque<std::shared_ptr<ConnState>> queue;
        size_t capacity = 0;
        bool stopped = false;
    };

    // Round-robin, but from a per-thread cursor rather than a shared counter: every loop
    // thread submits at the rate it reads, and one atomic between them all would be a
    // contended cacheline for nothing. There is one pool, so one cursor per thread is enough.
    unsigned nextShard() {
        static std::atomic<unsigned> seed{0};
        thread_local unsigned cursor = seed.fetch_add(1, std::memory_order_relaxed);
        return cursor++ % workerCount_;
    }

    void run(Shard &shard) {
        for (;;) {
            std::shared_ptr<ConnState> state;
            {
                std::unique_lock<std::mutex> lock(shard.mutex);
                shard.ready.wait(lock, [&shard] { return shard.stopped || !shard.queue.empty(); });
                if (shard.queue.empty()) return;  // stopped and drained
                state = std::move(shard.queue.front());
                shard.queue.pop_front();
            }
            drainConnection(state);
        }
    }

    const unsigned workerCount_;
    const unsigned pending_;
    std::vector<std::unique_ptr<Shard>> shards_;
    std::vector<std::thread> workers_;
};

// Null for the modes that answer in the loop, which is the whole of the mode: the handlers
// branch on it once, at registration.
std::unique_ptr<TaskPool> g_pool;

// What /taskpool answers, for the Pool column of the reports: the mode this server was given
// and what it did with it, since the same name means the pool here and a goroutine pool on the
// Go side. Written once, before the loops start. See config.GetFrameworkTaskPool.
std::string g_taskPoolReport = "-";

std::atomic<bool> g_warnedRefusal{false};

void warnRefusalOnce() {
    bool expected = false;
    if (g_warnedRefusal.compare_exchange_strong(expected, true)) {
        std::fprintf(stderr,
                     "uwebsockets: taskpool queue full, echoing on the event loop instead; "
                     "raise -tpqueue if this is not what you meant to measure\n");
    }
}

// Sends one connection's batch. Must run on that connection's loop thread.
void sendBatch(EchoWebSocket *ws, std::vector<Frame> &frames) {
    if (frames.size() == 1) {
        ws->send(frames[0].payload, frames[0].opCode, false);
        return;
    }
    // Corking writes the batch out in one go. uWS does this for the sends a message callback
    // makes; a deferred send is outside that, so it has to cork for itself.
    ws->cork([ws, &frames] {
        for (Frame &frame : frames) {
            ws->send(frame.payload, frame.opCode, false);
        }
    });
}

// Outbox is one loop's finished work: the batches its workers have echoed and want written.
//
// A worker cannot write - uWS is single threaded per loop - so the batch has to go back through
// uWS::Loop::defer, and deferring each one separately made every connection in a burst pay its
// own defer: a lock on the loop's defer queue, a heap allocation for the closure, an eventfd
// write and a loop wakeup, for work that is otherwise a memcpy. Here the workers append to one
// queue per loop and only the push that finds it unarmed defers anything, so a burst across a
// thousand connections costs one wakeup instead of a thousand. The deferred closure captures
// one pointer, which fits MoveOnlyFunction's small-object buffer, so arming allocates nothing
// either.
//
// One outbox per loop, owned by main and outliving every thread that holds its address.
struct Outbox {
    struct Pending {
        std::shared_ptr<ConnState> state;
        std::vector<Frame> frames;
    };

    uWS::Loop *loop = nullptr;

    std::mutex mutex;
    std::vector<Pending> pending;  // guarded by mutex, in the order the batches were taken
    bool armed = false;            // guarded by mutex: a flush is deferred and has not run yet

    // Loop thread only. Kept between flushes so the buffers it swaps out stay allocated.
    std::vector<Pending> flushing;
};

// The outbox of the loop this thread runs, for the handlers to record on a new connection.
// Set once at the top of runWorker.
thread_local Outbox *tlsOutbox = nullptr;

// Writes everything the workers have finished, in the order they finished it. Runs on the
// loop's own thread, from the defer the arming push queued.
void flushOutbox(Outbox *outbox) {
    {
        std::lock_guard<std::mutex> lock(outbox->mutex);
        outbox->flushing.swap(outbox->pending);
        // Disarmed before the writes rather than after: a worker that appends while this
        // flush is sending has to be able to arm a defer of its own, and that defer runs
        // after this one returns, which is what keeps the two in order.
        outbox->armed = false;
    }
    for (Outbox::Pending &entry : outbox->flushing) {
        if (!entry.state->open.load(std::memory_order_acquire)) continue;
        sendBatch(entry.state->ws, entry.frames);
    }
    outbox->flushing.clear();
}

// Hands one connection's batch to its loop. Called from a worker, and from the loop thread
// itself for a drain the pool refused.
void handBack(const std::shared_ptr<ConnState> &state, std::vector<Frame> &&frames) {
    Outbox *outbox = state->outbox;
    bool arm = false;
    {
        std::lock_guard<std::mutex> lock(outbox->mutex);
        outbox->pending.push_back(Outbox::Pending{state, std::move(frames)});
        if (!outbox->armed) {
            outbox->armed = true;
            arm = true;
        }
    }
    if (arm) {
        outbox->loop->defer([outbox] { flushOutbox(outbox); });
    }
}

// Empties a connection's queue, handing each batch back to its loop. Runs on a worker, and on
// the loop thread itself for the drains the pool refuses. Returns once the queue is empty and
// the drain flag is down, which is the point after which the next message submits a drain of
// its own.
//
// Every send goes through the outbox, including the ones this function makes while already on
// the loop thread. That is what orders the connection: the drain flag goes down as soon as the
// queue is empty, which is before the loop has written the batches handed to it, so a batch
// that sent directly could overtake one still waiting in the outbox. Going through it in every
// case leaves one FIFO as the only order there is - the outbox for a connection's batches, and
// defer's own FIFO for the flushes.
void drainConnection(const std::shared_ptr<ConnState> &state) {
    for (;;) {
        std::vector<Frame> batch;
        {
            std::lock_guard<std::mutex> lock(state->mutex);
            if (state->queue.empty()) {
                state->draining = false;
                return;
            }
            batch.swap(state->queue);
        }
        if (!state->open.load(std::memory_order_acquire)) {
            std::lock_guard<std::mutex> lock(state->mutex);
            state->queue.clear();
            state->draining = false;
            return;
        }
        handBack(state, std::move(batch));
    }
}

// Queues one message and hands the connection to the pool if no drain is in flight. Runs on
// the loop thread that owns the connection, which is its only producer.
void queueFrame(const std::shared_ptr<ConnState> &state, std::string_view message,
                uWS::OpCode opCode) {
    bool handOver = false;
    {
        std::lock_guard<std::mutex> lock(state->mutex);
        state->queue.push_back(Frame{std::string(message), opCode});
        if (!state->draining) {
            state->draining = true;
            handOver = true;
        }
    }
    // A drain already in flight will pick this frame up, in order, before it lowers the flag.
    if (!handOver) return;
    if (g_pool->go(state)) return;
    // Refused: the caller runs the task itself, which is what the Go adapters do with a task
    // their pool declines (taskpool.runOrCall). The drain still defers its sends, so a
    // refusal cannot reorder a connection either.
    warnRefusalOnce();
    drainConnection(state);
}

// Minimal stand-in for gopsutil's process.Process.Percent/MemoryInfo, sampled at the interval
// requested by /init. Mirrors github.com/lesismal/perf's PSCounter JSON shape closely enough
// for benchcli-uwscpp's resourceStats() and benchcli-go's config.GetFrameworkPsInfo, which only
// read the "cpu" and "mem"[].rss fields.
class PsSampler {
public:
    void start(std::chrono::nanoseconds interval) {
        bool expected = false;
        if (!started_.compare_exchange_strong(expected, true)) {
            return;
        }
        if (interval <= std::chrono::nanoseconds::zero()) {
            interval = std::chrono::seconds(1);
        }
        std::thread([this, interval] { run(interval); }).detach();
    }

    std::string psJSON() {
        std::lock_guard<std::mutex> lock(mutex_);
        std::ostringstream out;
        out << "{\"cpu\":[";
        for (size_t i = 0; i < cpu_.size(); ++i) {
            if (i) out << ',';
            out << cpu_[i];
        }
        out << "],\"mem\":[";
        for (size_t i = 0; i < mem_.size(); ++i) {
            if (i) out << ',';
            out << "{\"rss\":" << mem_[i] << '}';
        }
        out << "]}";
        return out.str();
    }

private:
    void run(std::chrono::nanoseconds interval) {
        unsigned long long lastUtime = 0, lastStime = 0;
        readProcStat(lastUtime, lastStime);
        auto lastTick = std::chrono::steady_clock::now();
        for (;;) {
            std::this_thread::sleep_for(interval);
            unsigned long long utime = 0, stime = 0;
            auto now = std::chrono::steady_clock::now();
            double wallSeconds = std::chrono::duration<double>(now - lastTick).count();
            double percent = 0.0;
            if (readProcStat(utime, stime) && wallSeconds > 0) {
                double procSeconds = double((utime - lastUtime) + (stime - lastStime)) / double(clockTicksPerSec_);
                percent = (procSeconds / wallSeconds) * 100.0;
            }
            lastUtime = utime;
            lastStime = stime;
            lastTick = now;
            unsigned long long rss = readRssBytes();
            std::lock_guard<std::mutex> lock(mutex_);
            cpu_.push_back(percent);
            mem_.push_back(rss);
        }
    }

    static bool readProcStat(unsigned long long &utime, unsigned long long &stime) {
        std::ifstream in("/proc/self/stat");
        if (!in) return false;
        std::string line;
        std::getline(in, line);
        // The comm field (2nd, parenthesized) may itself contain spaces, so skip past its
        // closing paren before splitting the remaining whitespace-separated fields.
        auto rparen = line.rfind(')');
        if (rparen == std::string::npos || rparen + 2 >= line.size()) return false;
        std::istringstream rest(line.substr(rparen + 2));
        std::vector<std::string> fields;
        std::string field;
        while (rest >> field) fields.push_back(field);
        // fields[0] is state (field 3 overall); utime/stime are fields 14/15 overall, i.e.
        // indices 11/12 here.
        if (fields.size() < 13) return false;
        utime = std::strtoull(fields[11].c_str(), nullptr, 10);
        stime = std::strtoull(fields[12].c_str(), nullptr, 10);
        return true;
    }

    static unsigned long long readRssBytes() {
        std::ifstream in("/proc/self/status");
        std::string line;
        while (std::getline(in, line)) {
            if (line.rfind("VmRSS:", 0) == 0) {
                unsigned long long kb = std::strtoull(line.c_str() + 6, nullptr, 10);
                return kb * 1024;
            }
        }
        return 0;
    }

    const long clockTicksPerSec_ = sysconf(_SC_CLK_TCK);
    std::atomic<bool> started_{false};
    std::mutex mutex_;
    std::vector<double> cpu_;
    std::vector<unsigned long long> mem_;
};

PsSampler g_psSampler;

// Body is the JSON produced by encoding/json for config.InitArgs, e.g. {"PsInterval":200000000}.
// Hand-rolled instead of pulling in a JSON library for a single integer field.
long long parsePsIntervalNanos(const std::string &body) {
    auto pos = body.find("PsInterval");
    if (pos == std::string::npos) return 0;
    pos = body.find(':', pos);
    if (pos == std::string::npos) return 0;
    ++pos;
    while (pos < body.size() && (body[pos] == ' ' || body[pos] == '"')) ++pos;
    long long value = 0;
    bool any = false;
    while (pos < body.size() && body[pos] >= '0' && body[pos] <= '9') {
        value = value * 10 + (body[pos] - '0');
        ++pos;
        any = true;
    }
    return any ? value : 0;
}


void runWorker(const std::vector<int> &ports, Outbox *outbox) {
    uWS::App app;
    if (outbox != nullptr) {
        outbox->loop = uWS::Loop::get();
        tlsOutbox = outbox;
    }

    uWS::App::WebSocketBehavior<PerSocketData> behavior{
        .compression = uWS::DISABLED,
        .maxPayloadLength = 64 * 1024 * 1024,
        .idleTimeout = 0,
        .maxBackpressure = 64 * 1024 * 1024,
        .closeOnBackpressureLimit = false,
        .resetIdleTimeoutOnSend = false,
        .sendPingsAutomatically = false,
        .upgrade = nullptr,
        .open = [](auto * /*ws*/) {},
        .message = [](auto *ws, std::string_view message, uWS::OpCode opCode) {
            ws->send(message, opCode, false);
        },
        .dropped = [](auto * /*ws*/, std::string_view /*message*/, uWS::OpCode /*opCode*/) {},
        .drain = [](auto * /*ws*/) {},
        .ping = [](auto * /*ws*/, std::string_view) {},
        .pong = [](auto * /*ws*/, std::string_view) {},
        .close = [](auto * /*ws*/, int /*code*/, std::string_view /*message*/) {},
    };

    if (g_pool) {
        behavior.open = [](auto *ws) {
            auto state = std::make_shared<ConnState>();
            state->ws = ws;
            state->loop = uWS::Loop::get();
            state->outbox = tlsOutbox;
            ws->getUserData()->state = std::move(state);
        };
        behavior.message = [](auto *ws, std::string_view message, uWS::OpCode opCode) {
            queueFrame(ws->getUserData()->state, message, opCode);
        };
        behavior.close = [](auto *ws, int /*code*/, std::string_view /*message*/) {
            const std::shared_ptr<ConnState> &state = ws->getUserData()->state;
            if (!state) return;
            state->open.store(false, std::memory_order_release);
            // Drop what is still queued so that a drain already in flight stops here rather
            // than working through frames for a socket that is gone.
            std::lock_guard<std::mutex> lock(state->mutex);
            state->queue.clear();
        };
    }

    app.ws<PerSocketData>("/*", std::move(behavior));

    app.post("/init", [](auto *res, auto * /*req*/) {
        auto body = std::make_shared<std::string>();
        res->onAborted([] {});
        res->onData([res, body](std::string_view chunk, bool isLast) {
            body->append(chunk);
            if (isLast) {
                g_psSampler.start(std::chrono::nanoseconds(parsePsIntervalNanos(*body)));
                res->end(std::to_string(getpid()));
            }
        });
    });

    app.get("/ps", [](auto *res, auto * /*req*/) {
        res->writeHeader("Content-Type", "application/json");
        res->end(g_psSampler.psJSON());
    });

    app.get("/taskpool", [](auto *res, auto * /*req*/) { res->end(g_taskPoolReport); });

    for (int port : ports) {
        app.listen(port, [port](auto *socket) {
            if (!socket) {
                std::fprintf(stderr, "uwebsockets: failed to listen on port %d\n", port);
            }
        });
    }

    app.run();
}

// The queue the pool's shards add up to, as taskpool's uws pool sizes it. A connection is in
// a queue at most once, so this is a ceiling on connections waiting for a worker rather than
// on messages; -tpqueue raises it.
constexpr unsigned kDefaultPending = 65536;

// The -taskpool modes, as taskpool.Names lists them, and what each says about where the Go
// servers answer a message from. That is the whole of the decision this server has to make:
// none of the Go pools can run under a C++ server, but every mode either hands the callback to
// a goroutine off the event loop, which this server's own pool is the stand-in for, or leaves
// it on the goroutine that read the frame, which is this server's loop callback.
struct PoolMode {
    const char *name;
    bool offLoop;
    const char *goSide;
};

constexpr PoolMode kPoolModes[] = {
    // Not a pool: each framework keeps the scheduling it ships with, and this server's own is
    // the event loop. It is the one mode where the Go servers do not answer from the same
    // place as each other either - greatws_event answers in its poller under it, the rest run
    // the pool they ship with.
    {"default", false, "no pool installed: each framework keeps the scheduling it ships with"},
    {"inline", false, "no pool: the callback runs on the I/O goroutine that read the frame"},
    {"go", true, "one goroutine per task"},
    {"fib_adaptive", true, "fib's taskpool, adaptive mode"},
    {"fib_cond", true, "fib's taskpool, cond mode"},
    {"fib_elastic", true, "fib's taskpool, elastic mode"},
    {"nbio", true, "nbio's taskpool"},
    {"fnet", true, "fnet.WorkerPool"},
    {"greatws", true, "greatws's stream2 business pool"},
    {"uws", true, "the sharded channel executor uws runs on"},
    // This server's own name for what it runs, for asking directly rather than through what a
    // Go mode implies. No Go side to describe, hence the empty note.
    {"pool", true, nullptr},
};

const PoolMode *findPoolMode(const std::string &name) {
    for (const PoolMode &mode : kPoolModes) {
        if (name == mode.name) return &mode;
    }
    return nullptr;
}

const PoolMode *resolvePoolMode(int argc, char **argv) {
    const std::string name = parseStringFlag(argc, argv, "taskpool", "fib_adaptive");
    const PoolMode *mode = findPoolMode(name);
    if (mode != nullptr) return mode;
    // taskpool.FromFlags exits on a name that names no pool, since a run that silently
    // ignored the flag would be reported under the wrong one. Same here.
    std::fprintf(stderr, "uwebsockets: unknown -taskpool=%s, want one of:", name.c_str());
    for (const PoolMode &known : kPoolModes) {
        std::fprintf(stderr, " %s", known.name);
    }
    std::fprintf(stderr, "\n");
    std::exit(1);
}

// How the process splits its threads.
//
// uWS runs one event loop per thread and is not thread safe within a loop, so a loop here is
// always single threaded; the parallelism is in how many there are, one uWS::App each, all
// listening on every port through uSockets' SO_REUSEPORT. Without a pool that is one loop per
// core, which is what this server has always run.
//
// With a pool it cannot stay one loop per core. The workers are OS threads on top of the
// loops, so that arrangement runs twice as many threads as there are CPUs and pays context
// switches for work the loops were already contending for. The Go servers do not have the
// problem: their pool's goroutines multiplex onto the same GOMAXPROCS threads their pollers
// run on, so a pool there does not widen the process. So the default here keeps loops +
// workers at the CPU count, and gives the loops the larger share.
//
// Measured on 5 CPUs (server and client pinned to disjoint sets, as script/env.sh pins them),
// echoing a 1KiB payload over 2000 connections, TPS averaged over three runs:
//
//     loops=4 workers=1   848k   <- kCoresPerWorker, and the best of these
//     loops=5 workers=1   802k      one thread more than there are CPUs
//     loops=4 workers=2   774k
//     loops=3 workers=1   650k
//     loops=3 workers=2   517k
//
// Two things in that: a loop is worth more than a worker, since the loop side does the poll,
// the read, the frame parse and the write while a worker only copies a payload and defers it
// back; and threads beyond the CPU count cost more than they add. Hence one worker per
// kCoresPerWorker cores and the rest loops, with -loops and -tpmax to override either.
struct ThreadPlan {
    unsigned loops;
    unsigned workers;  // 0 when the echo runs in the loop
};

// One worker per this many cores, when neither -loops nor -tpmax says otherwise. Fewer workers
// measured better at every loop count tried, but a pool of one thread for every loop in the
// process would be a poor default for any callback heavier than an echo, so a quarter of the
// CPUs is where this stops.
constexpr unsigned kCoresPerWorker = 4;

ThreadPlan planThreads(int argc, char **argv, const PoolMode &mode, unsigned cores) {
    const long loopFlag = parseIntFlag(argc, argv, "loops", 0);
    const long workerFlag = parseIntFlag(argc, argv, "tpmax", 0);

    ThreadPlan plan{cores, 0};
    if (!mode.offLoop) {
        // No pool to leave room for, so -loops is the whole of it.
        if (loopFlag > 0) plan.loops = unsigned(loopFlag);
        if (plan.loops == 0) plan.loops = 1;
        return plan;
    }
    // Whichever of the two the command line fixes, the other takes the rest of the cores, so
    // that a run which sizes one of them by hand does not end up oversubscribed by accident.
    if (loopFlag > 0) {
        plan.loops = unsigned(loopFlag);
        plan.workers = workerFlag > 0 ? unsigned(workerFlag)
                                      : (cores > plan.loops ? cores - plan.loops : 1);
    } else if (workerFlag > 0) {
        plan.workers = unsigned(workerFlag);
        plan.loops = cores > plan.workers ? cores - plan.workers : 1;
    } else {
        plan.workers = cores / kCoresPerWorker;
        if (plan.workers == 0) plan.workers = 1;
        plan.loops = cores > plan.workers ? cores - plan.workers : 1;
    }
    if (plan.loops == 0) plan.loops = 1;
    if (plan.workers == 0) plan.workers = 1;
    return plan;
}

// Builds the pool -taskpool asks for, or nothing for a mode that answers in the loop, and logs
// which it is so that a report can be read back against the scheduling that produced it.
void installTaskPool(int argc, char **argv, const PoolMode &mode, const ThreadPlan &plan) {
    const long minWorkers = parseIntFlag(argc, argv, "tpmin", 0);
    const long queueSize = parseIntFlag(argc, argv, "tpqueue", 0);

    g_taskPoolReport = std::string(mode.name) + (mode.offLoop ? "(pool)" : "(loop)");

    if (!mode.offLoop) {
        std::fprintf(stderr,
                     "uwebsockets taskpool: %s -> event loop (Go side: %s; here the echo is "
                     "written from the loop callback, which is uWS's own scheduling) "
                     "loops=%u cpus=%u\n",
                     mode.name, mode.goSide, plan.loops, availableCPUs());
        return;
    }

    unsigned pending = queueSize > 0 ? unsigned(queueSize) : kDefaultPending;
    g_pool = std::make_unique<TaskPool>(plan.workers, pending);
    std::string reason = "this server's thread pool, named directly";
    if (mode.goSide != nullptr) {
        reason = std::string("Go side: ") + mode.goSide +
                 ", off the event loop; here a thread pool stands in for it";
    }
    std::fprintf(stderr,
                 "uwebsockets taskpool: %s -> pool (%s) min=%ld(ignored) queue=%ld workers=%u "
                 "shards=%u pending=%u rejects=true loops=%u cpus=%u\n",
                 mode.name, reason.c_str(), minWorkers, queueSize, g_pool->workers(),
                 g_pool->workers(), g_pool->pending(), plan.loops, availableCPUs());
}

}  // namespace

int main(int argc, char **argv) {
    std::signal(SIGINT, [](int) { std::_Exit(0); });

    bool nodelay = parseBoolFlag(argc, argv, "nodelay", true);
    if (!nodelay) {
        std::fprintf(stderr,
                      "uwebsockets: uSockets always enables TCP_NODELAY and does not expose a "
                      "way to disable it; -nodelay=false is ignored\n");
    }

    std::vector<int> ports;
    ports.reserve(kPortEnd - kPortStart + 1);
    for (int port = kPortStart; port <= kPortEnd; ++port) ports.push_back(port);

    const unsigned cores = availableCPUs();

    const PoolMode *mode = resolvePoolMode(argc, argv);
    const ThreadPlan plan = planThreads(argc, argv, *mode, cores);
    std::fprintf(stderr,
                 "uwebsockets benchmark config: loops=%u workers=%u threads=%u cpus=%u "
                 "hardware_concurrency=%u ports=%d-%d\n",
                 plan.loops, plan.workers, plan.loops + plan.workers, cores,
                 std::thread::hardware_concurrency(), kPortStart, kPortEnd);

    installTaskPool(argc, argv, *mode, plan);

    // One outbox per loop, and they outlive the threads: a worker holds the address of the
    // outbox belonging to the loop its connection came from. Only the pool modes need them.
    std::vector<std::unique_ptr<Outbox>> outboxes;
    if (g_pool) {
        outboxes.reserve(plan.loops);
        for (unsigned i = 0; i < plan.loops; ++i) {
            outboxes.push_back(std::make_unique<Outbox>());
        }
    }

    std::vector<std::thread> workers;
    workers.reserve(plan.loops);
    for (unsigned i = 0; i < plan.loops; ++i) {
        workers.emplace_back(runWorker, std::cref(ports), g_pool ? outboxes[i].get() : nullptr);
    }
    for (auto &worker : workers) worker.join();
    g_pool.reset();
    return 0;
}
