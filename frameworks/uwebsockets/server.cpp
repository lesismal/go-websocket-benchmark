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
// # Logic thread pool
//
// -logicpool=false, the default, echoes straight from the loop callback - uWS's own
// scheduling - and is what the uwebsockets entry runs. -logicpool=true runs the message
// callback off the reactor instead: it hands each connection's frames to a fixed pool of worker
// threads, leaving the loop threads with the reads, the parse and the writes. Both listen on
// the same ports.
//
// This server's pool is its own setting, not the Go servers' goroutine pool: it does not read
// -taskpool or the -tp* flags, so BENCH_TASKPOOL and its sizing in script/config.sh leave it
// alone, and script/servers.sh never turns it on. The /taskpool route still answers, for the
// report's Pool: "logicpool" with the pool on, "inline" without it. The pool is sized by its
// own flags too: -workers or -workerspercpu for the threads (see planThreads) and -poolqueue
// for the queue.
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
#include <cmath>
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
constexpr int kPortStart = 17401;
constexpr int kPortEnd = 17450;

// The CPUs this process may actually run on, which is what the thread counts have to be sized
// against: script/env.sh pins the server to about half the host's CPUs with taskset, and
// std::thread::hardware_concurrency() counts every online CPU instead of the ones in the
// affinity mask, so sizing by it built twice the loops this server had CPUs for. On its own
// that costs little - a loop thread with nothing to read sits in the poller - but it doubles
// again once a pool is added, and that is where it hurt: 20 threads on 5 CPUs echoed at
// 532k/s where 5 threads on 5 CPUs echoed at 858k/s (the measurement is in the README).
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

// Returns defaultValue for a missing flag and for one whose value does not parse, matching
// the way the Go servers' -tp* flags treat 0 as "the implementation's own default" rather than
// an error.
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

// The per-CPU thread counts (-loopspercpu, -workerspercpu) are multipliers rather than counts,
// so they are fractional: a quarter of the CPUs is the pool's own default share. Treated like
// parseIntFlag treats its flags - 0 means "size it the usual way", and an unparsable value is
// a warning rather than an exit.
double parseDoubleFlag(int argc, char **argv, const char *name, double defaultValue) {
    std::string prefix = std::string("-") + name + "=";
    for (int i = 1; i < argc; ++i) {
        std::string arg(argv[i]);
        if (arg.rfind(prefix, 0) == 0) {
            const std::string value = arg.substr(prefix.size());
            char *end = nullptr;
            double parsed = std::strtod(value.c_str(), &end);
            if (end == value.c_str() || *end != '\0' || !std::isfinite(parsed)) {
                std::fprintf(stderr, "uwebsockets: ignoring -%s=%s, want a number\n", name,
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
            // ones, so that -poolqueue is the total, as -tpqueue is for the Go pools.
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

// Null unless -logicpool is on, which is the whole of the setting: the handlers branch on it
// once, at registration.
std::unique_ptr<TaskPool> g_pool;

// What /taskpool answers, for the Pool row of the reports: "logicpool" with the pool on, and
// "inline" without it, the name a Go server run -taskpool=inline reports for the same
// arrangement. Written once, before the loops start. See config.GetFrameworkTaskPool.
std::string g_taskPoolReport = "inline";

std::atomic<bool> g_warnedRefusal{false};

void warnRefusalOnce() {
    bool expected = false;
    if (g_warnedRefusal.compare_exchange_strong(expected, true)) {
        std::fprintf(stderr,
                     "uwebsockets: taskpool queue full, echoing on the event loop instead; "
                     "raise -poolqueue if this is not what you meant to measure\n");
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
//
// One defer per batch, rather than a queue per loop that a single deferred flush drains: that
// was tried, and it measured no faster. uSockets wakes a loop through an eventfd, whose
// counter the kernel coalesces on its own, so batching the defers saves the write syscalls but
// not the wakeups - and at a million echoes a second those writes are not what the time goes
// on. The README has the numbers.
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
// on messages; -poolqueue raises it.
constexpr unsigned kDefaultPending = 65536;

// How the process splits its threads.
//
// uWS runs one event loop per thread and is not thread safe within a loop, so a loop here is
// always single threaded; the parallelism is in how many there are, one uWS::App each, all
// listening on every port through uSockets' SO_REUSEPORT. Without a pool that is one loop per
// core, which is what this server has always run.
//
// With a pool the workers are OS threads on top of the loops. The Go servers do not have that
// question: their pool's goroutines multiplex onto the same GOMAXPROCS threads their pollers
// run on, so a pool there does not widen the process. Here it does, and the question is
// whether the pool's threads should come out of the loops' CPUs or sit on top of them.
//
// On top. Measured in Docker with the server and client pinned to disjoint CPU sets, as
// script/env.sh pins them, benchcli-uwscpp echoing a 1KiB payload over 50000 connections at
// 10000 concurrency, TPS averaged over two to four runs:
//
//     server CPUs  loops+workers  BenchEcho  BenchPipeline
//     2            1+1            266k       1.12M      the old default, loops = cpus - workers
//     2            2+1            323k       1.43M      <- this default
//     2            2+2            263k       1.33M
//     3            2+1            357k       2.17M      the old default
//     3            3+1            467k       2.83M      <- this default
//     3            4+1            464k       2.63M
//     3            3+2            360k       2.60M
//     5            4+1            582k       3.12M      the old default
//     5            5+1            585k       3.15M      <- this default
//     5            6+1            570k       3.03M
//     5            4+2            526k       2.97M
//     5            5+2            518k       3.10M
//
// A loop taken away for the pool costs a 1/cpus share of the poll, read, parse and write
// capacity, which is 30% of the throughput at 3 CPUs, while the one extra thread a worker adds
// cost nothing measurable at any size tried: a worker only copies a payload and defers it back,
// and it parks between batches. A second worker was slower at every size tried, and at 5 CPUs
// one worker already echoes within 1% of the pool-less (-logicpool=false) BenchEcho, so the pool grows only
// by one worker per kCoresPerWorker CPUs, for larger machines where one would not keep up.
//
// Both counts are overridable, either as a count of threads (-loops, -workers) or as a
// multiplier of the CPUs the process may run on (-loopspercpu, -workerspercpu), the form that
// carries from one machine to another and the one script/config.sh configures. Each flag sets
// its own side only; the other keeps the sizing above. Nothing larger than 5 server CPUs was
// measured, so a bigger machine is worth re-measuring: raise or lower the multiplier and watch
// the server's CPU% and TPS columns move together.
struct ThreadPlan {
    unsigned loops;
    unsigned workers;  // 0 when the echo runs in the loop (-logicpool=false)
};

// One worker per this many cores, when nothing on the command line says otherwise, and at
// least one. Below 8 CPUs that is a single worker, which is what the table above measured best;
// a pool of one thread for every loop would be a poor default for any callback heavier than an
// echo, so a quarter of the CPUs is where it scales to. The multiplier that says the same thing
// is -workerspercpu=0.25, give or take the rounding (this one divides down, the multiplier rounds
// to nearest).
constexpr unsigned kCoresPerWorker = 4;

// One thread count, from the two flags that can set it: the absolute one (-loops, -workers) if
// it is set, else the per-CPU multiplier (-loopspercpu, -workerspercpu) against the CPUs this
// process may actually run on, which is the form that means the same thing on machines of
// different sizes - the benchmark configures one N for every host it runs on, and a host with
// twice the CPUs gets twice the threads. A multiplier that rounds to nothing still gets one
// thread. 0 in both leaves the count to the caller's own sizing, which is what 0 back from
// here means.
unsigned resolveThreadCount(int argc, char **argv, const char *absoluteName,
                            const char *perCPUName, unsigned cores) {
    const long absolute = parseIntFlag(argc, argv, absoluteName, 0);
    if (absolute > 0) return unsigned(absolute);
    const double perCPU = parseDoubleFlag(argc, argv, perCPUName, 0);
    if (perCPU <= 0) return 0;
    const long scaled = std::lround(perCPU * double(cores));
    return scaled > 0 ? unsigned(scaled) : 1;
}

ThreadPlan planThreads(int argc, char **argv, bool logicPool, unsigned cores) {
    const unsigned loopCount = resolveThreadCount(argc, argv, "loops", "loopspercpu", cores);
    const unsigned workerCount = resolveThreadCount(argc, argv, "workers", "workerspercpu", cores);

    ThreadPlan plan{cores, 0};
    if (!logicPool) {
        // No pool to leave room for, so the loop count is the whole of it.
        if (loopCount > 0) plan.loops = loopCount;
        if (plan.loops == 0) plan.loops = 1;
        return plan;
    }
    // Each side is sized on its own: a loop for every core, and the workers on top of them.
    // Setting one does not shrink the other, since what was measured to cost throughput was a
    // loop given up for the pool, not the pool's extra thread.
    plan.loops = loopCount > 0 ? loopCount : cores;
    plan.workers = workerCount > 0 ? workerCount : cores / kCoresPerWorker;
    if (plan.loops == 0) plan.loops = 1;
    if (plan.workers == 0) plan.workers = 1;
    return plan;
}

// Builds the logic pool when -logicpool asks for it, and logs which way the server answers so
// that a report can be read back against the scheduling that produced it.
void installTaskPool(int argc, char **argv, bool logicPool, const ThreadPlan &plan) {
    if (!logicPool) {
        g_taskPoolReport = "inline";
        std::fprintf(stderr,
                     "uwebsockets logicpool: off -> event loop (the echo is written from the loop "
                     "callback, which is uWS's own scheduling) loops=%u cpus=%u\n",
                     plan.loops, availableCPUs());
        return;
    }

    const long queueSize = parseIntFlag(argc, argv, "poolqueue", 0);
    unsigned pending = queueSize > 0 ? unsigned(queueSize) : kDefaultPending;
    g_pool = std::make_unique<TaskPool>(plan.workers, pending);
    g_taskPoolReport = "logicpool";
    std::fprintf(stderr,
                 "uwebsockets logicpool: on -> thread pool off the event loop workers=%u "
                 "shards=%u pending=%u rejects=true loops=%u cpus=%u\n",
                 g_pool->workers(), g_pool->workers(), g_pool->pending(), plan.loops,
                 availableCPUs());
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

    const unsigned cores = availableCPUs();

    // Off by default, and independent of the Go servers' -taskpool: see the Logic thread pool
    // section at the top of this file.
    const bool logicPool = parseBoolFlag(argc, argv, "logicpool", false);
    const int portStart = kPortStart;
    const int portEnd = kPortEnd;
    std::vector<int> ports;
    ports.reserve(portEnd - portStart + 1);
    for (int port = portStart; port <= portEnd; ++port) ports.push_back(port);

    const ThreadPlan plan = planThreads(argc, argv, logicPool, cores);
    std::fprintf(stderr,
                 "uwebsockets benchmark config: loops=%u workers=%u threads=%u cpus=%u "
                 "hardware_concurrency=%u ports=%d-%d\n",
                 plan.loops, plan.workers, plan.loops + plan.workers, cores,
                 std::thread::hardware_concurrency(), portStart, portEnd);

    installTaskPool(argc, argv, logicPool, plan);
    // The two counts on a line of their own, the one script/servers.sh copies to the benchmark
    // console: the lines above carry them among everything else.
    if (g_pool) {
        std::fprintf(stderr, "uwebsockets threads: event loops=%u, task pool workers=%u, cpus=%u\n",
                     plan.loops, g_pool->workers(), cores);
    } else {
        std::fprintf(stderr,
                     "uwebsockets threads: event loops=%u, task pool workers=0 (-logicpool=false "
                     "answers on the event loops), cpus=%u\n",
                     plan.loops, cores);
    }

    std::vector<std::thread> workers;
    workers.reserve(plan.loops);
    for (unsigned i = 0; i < plan.loops; ++i) {
        workers.emplace_back(runWorker, std::cref(ports));
    }
    for (auto &worker : workers) worker.join();
    g_pool.reset();
    return 0;
}
