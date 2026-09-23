#include "report.hpp"
#include "engine.hpp"
#include <csignal>
#include <sys/resource.h>
#ifdef __linux__
#include <sched.h>
#endif

namespace {
volatile std::sig_atomic_t interrupted=0;
void signalHandler(int) {interrupted=1;}
int availableCPUs() {
#ifdef __linux__
    cpu_set_t set;
    if (!sched_getaffinity(0,sizeof(set),&set)) return std::max(1,CPU_COUNT(&set));
#endif
    return std::max(1,int(std::thread::hardware_concurrency()));
}
struct Runner {
    Shared shared;
    int64_t memoryBudget;
    std::vector<std::unique_ptr<Worker>> workers;
    explicit Runner(const Options &o):shared(o),memoryBudget(o.number("m")) {
        int n=o.integer("c");if (!n) n=1000;
        int dc=o.integer("dc");if (!dc) dc=availableCPUs()*1000;
        dc=std::min(dc,n);
        int ec=o.integer("ec");if (!ec) ec=availableCPUs()*1000;
        int rc=o.integer("rc");if (!rc) rc=50000;
        int threads=o.integer("threads");if (!threads) threads=availableCPUs();
        threads=std::min({threads,n,dc,ec,o.boolean("rate")?rc:n});
        for (int i=0;i<threads;++i)
            workers.push_back(std::make_unique<Worker>(o,shared,i,threads,n/threads+(i<n%threads),dc/threads+(i<dc%threads)));
        std::cout<<"Client: benchcli-uwscpp, event-loop threads: "<<threads<<std::endl;
    }
    ~Runner() {
        shared.stage.store(Stop,std::memory_order_release);
        for (auto &w:workers) if (w->thread.joinable()) w->thread.join();
    }
    void wait(int stage) {
        auto logAt=nowNs()+1'000'000'000;
        auto memoryAt=nowNs();
        for (;;) {
            #ifdef __APPLE__
            if (memoryBudget && nowNs()>=memoryAt) {
                rusage usage{};
                if (!getrusage(RUSAGE_SELF,&usage) && usage.ru_maxrss>memoryBudget)
                    throw std::runtime_error("native client exceeded -m RSS limit");
                memoryAt=nowNs()+100'000'000;
            }
#else
            (void)memoryAt;
#endif
            if (interrupted) throw std::runtime_error("interrupted");
            if (shared.fatal.load()) throw std::runtime_error("worker failed (allocation or socket setup error)");
            bool done=true;
            for (auto &w:workers) if (w->done.load(std::memory_order_acquire)!=stage) done=false;
            if (done) return;
            if (stage==Dial && nowNs()>=logAt) {
                int alive=0;for (auto &w:workers) alive+=w->live.load();
                std::cout<<"Connections: "<<alive<<" connected"<<std::endl;logAt=nowNs()+1'000'000'000;
            }
            std::this_thread::sleep_for(std::chrono::milliseconds(2));
        }
    }
    int allocate(int requested,bool rate=false) {
        int total=0;
        for (auto &w:workers) {total+=w->live.load();if(rate)w->rateConcurrency=0;else w->echoConcurrency=0;}
        int wanted=std::min(requested,total),remaining=wanted;
        while (remaining) {
            bool progress=false;
            for (auto &w:workers) {
                auto &quota=rate?w->rateConcurrency:w->echoConcurrency;
                if (remaining && quota<w->live.load()) {++quota;--remaining;progress=true;}
            }
            if (!progress) break;
        }
        return wanted-remaining;
    }
    void echo(int stage,int64_t total,int concurrency,int limit) {
        int64_t assigned=0,quota=0;
        for (auto &w:workers) {
            quota+=w->echoConcurrency;
            auto end=concurrency?total*quota/concurrency:0;
            w->target=end-assigned;assigned=end;
        }
        shared.limiter.reset(limit);
        shared.stage.store(stage,std::memory_order_release);wait(stage);
    }
    Stats collect(bool dial) {
        Stats result;
        for (auto &w:workers) {
            auto &v=dial?w->dialStats:w->echoStats;
            result.success+=v.success;result.failed+=v.failed;
            if (!result.begin || (v.begin && v.begin<result.begin)) result.begin=v.begin;
            result.end=std::max(result.end,v.end);
            result.latency.insert(result.latency.end(),v.latency.begin(),v.latency.end());
        }
        return result;
    }
};
void setLatency(json &r,Stats &s,bool tpn) {
    r["Success"]=s.success;r["Failed"]=s.failed;
    r["Used"]=std::max<int64_t>(1,s.end-s.begin);
    r["TPS"]=int64_t(double(s.success)*1e9/r["Used"].get<double>());
    if (tpn && !s.latency.empty()) {
        std::sort(s.latency.begin(),s.latency.end());
        r["Min"]=s.latency.front();r["Max"]=s.latency.back();
        long double sum=0;for(auto v:s.latency)sum+=v;
        r["Avg"]=int64_t(sum/s.latency.size());
        for (int p:{50,75,90,95,99}) r["TP"+std::to_string(p)]=s.latency[std::min(s.latency.size()-1,(s.latency.size()*p+99)/100-1)];
    }
}
void memoryLimit(const Options &o) {
    auto limit=o.number("m");if (!limit) return;
#ifdef __APPLE__
    // macOS does not implement RLIMIT_AS; Runner checks peak RSS every 100 ms.
    return;
#else
    rlimit existing{};
    if (getrlimit(RLIMIT_AS,&existing)) throw std::runtime_error("cannot read process memory limit");
    existing.rlim_cur=std::min(existing.rlim_max,rlim_t(limit));
    if (setrlimit(RLIMIT_AS,&existing)) throw std::runtime_error("cannot set process memory limit; use -m=0 for unlimited");
#endif
}
int benchmark(const Options &o) {
    memoryLimit(o);
    Runner runner(o);
    for (auto &w:runner.workers) w->start();
    runner.wait(Dial);
    auto connections=emptyReport("Connections",o);
    auto dial=runner.collect(true);
    setLatency(connections,dial,o.boolean("tpn"));
    connections["Total"]=dial.success+dial.failed;
    int dc=0;for (auto &w:runner.workers) dc+=w->dialConcurrency;
    connections["Concurrency"]=dc;
    saveReport(o,"Connections",connections);
    if (!dial.success) throw std::runtime_error("no WebSocket connections established");
    // Where this run's CPU and MEM samples come from. On a run whose server is on
    // this machine the client samples the process itself and the server is never
    // asked, which is one less request to fail at the far end of a benchmark
    // carrying a million connections; see setupPS.
    auto ps=setupPS(o);
    int ec=o.integer("ec");if (!ec) ec=availableCPUs()*1000;
    int concurrency=runner.allocate(ec);
    if (!concurrency) throw std::runtime_error("all connections closed before echo benchmark");
    auto echoProfile=profile(o,"BenchEcho",o.boolean("ep"),std::max(1,o.integer("epd")));
    int64_t warmup=std::min<int64_t>(dial.success*5,2'000'000);
    std::cout<<"BenchEcho warmup: "<<warmup<<std::endl;
    runner.echo(Warmup,warmup,concurrency,o.integer("el"));
    std::cout<<"BenchEcho: "<<o.integer("en")<<" messages"<<std::endl;
    runner.echo(Echo,o.integer("en"),concurrency,o.integer("el"));
    auto echo=emptyReport("BenchEcho",o);
    auto stats=runner.collect(false);
    setLatency(echo,stats,o.boolean("tpn"));
    echo["Total"]=o.integer("en");echo["Conns"]=dial.success;echo["Concurrency"]=concurrency;echo["Payload"]=runner.shared.payloads[0].size();
    resourceStats(echo,o,false,ps);
    if (echoProfile.valid()) echoProfile.get();
    saveReport(o,"BenchEcho",echo);
    if (o.boolean("rate")) {
        int rc=o.integer("rc");if(!rc)rc=50000;
        int rateConcurrency=runner.allocate(rc,true);
        runner.shared.limiter.reset(o.integer("rl"));
        int seconds=o.integer("rd");if(!seconds)seconds=10;
        runner.shared.rateStart=nowNs();runner.shared.rateEnd=runner.shared.rateStart+int64_t(seconds)*1'000'000'000;
        auto rateProfile=profile(o,"BenchRate",o.boolean("rp"),std::max(1,o.integer("rpd")));
        std::cout<<"BenchRate: "<<seconds<<" seconds"<<std::endl;
        runner.shared.stage.store(Rate,std::memory_order_release);runner.wait(Rate);
        auto rate=emptyReport("BenchRate",o);
        int64_t sent=0,received=0,bytes=0;
        for (auto &w:runner.workers) {sent+=w->rateStats.sent;received+=w->rateStats.received;bytes+=w->rateStats.recvBytes;}
        rate["Duration"]=int64_t(seconds)*1'000'000'000;rate["Conns"]=dial.success;
        rate["Concurrency"]=rateConcurrency;
        rate["Pipeline"]=runner.shared.batch;
        rate["SendRate"]=std::max(1,o.integer("rr"));rate["Payload"]=runner.shared.payloads[0].size();
        rate["SendTimes"]=sent;rate["SendBytes"]=sent*int64_t(runner.shared.payloads[0].size());
        rate["RecvTimes"]=received;rate["RecvBytes"]=bytes;
        fillRateTPS(rate);
        resourceStats(rate,o,true,ps);
        if(rateProfile.valid())rateProfile.get();
        saveReport(o,"BenchRate",rate);
    }
    return stats.failed || dial.failed ? 1:0;
}
}
int main(int argc,char **argv) {
    std::signal(SIGINT,signalHandler);std::signal(SIGTERM,signalHandler);std::signal(SIGPIPE,SIG_IGN);
    try {
        Options options(argc,argv);
        if(options.help){options.usage();return 0;}
        if(options.boolean("r")){generateReports(options);return 0;}
        if(curl_global_init(CURL_GLOBAL_DEFAULT)!=CURLE_OK) throw std::runtime_error("curl global initialization failed");
        int result=benchmark(options);
        curl_global_cleanup();return result;
    } catch(const std::exception &e) {std::cerr<<"benchcli-uwscpp: "<<e.what()<<std::endl;return interrupted?130:1;}
}
