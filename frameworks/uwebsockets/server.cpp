// Echo WebSocket server built on uWebSockets (https://github.com/uNetworking/uWebSockets),
// the same C++ networking library used by benchcli-uwscpp. One uWS::App per hardware thread,
// all listening on every benchmark port via uSockets' default SO_REUSEPORT behavior.
//
// The /init and /ps routes replicate just enough of frameworks.HandleCommon (see
// frameworks/handlers.go) for the benchmark clients' resource reporting: /init starts a
// background CPU%/RSS sampler and returns the PID, /ps returns the samples as JSON. uSockets
// always enables TCP_NODELAY on accepted sockets and does not expose a way to turn it off, so
// -nodelay=false cannot be honored here (unlike the Go frameworks in this repo).
#include "App.h"

#include <atomic>
#include <chrono>
#include <csignal>
#include <cstdio>
#include <cstdlib>
#include <fstream>
#include <mutex>
#include <sstream>
#include <string>
#include <thread>
#include <vector>

#include <unistd.h>

namespace {

// Must match config.Ports[config.Uwebsockets] in config/config.go.
constexpr int kPortStart = 31001;
constexpr int kPortEnd = 31050;

struct PerSocketData {};

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

    app.ws<PerSocketData>("/*", {
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
    });

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

    for (int port : ports) {
        app.listen(port, [port](auto *socket) {
            if (!socket) {
                std::fprintf(stderr, "uwebsockets: failed to listen on port %d\n", port);
            }
        });
    }

    app.run();
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

    std::vector<std::thread> workers;
    workers.reserve(threadCount);
    for (unsigned int i = 0; i < threadCount; ++i) {
        workers.emplace_back(runWorker, std::cref(ports));
    }
    for (auto &worker : workers) worker.join();
    return 0;
}
