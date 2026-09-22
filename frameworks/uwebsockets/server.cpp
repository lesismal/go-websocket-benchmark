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
//   - uWS is single threaded per loop, so a worker cannot write: it hands the echo back
//     through uWS::Loop::defer, whose queue is FIFO. Since one connection has at most one
//     drain in flight, its batches are deferred in the order they were taken, and the loop
//     sends them in that order.
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

namespace {

// Must match config.Ports[config.Uwebsockets] in config/config.go.
constexpr int kPortStart = 31001;
constexpr int kPortEnd = 31050;

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

// ConnState is a connection's queue, as the pool sees it.
struct ConnState {
    // Written in the open handler, before the state can be reached from a worker, and read
    // only on the loop's own thread afterwards.
    EchoWebSocket *ws = nullptr;
    uWS::Loop *loop = nullptr;

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

// Empties a connection's queue, handing each batch back to its loop. Runs on a worker, and on
// the loop thread itself for the drains the pool refuses. Returns once the queue is empty and
// the drain flag is down, which is the point after which the next message submits a drain of
// its own.
//
// Every send goes through defer, including the ones this function makes while already on the
// loop thread. That is what orders the connection: the drain flag goes down as soon as the
// queue is empty, which is before the loop has run the sends deferred for it, so a batch that
// sent directly could overtake one still sitting in the defer queue. Going through the queue
// in every case leaves defer's FIFO order the only order there is.
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
        state->loop->defer([state, batch = std::move(batch)]() mutable {
            if (!state->open.load(std::memory_order_acquire)) return;
            sendBatch(state->ws, batch);
        });
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


void runWorker(const std::vector<int> &ports) {
    uWS::App app;

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

// Builds the pool -taskpool asks for, or nothing for a mode that answers in the loop, and logs
// which it is so that a report can be read back against the scheduling that produced it.
void installTaskPool(int argc, char **argv, unsigned loops) {
    const std::string name = parseStringFlag(argc, argv, "taskpool", "fib_adaptive");
    const long minWorkers = parseIntFlag(argc, argv, "tpmin", 0);
    const long maxWorkers = parseIntFlag(argc, argv, "tpmax", 0);
    const long queueSize = parseIntFlag(argc, argv, "tpqueue", 0);

    const PoolMode *mode = findPoolMode(name);
    if (mode == nullptr) {
        // taskpool.FromFlags exits on a name that names no pool, since a run that silently
        // ignored the flag would be reported under the wrong one. Same here.
        std::fprintf(stderr, "uwebsockets: unknown -taskpool=%s, want one of:", name.c_str());
        for (const PoolMode &known : kPoolModes) {
            std::fprintf(stderr, " %s", known.name);
        }
        std::fprintf(stderr, "\n");
        std::exit(1);
    }

    g_taskPoolReport = std::string(mode->name) + (mode->offLoop ? "(pool)" : "(loop)");

    if (!mode->offLoop) {
        std::fprintf(stderr,
                     "uwebsockets taskpool: %s -> event loop (Go side: %s; here the echo is "
                     "written from the loop callback, which is uWS's own scheduling) "
                     "loops=%u\n",
                     mode->name, mode->goSide, loops);
        return;
    }

    // One worker per core: the callback is an echo, so a worker never blocks and more threads
    // than cores would only add context switches to work the loops are already contending
    // for. Wider or narrower is -tpmax. Note that this is a pool on top of the loop threads,
    // so the process runs 2x hardware_concurrency threads where the in-loop modes run 1x.
    unsigned workers = maxWorkers > 0 ? unsigned(maxWorkers) : loops;
    if (workers == 0) workers = 1;
    unsigned pending = queueSize > 0 ? unsigned(queueSize) : kDefaultPending;

    g_pool = std::make_unique<TaskPool>(workers, pending);
    std::string reason = "this server's thread pool, named directly";
    if (mode->goSide != nullptr) {
        reason = std::string("Go side: ") + mode->goSide +
                 ", off the event loop; here a thread pool stands in for it";
    }
    std::fprintf(stderr,
                 "uwebsockets taskpool: %s -> pool (%s) min=%ld(ignored) max=%ld queue=%ld "
                 "workers=%u shards=%u pending=%u rejects=true loops=%u "
                 "hardware_concurrency=%u\n",
                 mode->name, reason.c_str(), minWorkers, maxWorkers, queueSize,
                 g_pool->workers(), g_pool->workers(), g_pool->pending(), loops,
                 std::thread::hardware_concurrency());
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

    unsigned int threadCount = std::thread::hardware_concurrency();
    if (threadCount == 0) threadCount = 1;
    std::fprintf(stderr, "uwebsockets benchmark config: threads=%u ports=%d-%d\n", threadCount,
                 kPortStart, kPortEnd);

    installTaskPool(argc, argv, threadCount);

    std::vector<std::thread> workers;
    workers.reserve(threadCount);
    for (unsigned int i = 0; i < threadCount; ++i) {
        workers.emplace_back(runWorker, std::cref(ports));
    }
    for (auto &worker : workers) worker.join();
    g_pool.reset();
    return 0;
}
