// Echo WebSocket server built on Aegon (https://github.com/UdayKhare09/Aegon), a C++26 web
// framework on Linux io_uring: multishot accept and recv into provided buffer rings, one
// shared-nothing event loop per thread, each thread with its own SO_REUSEPORT listener on every
// port. Linux only.
//
// Set up the way HttpArena's aegon entry sets it up (frameworks/aegon/main.cpp in
// https://github.com/MDA2AV/HttpArena): router.ws("/ws") with no handler, which is Aegon's
// batched fast-path echo; the ring and provided-buffer sizes it picks by RLIMIT_MEMLOCK; and one
// event loop per CPU the process may run on, sized the way its cgroup_cpus() sizes them. Only
// the WebSocket route is kept of HttpArena's routes, on this benchmark's 50 ports.
//
// The /init and /ps routes replicate just enough of frameworks.HandleCommon (see
// frameworks/handlers.go) for the benchmark clients' resource reporting, as the uwebsockets
// server does: /init starts a background CPU%/RSS sampler and returns the PID, /ps returns the
// samples as JSON. They are registered on every port, so the last benchmark port doubles as
// the control port.
#include <aegon/http/Server.h>

#include <sys/resource.h>
#include <sched.h>
#include <unistd.h>

#include <algorithm>
#include <atomic>
#include <chrono>
#include <csignal>
#include <cstdio>
#include <cstdlib>
#include <fstream>
#include <mutex>
#include <sstream>
#include <string>
#include <string_view>
#include <thread>
#include <vector>

using namespace aegon::http;

namespace {

// Must match config.Ports[config.Aegon] in config/config.go.
constexpr int kPortStart = 17701;
constexpr int kPortEnd = 17750;

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

// Returns defaultValue for a missing flag and for one whose value does not parse; 0 is the
// framework's own default, as for the Go servers' -eventloops.
long parseIntFlag(int argc, char **argv, const char *name, long defaultValue) {
    std::string prefix = std::string("-") + name + "=";
    for (int i = 1; i < argc; ++i) {
        std::string arg(argv[i]);
        if (arg.rfind(prefix, 0) == 0) {
            const std::string value = arg.substr(prefix.size());
            char *end = nullptr;
            long parsed = std::strtol(value.c_str(), &end, 10);
            if (end == value.c_str() || *end != '\0') {
                std::fprintf(stderr, "aegon: ignoring -%s=%s, want an integer\n", name, value.c_str());
                return defaultValue;
            }
            return parsed;
        }
    }
    return defaultValue;
}

// The CPUs this process may run on: the smallest of the cgroup CPU quota, the affinity mask
// (script/env.sh pins the server with taskset) and the online CPUs. The same sizing as
// HttpArena's cgroup_cpus().
unsigned availableCPUs() {
    unsigned quota = 0;
    {
        std::ifstream f("/sys/fs/cgroup/cpu.max");
        std::string q, p;
        if (f >> q >> p && q != "max") {
            long long qv = std::strtoll(q.c_str(), nullptr, 10);
            long long pv = std::strtoll(p.c_str(), nullptr, 10);
            if (qv > 0 && pv > 0) quota = unsigned(qv / pv);
        }
    }
    if (quota == 0) {
        std::ifstream qf("/sys/fs/cgroup/cpu/cpu.cfs_quota_us");
        std::ifstream pf("/sys/fs/cgroup/cpu/cpu.cfs_period_us");
        long long qv = -1, pv = -1;
        if (qf >> qv && pf >> pv && qv > 0 && pv > 0) quota = unsigned(qv / pv);
    }

    unsigned effective = std::thread::hardware_concurrency();
    if (effective == 0) effective = 1;
    cpu_set_t set;
    CPU_ZERO(&set);
    if (sched_getaffinity(0, sizeof(set), &set) == 0 && CPU_COUNT(&set) > 0) {
        effective = std::min(effective, unsigned(CPU_COUNT(&set)));
    }
    if (quota > 0) effective = std::min(effective, quota);
    return std::max(1u, effective);
}

// Minimal stand-in for gopsutil's process.Process.Percent/MemoryInfo, sampled at the interval
// requested by /init. The same sampler as frameworks/uwebsockets/server.cpp's: it mirrors
// github.com/lesismal/perf's PSCounter JSON shape closely enough for the clients, which only
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
// Hand-rolled instead of going through glaze for a single integer field.
long long parsePsIntervalNanos(std::string_view body) {
    auto pos = body.find("PsInterval");
    if (pos == std::string_view::npos) return 0;
    pos = body.find(':', pos);
    if (pos == std::string_view::npos) return 0;
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

}  // namespace

int main(int argc, char **argv) {
    // Server::run blocks in its loops for good; script/killone.sh stops it with SIGINT.
    std::signal(SIGINT, [](int) { std::_Exit(0); });

    const bool nodelay = parseBoolFlag(argc, argv, "nodelay", true);
    const unsigned cpus = availableCPUs();
    const long eventLoops = parseIntFlag(argc, argv, "eventloops", 0);
    const unsigned loops = eventLoops > 0 ? unsigned(eventLoops) : cpus;

    // HttpArena's sizing: io_uring rings and provided-buffer pools as large as a container's
    // locked-memory limit allows.
    struct rlimit rl{};
    const bool lowMemlock = getrlimit(RLIMIT_MEMLOCK, &rl) == 0 && rl.rlim_cur != RLIM_INFINITY &&
                            rl.rlim_cur < 64 * 1024 * 1024;

    Server server;
    if (lowMemlock) {
        server.ring_entries(512);
        server.buffer_pool_entries(256);
    } else {
        server.ring_entries(8192);
        server.buffer_pool_entries(16384);
    }
    // Aegon sets TCP_NODELAY on every accepted socket unless told not to.
    server.tcp_nodelay(nodelay);

    Router &router = server.router();
    router.ws("/ws");
    router.post("/init", [](Context &ctx) {
        g_psSampler.start(std::chrono::nanoseconds(parsePsIntervalNanos(ctx.req().body())));
        ctx.res().text(std::to_string(getpid()));
    });
    router.get("/ps", [](Context &ctx) { ctx.res().json(std::string_view(g_psSampler.psJSON())); });

    for (int port = kPortStart; port <= kPortEnd; ++port) {
        server.listen(static_cast<uint16_t>(port));
    }

    std::fprintf(stderr,
                 "aegon benchmark config: loops=%u cpus=%u hardware_concurrency=%u nodelay=%s "
                 "ring_entries=%u buffer_pool_entries=%u ports=%d-%d\n",
                 loops, cpus, std::thread::hardware_concurrency(), nodelay ? "true" : "false",
                 server.ring_entries(), unsigned(server.buffer_pool_entries()), kPortStart, kPortEnd);
    std::fprintf(stderr, "aegon threads: event loops=%u (-eventloops=%ld, 0 = one per CPU), cpus=%u\n",
                 loops, eventLoops, cpus);

    server.run(loops);
    return 0;
}
