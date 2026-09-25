#pragma once
// Sampling a server that runs on this machine, instead of asking it for the
// samples it took of itself.
//
// The CPU and MEM columns - and so CPU EER and MEM EER - used to come from one
// request to the server's /ps route, sent at the far end of a benchmark while
// the server is still buried under the connections it has just finished
// echoing to. That is exactly when a request is most likely to be reset or
// answered too late, and a column that silently read 0 took EER down with it.
// When the server is on this machine there is no need to ask: the client can
// read the process' own CPU time and resident memory straight from the
// operating system, which no amount of load on the server can make fail.
//
// Mirrors config/pssource.go and config/localps.go, down to how a sample
// becomes a percentage: the CPU time the process used over the interval
// divided by the interval, times 100, so 100 is one core busy - the same
// number gopsutil gives the Go client.
#include "options.hpp"
#include <atomic>
#include <cstdio>
#include <cstring>
#include <fstream>
#include <mutex>
#include <sstream>
#include <netdb.h>
#include <ifaddrs.h>
#include <arpa/inet.h>
#include <sys/socket.h>
#include <thread>
#include <unistd.h>
#include <vector>
#ifdef __linux__
#include <dirent.h>
#else
#include <libproc.h>
#include <mach/mach_time.h>
#endif

// serverProcessName is the name every framework's server binary is built
// under: script/build.sh writes ./output/bin/<framework>.server, and
// script/killone.sh stops it by the same path.
inline std::string serverProcessName(const std::string &framework) { return framework+".server"; }

inline std::string baseName(const std::string &path) {
    auto slash=path.find_last_of('/');
    return slash==std::string::npos?path:path.substr(slash+1);
}

// runCommand collects a command's standard output. Only ever ps, and only
// where there is no /proc to read instead.
inline std::string runCommand(const std::string &command) {
    auto *pipe=popen(command.c_str(),"r");
    if (!pipe) throw std::runtime_error("cannot run "+command);
    std::string out;
    char buffer[4096];
    while (auto n=fread(buffer,1,sizeof(buffer),pipe)) out.append(buffer,n);
    if (pclose(pipe)!=0 && out.empty()) throw std::runtime_error(command+" failed");
    return out;
}

// psNames reads pid and executable name from ps, for one pid or - with pid 0 -
// for every process this user can see. One ps for the whole listing rather
// than one per process: on a machine carrying a benchmark, forking a few
// hundred times to name a few hundred processes is not free.
inline std::vector<std::pair<int,std::string>> psNames(int pid) {
    std::string command="ps -o pid=,comm= ";
    command+=pid>0?("-p "+std::to_string(pid)):"-A";
    std::vector<std::pair<int,std::string>> names;
    std::istringstream lines(runCommand(command));
    std::string line;
    while (std::getline(lines,line)) {
        std::istringstream fields(line);
        int procPid=0;
        std::string rest,word;
        if (!(fields>>procPid)) continue;
        // An executable path with spaces in it comes back in several fields;
        // only the base name is wanted, so rejoin before taking it.
        while (fields>>word) rest+=(rest.empty()?"":" ")+word;
        if (!rest.empty()) names.emplace_back(procPid,baseName(rest));
    }
    return names;
}

// processName is the base name of a pid's argv[0], which for a server started
// by script/server.sh is <framework>.server.
inline std::string processName(int pid) {
#ifdef __linux__
    std::ifstream in("/proc/"+std::to_string(pid)+"/cmdline",std::ios::binary);
    if (!in) throw std::runtime_error("pid "+std::to_string(pid)+": no such process");
    std::string cmdline((std::istreambuf_iterator<char>(in)),std::istreambuf_iterator<char>());
    auto end=cmdline.find('\0');
    auto argv0=end==std::string::npos?cmdline:cmdline.substr(0,end);
    if (argv0.empty()) throw std::runtime_error("pid "+std::to_string(pid)+": empty command line");
    return baseName(argv0);
#else
    for (auto &entry:psNames(pid)) if (entry.first==pid) return entry.second;
    throw std::runtime_error("pid "+std::to_string(pid)+": no such process");
#endif
}

// findServerProcess returns the pid of this framework's server on this
// machine, found by the name it was built under rather than by asking it.
//
// Two servers answering to one name is an error rather than a guess: that is
// a leftover from an earlier run standing next to the one being measured, and
// sampling the wrong one would fill the resource columns with a number that
// has nothing to do with the benchmark. So is no match at all, which is what
// a server in another container - or on another machine - looks like from
// here. Both leave the caller to fall back to the server's own sampling.
inline int findServerProcess(const std::string &framework) {
    auto name=serverProcessName(framework);
    std::vector<int> matched;
#ifdef __linux__
    auto *dir=opendir("/proc");
    if (!dir) throw std::runtime_error("cannot read /proc");
    while (auto *entry=readdir(dir)) {
        char *end=nullptr;
        long pid=strtol(entry->d_name,&end,10);
        if (!end || *end || pid<=0) continue;
        // A process that exited between the listing and the read is not an
        // error; it is simply not a match.
        try { if (processName(int(pid))==name) matched.push_back(int(pid)); } catch (const std::exception &) {}
    }
    closedir(dir);
#else
    for (auto &entry:psNames(0)) if (entry.second==name) matched.push_back(entry.first);
#endif
    if (matched.empty()) throw std::runtime_error("no "+name+" process on this machine");
    if (matched.size()>1) {
        std::string pids;
        for (auto pid:matched) pids+=(pids.empty()?"":" ")+std::to_string(pid);
        throw std::runtime_error(std::to_string(matched.size())+" "+name+" processes on this machine ("+
                                 pids+"): stop the leftovers of earlier runs, e.g. with script/killall.sh");
    }
    return matched.front();
}

// verifyServerProcess checks that a pid is this framework's server here. A pid
// is only ever as good as the namespace it came from: the one /init answers
// with is the server's own, which is a different process here when the server
// runs in another container, and sampling it would measure whatever happens to
// hold that number locally.
inline void verifyServerProcess(int pid,const std::string &framework) {
    auto name=processName(pid),want=serverProcessName(framework);
    if (name!=want)
        throw std::runtime_error("pid "+std::to_string(pid)+" on this machine is "+name+", not "+want);
}

// readProcess reads a process' total CPU time in seconds and its resident
// memory in bytes.
inline void readProcess(int pid,double &cpuSeconds,uint64_t &rss) {
#ifdef __linux__
    std::ifstream stat("/proc/"+std::to_string(pid)+"/stat");
    if (!stat) throw std::runtime_error("pid "+std::to_string(pid)+": no such process");
    std::string line((std::istreambuf_iterator<char>(stat)),std::istreambuf_iterator<char>());
    // The executable name is parenthesised and may itself contain spaces and
    // parentheses, so the fields are counted from the last ')'.
    auto comm=line.find_last_of(')');
    if (comm==std::string::npos) throw std::runtime_error("pid "+std::to_string(pid)+": malformed stat");
    std::vector<std::string> fields;
    std::istringstream rest(line.substr(comm+1));
    std::string field;
    while (rest>>field) fields.push_back(field);
    // Counting from the state field, which is field 3 of the whole line:
    // utime is 14, stime 15 and delayacct_blkio_ticks 42. gopsutil adds the
    // last one to the process' CPU time, so the two clients agree.
    if (fields.size()<13) throw std::runtime_error("pid "+std::to_string(pid)+": short stat");
    double ticks=std::stod(fields[11])+std::stod(fields[12]);
    if (fields.size()>39) { try { ticks+=std::stod(fields[39]); } catch (const std::exception &) {} }
    auto hz=sysconf(_SC_CLK_TCK);
    cpuSeconds=ticks/double(hz>0?hz:100);

    std::ifstream statm("/proc/"+std::to_string(pid)+"/statm");
    uint64_t size=0,resident=0;
    if (!(statm>>size>>resident)) throw std::runtime_error("pid "+std::to_string(pid)+": cannot read statm");
    auto pageSize=sysconf(_SC_PAGESIZE);
    rss=resident*uint64_t(pageSize>0?pageSize:4096);
#else
    rusage_info_current usage{};
    if (proc_pid_rusage(pid,RUSAGE_INFO_CURRENT,(rusage_info_t *)&usage))
        throw std::runtime_error("pid "+std::to_string(pid)+": cannot read resource usage");
    // The times are in mach absolute time units, which are nanoseconds only on
    // Intel: an Apple Silicon tick is 125/3 of one, and taking them for
    // nanoseconds there divided every CPU column by about 42.
    static mach_timebase_info_data_t timebase=[]{
        mach_timebase_info_data_t info{};
        if (mach_timebase_info(&info) || !info.denom) { info.numer=1; info.denom=1; }
        return info;
    }();
    cpuSeconds=double(usage.ri_user_time+usage.ri_system_time)*double(timebase.numer)/
               double(timebase.denom)/1e9;
    rss=usage.ri_resident_size;
#endif
}

// LocalPSSampler samples a server process on this machine at the run's -pi
// interval, the way the server's own /ps sampler does. Nothing is asked of the
// server, so nothing here fails because it is busy carrying a million
// connections.
class LocalPSSampler {
public:
    // The process is gone, almost always: a server that exited or was killed
    // mid-run. Whatever was sampled before that is kept, since it is the
    // benchmark that was measured.
    static constexpr int kConsecutiveErrors=5;

    LocalPSSampler(int pid,int64_t intervalNs):pid_(pid),intervalNs(intervalNs>0?intervalNs:1'000'000'000) {
        // Read once before starting, so that a process this client cannot
        // sample at all - gone, or another user's - fails here, where the run
        // can still fall back to the server's own sampling, rather than at the
        // end of the benchmark with empty columns.
        double cpu=0;uint64_t rss=0;
        readProcess(pid_,cpu,rss);
        lastCPU=cpu;lastAt=nowNs();
        thread=std::thread([this]{run();});
    }
    ~LocalPSSampler() { stop(); }
    LocalPSSampler(const LocalPSSampler &)=delete;
    LocalPSSampler &operator=(const LocalPSSampler &)=delete;

    int pid() const { return pid_; }
    void stop() {
        stopping.store(true,std::memory_order_release);
        if (thread.joinable()) thread.join();
    }
    // samples copies what has been collected so far: a report is built while
    // the sampling thread is still appending.
    void samples(std::vector<double> &cpuOut,std::vector<uint64_t> &memOut) const {
        std::lock_guard<std::mutex> lock(mutex);
        cpuOut=cpu;memOut=mem;
    }

private:
    void run() {
        int errors=0;
        while (!stopping.load(std::memory_order_acquire)) {
            // Sleep in short steps so that stopping does not wait out a whole
            // sampling interval.
            auto wakeAt=nowNs()+intervalNs;
            while (nowNs()<wakeAt && !stopping.load(std::memory_order_acquire))
                std::this_thread::sleep_for(std::chrono::milliseconds(5));
            if (stopping.load(std::memory_order_acquire)) return;
            double cpuSeconds=0;uint64_t rss=0;
            try { readProcess(pid_,cpuSeconds,rss); }
            catch (const std::exception &e) {
                if (++errors>=kConsecutiveErrors) {
                    std::cerr<<"sampling pid "<<pid_<<" stopped after "<<errors<<" failures: "<<e.what()<<'\n';
                    return;
                }
                continue;
            }
            errors=0;
            auto at=nowNs();
            double elapsed=double(at-lastAt)/1e9;
            double percent=elapsed>0?(cpuSeconds-lastCPU)/elapsed*100:0;
            lastCPU=cpuSeconds;lastAt=at;
            std::lock_guard<std::mutex> lock(mutex);
            cpu.push_back(percent);mem.push_back(rss);
        }
    }

    int pid_;
    int64_t intervalNs;
    double lastCPU=0;
    int64_t lastAt=0;
    mutable std::mutex mutex;
    std::vector<double> cpu;
    std::vector<uint64_t> mem;
    std::atomic<bool> stopping{false};
    std::thread thread;
};

// localHost reports whether the host the clients dial is this machine: the
// loopback address, or one of this machine's own interface addresses. It is
// what tells a single-node run from a two-node one without either having to
// say so, since BENCH_SERVER_HOST is the only thing that differs between them.
//
// A host that is this machine's by address may still be another container's
// server, with its own process table; that is why sampling it is attempted
// rather than assumed, and falls back to the server's own sampling.
inline bool localHost(std::string host) {
    if (!host.empty() && host.front()=='[' && host.back()==']') host=host.substr(1,host.size()-2);
    if (host.empty()) return false;

    addrinfo hints{},*resolved=nullptr;
    hints.ai_family=AF_UNSPEC;
    hints.ai_socktype=SOCK_STREAM;
    if (getaddrinfo(host.c_str(),nullptr,&hints,&resolved) || !resolved) return false;

    auto text=[](const sockaddr *addr)->std::string {
        char buffer[INET6_ADDRSTRLEN]={0};
        if (addr->sa_family==AF_INET) {
            auto *in=(const sockaddr_in *)addr;
            if (!inet_ntop(AF_INET,&in->sin_addr,buffer,sizeof(buffer))) return "";
        } else if (addr->sa_family==AF_INET6) {
            auto *in6=(const sockaddr_in6 *)addr;
            if (!inet_ntop(AF_INET6,&in6->sin6_addr,buffer,sizeof(buffer))) return "";
        } else return "";
        return buffer;
    };
    auto unspecifiedOrLoopback=[](const sockaddr *addr) {
        if (addr->sa_family==AF_INET) {
            auto host4=ntohl(((const sockaddr_in *)addr)->sin_addr.s_addr);
            return (host4>>24)==127 || host4==INADDR_ANY;
        }
        if (addr->sa_family==AF_INET6) {
            const auto &in6=((const sockaddr_in6 *)addr)->sin6_addr;
            return bool(IN6_IS_ADDR_LOOPBACK(&in6)) || bool(IN6_IS_ADDR_UNSPECIFIED(&in6));
        }
        return false;
    };

    std::vector<std::string> wanted;
    bool local=false;
    for (auto *a=resolved;a && !local;a=a->ai_next) {
        if (unspecifiedOrLoopback(a->ai_addr)) local=true;
        else if (auto address=text(a->ai_addr);!address.empty()) wanted.push_back(address);
    }
    freeaddrinfo(resolved);
    if (local || wanted.empty()) return local;

    ifaddrs *interfaces=nullptr;
    if (getifaddrs(&interfaces)) return false;
    for (auto *i=interfaces;i && !local;i=i->ifa_next) {
        if (!i->ifa_addr) continue;
        auto address=text(i->ifa_addr);
        if (address.empty()) continue;
        for (auto &want:wanted) if (want==address) { local=true; break; }
    }
    freeifaddrs(interfaces);
    return local;
}
