#pragma once
#include "options.hpp"
#include <algorithm>
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
// The pool the server installed, for the report's Pool column. "-" covers a server that
// installs none: the frameworks that take no -taskpool flag, and any whose /taskpool route
// does not answer. Mirrors config.GetFrameworkTaskPool.
inline std::string frameworkTaskPool(const Options &o) {
    const std::string none="-";
    try {
        std::string name=http(controlURL(o)+"/taskpool",nullptr,5);
        auto first=name.find_first_not_of(" \t\r\n");
        if (first==std::string::npos) return none;
        auto last=name.find_last_not_of(" \t\r\n");
        return name.substr(first,last-first+1);
    } catch (const std::exception &) { return none; }
}
inline json emptyReport(const std::string &kind,const Options &o) {
    json r=json::object();
    for (const auto &field:metadata["schemas"][kind]) {
        std::string key=field["key"];
        if (field["string"].get<bool>()) r[key]="";
        else r[key]=0;
    }
    r["Framework"]=o.get("f");
    r["BenchClient"]="benchcli-uwscpp";
    r["TaskPool"]=frameworkTaskPool(o);
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
    std::vector<std::pair<std::string,std::string>> lines;
    std::string typHeader="BenchType";
    size_t maxHeaderLen=typHeader.size();
    for (const auto &field:metadata["schemas"][kind]) {
        if (field["optional"].get<bool>() && !o.boolean("tpn")) continue;
        std::string title=field["title"].get<std::string>();
        maxHeaderLen=std::max(maxHeaderLen,title.size());
        lines.emplace_back(std::move(title),formatField(r,field));
    }
    typHeader.resize(maxHeaderLen,' ');
    std::cout<<typHeader<<": "<<kind<<'\n';
    for (auto &line:lines) {
        line.first.resize(maxHeaderLen,' ');
        std::cout<<line.first<<": "<<line.second<<'\n';
    }
}
// padCell mirrors github.com/lesismal/perf Table.padding so the console/markdown
// output lines up the same way benchcli-go's report tables do.
inline std::string padCell(const std::string &s,size_t maxLen,bool isFirst,size_t titleLeftPaddingIdx) {
    if (s.size()>=maxLen) return s;
    size_t paddingLen=maxLen-s.size();
    std::string out=s;
    if (isFirst) {
        // paddingLen shrinks as spaces are added, so the loop bound must be
        // re-evaluated each iteration (matches perf.Table.padding exactly).
        for (size_t i=0;i<paddingLen/2 && i<titleLeftPaddingIdx;++i) { out=" "+out; --paddingLen; }
        out.append(paddingLen,' ');
    } else {
        size_t half=paddingLen/2;
        out=std::string(half,' ')+out+std::string(half,' ');
        if (paddingLen%2==1) out+=' ';
    }
    return out;
}
inline std::string markdownTable(std::vector<std::string> title,std::vector<std::vector<std::string>> rows) {
    std::vector<size_t> maxLen;
    size_t columnNum=title.size();
    for (auto &v:title) maxLen.push_back(v.size());

    std::vector<std::vector<std::string>> allRows;
    allRows.push_back(std::vector<std::string>(columnNum,"---"));
    for (auto &r:rows) allRows.push_back(std::move(r));

    for (auto &v:allRows) {
        for (size_t j=0;j<v.size();++j) {
            if (maxLen.size()<j+1) maxLen.push_back(v[j].size());
            else if (v[j].size()>maxLen[j]) maxLen[j]=v[j].size();
        }
        if (v.size()>columnNum) columnNum=v.size();
    }

    while (title.size()<columnNum) title.push_back("");
    for (auto &r:allRows) while (r.size()<columnNum) r.push_back("");

    for (auto &m:maxLen) m+=2;

    std::string s="|";
    size_t titleLeftPaddingIdx=0;
    std::vector<std::string> alignedTitle(title.size());
    for (size_t i=0;i<title.size();++i) {
        alignedTitle[i]=padCell(title[i],maxLen[i],false,0);
        if (i==0)
            for (size_t k=0;k<alignedTitle[i].size();++k)
                if (alignedTitle[i][k]!=' ') titleLeftPaddingIdx=k;
        s+=alignedTitle[i]+"|";
    }
    s+="\n";

    for (auto &v:allRows) {
        s+="|";
        for (size_t j=0;j<v.size();++j) s+=padCell(v[j],maxLen[j],j==0,titleLeftPaddingIdx)+"|";
        s+="\n";
    }
    return s;
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
            std::vector<std::string> titles;
            for (const auto &f:fields) titles.push_back(f["title"].get<std::string>());
            std::vector<std::vector<std::string>> tableRows;
            for (const auto &r:rows) {
                std::vector<std::string> row;
                for (const auto &f:fields) row.push_back(formatField(r,f));
                tableRows.push_back(std::move(row));
            }
            md=markdownTable(titles,tableRows);
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
