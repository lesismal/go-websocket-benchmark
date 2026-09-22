#pragma once
#include <algorithm>
#include <chrono>
#include <cmath>
#include <cstdint>
#include <iostream>
#include <limits>
#include <map>
#include <regex>
#include <stdexcept>
#include <string>
#include <thread>
#include <nlohmann/json.hpp>
using json = nlohmann::ordered_json;
using Clock = std::chrono::steady_clock;
inline int64_t nowNs() {
    return std::chrono::duration_cast<std::chrono::nanoseconds>(Clock::now().time_since_epoch()).count();
}
#include "metadata.hpp"

// Where a run's CPU and MEM samples come from; -ps takes these names. See
// pssample.hpp, and config/pssource.go for the same three in the Go client.
inline constexpr const char *kPSModeAuto="auto";
inline constexpr const char *kPSModeLocal="local";
inline constexpr const char *kPSModeRemote="remote";

struct Options {
    std::map<std::string, std::string> values = {
        {"nodelay","true"}, {"m","4294967296"}, {"f","nbio_std"}, {"ip","127.0.0.1"},
        {"c","10000"}, {"dc","2000"}, {"dt","5s"}, {"dr","5"}, {"dri","100ms"},
        {"b","1024"}, {"check","false"}, {"pi","1000"}, {"ps",kPSModeAuto}, {"tpn","true"},
        {"ec","10000"}, {"en","2000000"}, {"el","0"}, {"ep","true"}, {"epd","5"},
        {"rate","false"}, {"rc","10000"}, {"rd","10"}, {"rr","200"}, {"rbs","16384"},
        {"rl","0"}, {"rp","false"}, {"rpd","5"}, {"r","false"}, {"preffix",""}, {"suffix",""},
        {"threads","0"}, {"io-timeout","30s"}
    };
    const std::vector<std::string> bools = {"nodelay","check","tpn","ep","rate","rp","r"};
    bool help = false;
    std::string get(const std::string &key) const { return values.at(key); }
    bool boolean(const std::string &key) const {
        const auto v = get(key);
        if (v=="true" || v=="1" || v=="t" || v=="TRUE" || v=="True" || v=="T") return true;
        if (v=="false" || v=="0" || v=="f" || v=="FALSE" || v=="False" || v=="F") return false;
        throw std::runtime_error("invalid boolean -" + key + "=" + v);
    }
    int64_t number(const std::string &key) const {
        auto v = get(key); size_t end = 0;
        int64_t n = std::stoll(v, &end);
        if (end != v.size()) throw std::runtime_error("invalid integer -" + key);
        return n;
    }
    int integer(const std::string &key) const {
        auto n = number(key);
        if (n < 0 || n > std::numeric_limits<int>::max()) throw std::runtime_error("out of range -" + key);
        return int(n);
    }
    int64_t duration(const std::string &key) const {
        std::string v = get(key);
        if (v == "0") return 0;
        std::regex part(R"(([0-9]+(?:\.[0-9]*)?|\.[0-9]+)(ns|us|µs|μs|ms|s|m|h))");
        std::map<std::string, double> scale = {{"ns",1},{"us",1e3},{"µs",1e3},{"μs",1e3},{"ms",1e6},{"s",1e9},{"m",60e9},{"h",3600e9}};
        double result = 0;
        std::smatch match;
        while (!v.empty()) {
            if (!std::regex_search(v, match, part, std::regex_constants::match_continuous))
                throw std::runtime_error("invalid duration -" + key);
            result += std::stod(match[1]) * scale.at(match[2]);
            v = match.suffix();
        }
        if (!std::isfinite(result) || result >= double(INT64_MAX)/2) throw std::runtime_error("duration too large -" + key);
        return int64_t(result);
    }
    Options(int argc, char **argv) {
        for (int i=1; i<argc; ++i) {
            std::string key = argv[i];
            if (key=="-h" || key=="--help" || key=="-help") { help=true; return; }
            if (key.empty() || key[0]!='-') throw std::runtime_error("unexpected argument: " + key);
            key.erase(0, key.rfind("--",0)==0 ? 2 : 1);
            auto equal=key.find('=');
            std::string value;
            if (equal!=std::string::npos) { value=key.substr(equal+1); key.resize(equal); }
            if (!values.count(key)) throw std::runtime_error("unknown flag -" + key);
            if (equal==std::string::npos) {
                if (std::find(bools.begin(),bools.end(),key)!=bools.end()) value="true";
                else if (++i<argc) value=argv[i];
                else throw std::runtime_error("missing value -" + key);
            }
            values[key]=value;
        }
        for (auto &key:bools) boolean(key);
        for (auto key:{"c","dc","dr","b","pi","ec","en","el","epd","rc","rd","rr","rbs","rl","rpd","threads"}) integer(key);
        if (number("m")<0) throw std::runtime_error("-m must be nonnegative");
        for (auto key:{"dt","dri","io-timeout"}) duration(key);
        if (!metadata["ports"].contains(get("f"))) throw std::runtime_error("unknown framework: " + get("f"));
        for (auto key:{"preffix","suffix"})
            if (get(key).find_first_of("/\\")!=std::string::npos) throw std::runtime_error("report prefix/suffix must not contain paths");
        if (get("ip").empty() || get("ip").find_first_of("\r\n /?#@")!=std::string::npos)
            throw std::runtime_error("invalid -ip");
        // Checked here so that a misspelled -ps fails before the run spends
        // the whole benchmark rather than after.
        if (get("ps")!=kPSModeAuto && get("ps")!=kPSModeLocal && get("ps")!=kPSModeRemote)
            throw std::runtime_error("unsupported -ps value "+get("ps")+" (want "+kPSModeAuto+", "+
                                     kPSModeLocal+" or "+kPSModeRemote+")");
    }
    void usage() const {
        std::cout << "benchcli-uwscpp: uWebSockets C++ benchmark client\n"
                     "Flags match benchcli-go (use -flag=value or -flag value; booleans use =false).\n";
        for (const auto &v:values) std::cout << "  -" << v.first << "=" << v.second << '\n';
        std::cout << "-ps: where the server's CPU and MEM samples come from: auto samples the server here when\n"
                     "     it runs on this machine and asks it over HTTP when it does not, local always samples\n"
                     "     here, remote always asks.\n"
                     "-threads: native event-loop threads (0: available CPUs, capped by concurrency).\n"
                     "-io-timeout: maximum echo response wait; -dt: TCP + upgrade timeout.\n"
                     "-m: native memory limit in bytes (Linux: address space; macOS: sampled RSS; 0: unlimited).\n";
    }
};
