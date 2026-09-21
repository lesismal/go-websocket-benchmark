#pragma once
#include "options.hpp"
#include <curl/curl.h>
#include <filesystem>
#include <fstream>
#include <future>
#include <iomanip>
#include <numeric>
#include <sstream>

inline std::string http(const std::string &url, const std::string *body=nullptr, long timeout=15) {
    auto *curl=curl_easy_init();
    if (!curl) throw std::runtime_error("curl initialization failed");
    std::string result;
    curl_easy_setopt(curl,CURLOPT_URL,url.c_str());
    curl_easy_setopt(curl,CURLOPT_NOSIGNAL,1L);
    curl_easy_setopt(curl,CURLOPT_CONNECTTIMEOUT,5L);
    curl_easy_setopt(curl,CURLOPT_TIMEOUT,timeout);
    curl_easy_setopt(curl,CURLOPT_FAILONERROR,1L);
    curl_easy_setopt(curl,CURLOPT_WRITEFUNCTION,+[](char *p,size_t size,size_t count,void *data)->size_t {
        try { static_cast<std::string *>(data)->append(p,size*count); return size*count; }
        catch (...) { return 0; }
    });
    curl_easy_setopt(curl,CURLOPT_WRITEDATA,&result);
    if (body) {
        curl_easy_setopt(curl,CURLOPT_POSTFIELDS,body->data());
        curl_easy_setopt(curl,CURLOPT_POSTFIELDSIZE,long(body->size()));
    }
    auto error=curl_easy_perform(curl);
    std::string message=curl_easy_strerror(error);
    curl_easy_cleanup(curl);
    if (error!=CURLE_OK) throw std::runtime_error(url+": "+message);
    return result;
}
inline std::string controlURL(const Options &o) {
    std::string host=o.get("ip");
    if (host.find(':')!=std::string::npos && host[0]!='[') host="["+host+"]";
    auto f=o.get("f");
    int port=metadata["ports"][f][1];
    if (f=="fib" || f=="gws" || f=="uws_std" || f=="uws_events") ++port;
    return "http://"+host+":"+std::to_string(port);
}
inline json emptyReport(const std::string &kind,const Options &o) {
    json r=json::object();
    for (const auto &field:metadata["schemas"][kind]) {
        std::string key=field["key"];
        if (field["string"].get<bool>()) r[key]="";
        else r[key]=0;
    }
    r["Framework"]=o.get("f");
    return r;
}
inline std::string filename(const Options &o,const std::string &base,const std::string &ext) {
    return "output/report/"+o.get("preffix")+base+o.get("suffix")+ext;
}
inline void writeFile(const std::string &path,const std::string &data) {
    std::filesystem::create_directories(std::filesystem::path(path).parent_path());
    std::ofstream out(path,std::ios::binary);
    out.write(data.data(),std::streamsize(data.size()));
    if (!out) throw std::runtime_error("cannot write "+path);
}
inline std::string fixed(double v,const std::string &unit="") {
    std::ostringstream out; out<<std::fixed<<std::setprecision(2)<<v<<unit; return out.str();
}
inline std::string formatField(const json &r,const json &field) {
    std::string key=field["key"], fmt=field["fmt"];
    if (!r.contains(key) || r[key].is_null()) return "0";
    const auto &value=r[key];
    if (value.is_string()) return value.get<std::string>();
    double n=value.get<double>();
    if (fmt=="duration") {
        if (n>=1e9) return fixed(n/1e9,"s");
        if (n>=1e6) return fixed(n/1e6,"ms");
        if (n>=1e3) return fixed(n/1e3,"us");
        return value.dump()+"ns";
    }
    if (fmt=="mem") return n>=1073741824?fixed(n/1073741824,"G"):(n>=1048576?fixed(n/1048576,"M"):fixed(n/1024,"K"));
    if (field["floating"].get<bool>()) return fixed(n);
    return value.dump();
}
inline void saveReport(const Options &o,const std::string &kind,const json &r) {
    writeFile(filename(o,o.get("f")+"-"+kind,".json"),r.dump()+"\n");
    std::cout<<"BenchType: "<<kind<<'\n';
    for (const auto &field:metadata["schemas"][kind]) {
        if (field["optional"].get<bool>() && !o.boolean("tpn")) continue;
        std::cout<<field["title"].get<std::string>()<<": "<<formatField(r,field)<<'\n';
    }
}
inline void generateReports(const Options &o) {
    for (auto kind:{"Connections","BenchEcho","BenchRate"}) {
        std::vector<json> rows;
        for (const auto &f:metadata["frameworks"]) {
            auto path=filename(o,f.get<std::string>()+"-"+kind,".json");
            std::ifstream in(path);
            if (!in) continue;
            try { json row; in>>row; rows.push_back(std::move(row)); }
            catch (const std::exception &e) { throw std::runtime_error(path+": "+e.what()); }
        }
        std::string md;
        if (!rows.empty()) {
            std::vector<json> fields;
            for (const auto &field:metadata["schemas"][kind])
                if (!field["optional"].get<bool>() || o.boolean("tpn")) fields.push_back(field);
            md="|";
            for (const auto &f:fields) md+=" "+f["title"].get<std::string>()+" |";
            md+="\n|";
            for (size_t i=0;i<fields.size();++i) md+=" --- |";
            md+='\n';
            for (const auto &r:rows) {
                md+='|';
                for (const auto &f:fields) md+=" "+formatField(r,f)+" |";
                md+='\n';
            }
        }
        writeFile(filename(o,kind,".md"),md);
        std::cout<<kind<<"\n"<<md;
    }
}
inline void resourceStats(json &r,const Options &o,bool rate) {
    try {
        auto ps=json::parse(http(controlURL(o)+"/ps"));
        std::vector<double> cpu;
        if (ps.contains("cpu") && ps["cpu"].is_array()) cpu=ps["cpu"].get<std::vector<double>>();
        if (!cpu.empty()) {
            auto first=cpu.begin()+(cpu.size()>1);
            r["CPUMin"]=*std::min_element(first,cpu.end());
            r["CPUAvg"]=std::accumulate(first,cpu.end(),0.0)/std::distance(first,cpu.end());
            r["CPUMax"]=*std::max_element(cpu.begin(),cpu.end());
        }
        std::vector<uint64_t> mem;
        if (ps.contains("mem") && ps["mem"].is_array())
            for (const auto &v:ps["mem"]) if (v.is_object()) mem.push_back(v.value("rss",uint64_t(0)));
        if (!mem.empty()) {
            // Go's MEMRSSMin sorts the samples and skips the first sample for Min/Avg.
            std::sort(mem.begin(),mem.end());
            auto first=mem.begin()+(mem.size()>1);
            r["MEMMin"]=*first;
            r["MEMAvg"]=std::accumulate(first,mem.end(),uint64_t(0))/uint64_t(std::distance(first,mem.end()));
            r["MEMMax"]=mem.back();
        }
        double avg=r["CPUAvg"].get<double>();
        double tps=rate?r["RecvTimes"].get<double>()/(r["Duration"].get<double>()/1e9):r["TPS"].get<double>();
        r[rate?"EchoEER":"EER"]=avg>0?tps/avg:0.0;
    } catch (const std::exception &e) { std::cerr<<"server resource statistics unavailable: "<<e.what()<<'\n'; }
}
inline std::future<void> profile(const Options &o,const std::string &kind,bool enabled,int seconds) {
    if (!enabled) return {};
    return std::async(std::launch::async,[o,kind,seconds] {
        std::this_thread::sleep_for(std::chrono::seconds(2));
        try {
            auto cpu=http(controlURL(o)+"/debug/pprof/profile?seconds="+std::to_string(seconds),nullptr,long(seconds)+15);
            auto mem=http(controlURL(o)+"/debug/pprof/heap");
            writeFile(filename(o,o.get("f")+"-"+kind,".pprof.cpu"),cpu);
            writeFile(filename(o,o.get("f")+"-"+kind,".pprof.mem"),mem);
        } catch (const std::exception &e) { std::cerr<<kind<<" pprof: "<<e.what()<<'\n'; }
    });
}
