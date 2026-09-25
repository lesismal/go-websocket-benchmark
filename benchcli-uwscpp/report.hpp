#pragma once
#include "pssample.hpp"
#include <algorithm>
#include <curl/curl.h>
#include <filesystem>
#include <fstream>
#include <future>
#include <iomanip>
#include <map>
#include <memory>
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
// Control requests - /init, /ps, /taskpool - go to the pid port, which for most frameworks is
// also carrying benchmark connections. At a hundred thousand of them one attempt is not enough:
// a server still working through the backlog of a just-finished rate test can reset the
// connection or sit on the request past any deadline, and the resource columns that silently
// read 0 when that happened took EER down with them. Retry a transport failure, patiently; a
// reply the server produced, 404 included, is returned as it is, since waiting will not put a
// missing route there.
inline constexpr int kControlAttempts=4;
inline constexpr long kControlTimeout=30;
inline std::string httpRetry(const std::string &url,const std::string *body=nullptr,
                             int attempts=kControlAttempts,long timeout=kControlTimeout) {
    std::string lastError;
    for (int attempt=1;attempt<=attempts;++attempt) {
        if (attempt>1) std::this_thread::sleep_for(std::chrono::seconds(2*(attempt-1)));
        try { return http(url,body,timeout); }
        catch (const std::exception &e) {
            lastError=e.what();
            // curl reports an HTTP status of its own through CURLOPT_FAILONERROR, which is a
            // reply rather than a failure to reach the server.
            if (lastError.find("HTTP response code")!=std::string::npos) break;
            if (attempt<attempts)
                std::cerr<<"control request failed, retrying ("<<attempt<<'/'<<attempts<<"): "
                         <<lastError<<'\n';
        }
    }
    throw std::runtime_error(lastError);
}
inline std::string controlURL(const Options &o) {
    std::string host=o.get("ip");
    if (host.find(':')!=std::string::npos && host[0]!='[') host="["+host+"]";
    auto f=o.get("f");
    int port=metadata["ports"][f][1];
    if (f=="fib" || f=="gws" || f=="uws_events" || f=="uws_std") ++port;
    return "http://"+host+":"+std::to_string(port);
}
// The pool the server installed, for the report's Pool column. "-" covers a server that
// installs none: the frameworks that take no -taskpool flag, which are not asked, and any whose
// /taskpool route does not answer. Mirrors config.GetFrameworkTaskPool.
inline std::string frameworkTaskPool(const Options &o) {
    const std::string none="-";
    const auto &pooled=metadata["taskpoolFrameworks"];
    if (std::find(pooled.begin(),pooled.end(),o.get("f"))==pooled.end()) return none;
    try {
        std::string name=httpRetry(controlURL(o)+"/taskpool");
        auto first=name.find_first_not_of(" \t\r\n");
        if (first==std::string::npos) return none;
        auto last=name.find_last_not_of(" \t\r\n");
        return name.substr(first,last-first+1);
    } catch (const std::exception &) { return none; }
}
// The language a framework's server is written in, for the report's Lang column, as
// config.FrameworkLang has it.
inline std::string frameworkLang(const std::string &framework) {
    return metadata["langs"].value(framework,std::string("-"));
}
// Only the Go servers have the /debug/pprof routes, as config.FrameworkServesPprof has it, so
// the profile is skipped for every other one rather than asked for and logged as a 404.
inline bool servesPprof(const std::string &framework) { return frameworkLang(framework)=="go"; }
// The hint after the Server PID line, for the servers that have somewhere to point it.
inline std::string pprofHint(const Options &o) {
    return servesPprof(o.get("f")) ? "\npprof: "+controlURL(o)+"/debug/pprof/profile" : "";
}
inline json emptyReport(const std::string &kind,const Options &o) {
    json r=json::object();
    for (const auto &field:metadata["schemas"][kind]) {
        std::string key=field["key"];
        if (field["string"].get<bool>()) r[key]="";
        else r[key]=0;
    }
    r["Framework"]=o.get("f");
    r["Lang"]=frameworkLang(o.get("f"));
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
// shownField is whether a field is printed and tabled at all: a hidden one (md:"-") is only
// in the JSON, and a latency percentile only when -tpn is on.
inline bool shownField(const json &field,const Options &o) {
    return !field["hidden"].get<bool>() && (!field["optional"].get<bool>() || o.boolean("tpn"));
}
// tableColumn is whether a field is a column of its report's table: a run parameter, tagged
// summary:"<name>", is shown once in the Summary table instead, as report.tableColumn has it.
inline bool tableColumn(const json &field,const Options &o) {
    return shownField(field,o) && field["summary"].get<std::string>().empty();
}
inline std::string formatField(const json &r,const json &field) {
    std::string key=field["key"], fmt=field["fmt"];
    // Missing - a report written before the field existed - reads as Go's zero value.
    if (!r.contains(key) || r[key].is_null()) return field["string"].get<bool>()?"":"0";
    const auto &value=r[key];
    if (value.is_string()) {
        auto s=value.get<std::string>();
        // The Client column reads "go" or "uwscpp"; the JSON keeps the full name.
        if (fmt=="client" && s.rfind("benchcli-",0)==0) s.erase(0,9);
        return s;
    }
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
        if (!shownField(field,o)) continue;
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
// textWidth is a cell's width in characters rather than bytes: the [↓1] and [↓2] on the rank
// columns' titles are three bytes each for one character, and counting the bytes would put
// their columns out of line. The same as report.markdownTable in benchcli-go.
inline size_t textWidth(const std::string &s) {
    size_t n=0;
    for (unsigned char c:s) if ((c&0xC0)!=0x80) ++n;
    return n;
}
inline std::string padCell(const std::string &s,size_t maxLen,bool isFirst,size_t titleLeftPaddingIdx) {
    if (textWidth(s)>=maxLen) return s;
    size_t paddingLen=maxLen-textWidth(s);
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
// padLeft mirrors report.padLeft: one space before the cell, the rest after.
inline std::string padLeft(const std::string &s,size_t maxLen,bool,size_t) {
    if (textWidth(s)>=maxLen) return s;
    return " "+s+std::string(maxLen-textWidth(s)-1,' ');
}
// markdownTable mirrors report.markdownTableAligned: with left set, every cell is padded on
// the right, so the table is left-aligned in the console; the separators stay "---".
inline std::string markdownTable(std::vector<std::string> title,std::vector<std::vector<std::string>> rows,
                                 bool left=false) {
    auto pad=left?padLeft:padCell;
    std::vector<size_t> maxLen;
    size_t columnNum=title.size();
    for (auto &v:title) maxLen.push_back(textWidth(v));

    std::vector<std::vector<std::string>> allRows;
    allRows.push_back(std::vector<std::string>(columnNum,"---"));
    for (auto &r:rows) allRows.push_back(std::move(r));

    for (auto &v:allRows) {
        for (size_t j=0;j<v.size();++j) {
            if (maxLen.size()<j+1) maxLen.push_back(textWidth(v[j]));
            else if (textWidth(v[j])>maxLen[j]) maxLen[j]=textWidth(v[j]);
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
        alignedTitle[i]=pad(title[i],maxLen[i],false,0);
        if (i==0)
            for (size_t k=0,at=0;k<alignedTitle[i].size();++k) {
                unsigned char c=alignedTitle[i][k];
                if ((c&0xC0)==0x80) continue;
                if (c!=' ') titleLeftPaddingIdx=at;
                ++at;
            }
        s+=alignedTitle[i]+"|";
    }
    s+="\n";

    for (auto &v:allRows) {
        s+="|";
        for (size_t j=0;j<v.size();++j) s+=pad(v[j],maxLen[j],j==0,titleLeftPaddingIdx)+"|";
        s+="\n";
    }
    return s;
}
// rankKeys is what -sort=result ranks a report by, most significant first: the fields
// tagged rank:"1", rank:"2" and so on in benchcli-go/report, as RankKeys reads them there.
// That is TPS for Connections, and TPS then EER for BenchEcho and BenchRate, whose TPS is
// the packets the clients read back off the server per second.
inline std::vector<json> rankFields(const std::string &kind) {
    std::vector<json> fields;
    for (const auto &field:metadata["schemas"][kind])
        if (field["rank"].get<int>()>0) fields.push_back(field);
    std::stable_sort(fields.begin(),fields.end(),[](const json &a,const json &b) {
        return a["rank"].get<int>()<b["rank"].get<int>();
    });
    return fields;
}
inline double rankValue(const json &r,const json &field) {
    std::string key=field["key"];
    if (!r.contains(key) || !r[key].is_number()) return 0;
    return r[key].get<double>();
}
inline bool rankedBefore(const std::vector<json> &ranks,const json &a,const json &b) {
    for (const auto &field:ranks) {
        double x=rankValue(a,field), y=rankValue(b,field);
        if (x!=y) return x>y;
    }
    return false;
}
// percent mirrors report.Percent: floored, so only the best shows 100%. The best is 100% by
// comparison, since value*100/value can come out a hair under 100 for a float EER, and the
// rest get the same nudge up against the division's rounding, held under 100.
inline std::string percent(double value,double best) {
    if (best<=0 || value<=0) return "0%";
    if (value>=best) return "100%";
    auto p=(long long)std::floor(value*100/best+1e-9);
    return std::to_string(std::min(p,99LL))+"%";
}
// withPercent mirrors report.withPercent: each cell of column col gets its row's share of
// the best after it, the value and the percentage each right-aligned.
inline void withPercent(std::vector<std::vector<std::string>> &rows,size_t col,const std::vector<double> &values) {
    double best=0;
    for (double v:values) best=std::max(best,v);
    std::vector<std::string> percents;
    size_t valueLen=0, percentLen=0;
    for (size_t i=0;i<rows.size();++i) {
        percents.push_back(percent(values[i],best));
        valueLen=std::max(valueLen,rows[i][col].size());
        percentLen=std::max(percentLen,percents[i].size());
    }
    for (size_t i=0;i<rows.size();++i)
        rows[i][col]=std::string(valueLen-rows[i][col].size(),' ')+rows[i][col]+" "+
                     std::string(percentLen-percents[i].size(),' ')+percents[i];
}
// fillRateTPS mirrors BenchRateReport.fillTPS and benchrate.Report: a rate run's TPS is the
// packets the clients read back per second of its duration, floored. A report written before
// it had one gets it here, so that an earlier run still ranks by it when it is read again.
inline void fillRateTPS(json &r) {
    auto number=[&r](const char *key) { return r.contains(key)&&r[key].is_number()?r[key].get<double>():0.0; };
    if (number("TPS")!=0 || number("RecvTimes")<=0) return;
    double duration=number("Duration");
    r["TPS"]=duration>0?int64_t(std::floor(number("RecvTimes")/(duration/1e9))):int64_t(0);
}
inline std::vector<json> readReports(const Options &o,const std::string &kind) {
    std::vector<json> rows;
    for (const auto &f:metadata["frameworks"]) {
        auto path=filename(o,f.get<std::string>()+"-"+kind,".json");
        std::ifstream in(path);
        if (!in) continue;
        try { json row; in>>row; rows.push_back(std::move(row)); }
        catch (const std::exception &e) { throw std::runtime_error(path+": "+e.what()); }
        // A report written before the Lang column existed gets it from the config here.
        auto &row=rows.back();
        if (!row.contains("Lang") || !row["Lang"].is_string() || row["Lang"].get<std::string>().empty())
            row["Lang"]=frameworkLang(f.get<std::string>());
        if (kind=="BenchRate") fillRateTPS(row);
    }
    return rows;
}
struct SummaryValue { std::string value; std::vector<std::string> frameworks; };
// poolSummary mirrors report.poolSummary: the pools that ran, once each, without the
// frameworks, the "-" of those that installed none, or a value's "(...)" suffix.
inline std::string poolSummary(const std::vector<SummaryValue> &values) {
    std::vector<std::string> pools;
    for (const auto &v:values) {
        auto pool=v.value;
        auto paren=pool.find('(');
        if (paren!=std::string::npos && paren>0) pool.resize(paren);
        if (pool=="-" || pool.empty() || std::find(pools.begin(),pools.end(),pool)!=pools.end()) continue;
        pools.push_back(pool);
    }
    if (pools.empty()) return "-";
    std::string text;
    for (size_t i=0;i<pools.size();++i) text+=(i?", ":"")+pools[i];
    return text;
}
// summaryDescription mirrors report.summaryDescription: the Description column's words.
inline std::string summaryDescription(const std::string &name) {
    for (const auto &p:metadata["summaryOrder"])
        if (p["name"]==name) return p["description"];
    return "";
}
// summaryTable mirrors report.Summary: the run's parameters, the summary-tagged fields of every
// row of every report, left-aligned. One value where the rows agree; otherwise each value
// followed by the frameworks that had it, "20000 (fib, fnet); 19998 (fasthttp)" - except Pool,
// which poolSummary writes - and a Description column from summaryDescription, all headed by
// -project's Project row unless it is empty.
inline std::string summaryTable(const Options &o) {
    using Value=SummaryValue;
    std::map<std::string,std::vector<Value>> values;
    std::vector<std::string> names;
    for (auto kind:{"Connections","BenchEcho","BenchRate"})
        for (const auto &r:readReports(o,kind)) {
            std::string framework=r.value("Framework","");
            for (const auto &field:metadata["schemas"][kind]) {
                std::string name=field["summary"];
                if (name.empty()) continue;
                if (!values.count(name)) names.push_back(name);
                auto &list=values[name];
                auto value=formatField(r,field);
                auto it=std::find_if(list.begin(),list.end(),[&](const Value &v){return v.value==value;});
                if (it==list.end()) list.push_back({value,{framework}});
                else if (std::find(it->frameworks.begin(),it->frameworks.end(),framework)==it->frameworks.end())
                    it->frameworks.push_back(framework);
            }
        }
    if (names.empty()) return "";
    std::vector<std::string> ordered;
    for (const auto &p:metadata["summaryOrder"])
        if (values.count(p["name"].get<std::string>())) ordered.push_back(p["name"].get<std::string>());
    for (const auto &name:names)
        if (std::find(ordered.begin(),ordered.end(),name)==ordered.end()) ordered.push_back(name);
    std::vector<std::vector<std::string>> rows;
    if (!o.get("project").empty()) rows.push_back({"Project",o.get("project"),summaryDescription("Project")});
    for (const auto &name:ordered) {
        const auto &list=values[name];
        // A parameter no report carries has no row, as report.Summary has it.
        if (list.size()==1 && list[0].value.empty()) continue;
        std::string text;
        if (name=="Pool") text=poolSummary(list);
        else if (list.size()==1) text=list[0].value;
        else for (size_t i=0;i<list.size();++i) {
            if (i) text+="; ";
            text+=list[i].value+" (";
            for (size_t j=0;j<list[i].frameworks.size();++j) text+=(j?", ":"")+list[i].frameworks[j];
            text+=")";
        }
        rows.push_back({name,text,summaryDescription(name)});
    }
    return markdownTable({"Parameter","Value","Description"},rows,true);
}
// consoleSection mirrors report.ConsoleSection: a rule, the table's name, and the table
// between blank lines.
inline const std::string kLongLine(100,'-');
inline std::string consoleSection(const std::string &name,std::string table) {
    if (table.empty()) table="(no results)\n";
    return kLongLine+"\n["+name+"]\n\n"+table+"\n";
}
inline void generateReports(const Options &o) {
    auto summary=summaryTable(o);
    writeFile(filename(o,"Summary",".md"),summary);
    std::cout<<consoleSection(o.get("preffix")+"Summary"+o.get("suffix"),summary);
    for (auto kind:{"Connections","BenchEcho","BenchRate"}) {
        auto rows=readReports(o,kind);
        // The rows are read in metadata["frameworks"] order, which is
        // config.FrameworkList's, so -sort=framework is already what they are
        // in and only result has anything to do. The sort is stable, which is
        // what leaves a tie - two frameworks that scored the same, or a whole
        // table from a benchmark that did not run and left every row at zero -
        // in framework order rather than wherever the sort landed on.
        auto ranks=rankFields(kind);
        if (o.get("sort")==kSortResult)
            std::stable_sort(rows.begin(),rows.end(),[&ranks](const json &a,const json &b) {
                return rankedBefore(ranks,a,b);
            });
        std::string md;
        if (!rows.empty()) {
            std::vector<json> fields;
            for (const auto &field:metadata["schemas"][kind])
                if (tableColumn(field,o)) fields.push_back(field);
            std::vector<std::string> titles;
            // Rank columns carry " [↓1]", " [↓2]", ... after their names, as report.RankMarker
            // has it.
            for (const auto &f:fields) {
                std::string title=f["title"];
                if (f["rank"].get<int>()>0) title+=" [↓"+std::to_string(f["rank"].get<int>())+"]";
                titles.push_back(title);
            }
            std::vector<std::vector<std::string>> tableRows;
            for (const auto &r:rows) {
                std::vector<std::string> row;
                for (const auto &f:fields) row.push_back(formatField(r,f));
                tableRows.push_back(std::move(row));
            }
            // Every rank column carries each row's share of the best in it, in either order.
            for (const auto &rank:ranks)
                for (size_t col=0;col<fields.size();++col)
                    if (fields[col]["key"]==rank["key"]) {
                        std::vector<double> values;
                        for (const auto &r:rows) values.push_back(rankValue(r,rank));
                        withPercent(tableRows,col,values);
                        break;
                    }
            md=markdownTable(titles,tableRows);
        }
        writeFile(filename(o,kind,".md"),md);
        std::cout<<consoleSection(o.get("preffix")+kind+o.get("suffix"),md);
    }
    std::cout<<kLongLine<<'\n';
}
// PSSetup is how this run reads the server's CPU and memory; see setupPS.
struct PSSetup {
    // The client's own sampler, or null when only the server is sampling.
    std::unique_ptr<LocalPSSampler> local;
    int pid=-1;
    // Whether /init reached the server, i.e. whether /ps has anything to
    // answer with. It is the fallback for a local sampler with no samples yet.
    bool serverSampling=false;
};
// setupPS settles where the samples come from, and starts collecting them.
// Mirrors config.SetupPS: local sampling asks the server nothing at all, so a
// server sampled from here is not asked to sample itself, and everything that
// can go wrong with that ends with the server sampling itself as it always has.
inline PSSetup setupPS(const Options &o) {
    PSSetup ps;
    auto interval=int64_t(o.integer("pi"))*1'000'000;
    auto mode=o.get("ps");
    bool wantLocal=mode==kPSModeLocal || (mode==kPSModeAuto && localHost(o.get("ip")));
    if (wantLocal) {
        try {
            int pid=findServerProcess(o.get("f"));
            ps.local=std::make_unique<LocalPSSampler>(pid,interval);
            ps.pid=pid;
            std::cout<<"Server PID: "<<pid<<" (sampled here, so it is not asked to sample itself)"
                     <<pprofHint(o)<<std::endl;
            return ps;
        } catch (const std::exception &e) {
            std::cerr<<"cannot sample the server from this machine, asking it over HTTP instead: "
                     <<e.what()<<'\n';
        }
    }
    try {
        auto body=json{{"PsInterval",interval}}.dump();
        // A failed /init is not just a missing pid: it is a server that never started
        // sampling, so every CPU and MEM column of the run would read 0.
        auto reply=httpRetry(controlURL(o)+"/init",&body);
        ps.serverSampling=true;
        std::cout<<"Server PID: "<<reply<<pprofHint(o)<<std::endl;
        try { ps.pid=std::stoi(reply); } catch (const std::exception &) { ps.pid=-1; }
    } catch (const std::exception &e) {std::cerr<<"server initialization: "<<e.what()<<'\n';}
    // The pid the server just gave us is from its own namespace, so it names this
    // framework's server here only when the two share one. Where it does, sample it
    // from here as well and keep /ps as the fallback: the numbers no longer depend
    // on that request succeeding again at the end of the run.
    if (wantLocal && ps.pid>0) {
        try {
            verifyServerProcess(ps.pid,o.get("f"));
            ps.local=std::make_unique<LocalPSSampler>(ps.pid,interval);
            std::cout<<"sampling pid "<<ps.pid<<" here as well, with the server's own /ps as the fallback"
                     <<std::endl;
        } catch (const std::exception &) {}
    }
    return ps;
}
// applyResourceStats fills the CPU, MEM and EER columns from a set of samples,
// whoever took them. Min and Avg skip the first sample, and MEM sorts before it
// does, the way github.com/lesismal/perf PSCounter computes the same columns.
inline void applyResourceStats(json &r,std::vector<double> cpu,std::vector<uint64_t> mem,bool rate) {
    if (!cpu.empty()) {
        auto first=cpu.begin()+(cpu.size()>1);
        r["CPUMin"]=*std::min_element(first,cpu.end());
        r["CPUAvg"]=std::accumulate(first,cpu.end(),0.0)/std::distance(first,cpu.end());
        r["CPUMax"]=*std::max_element(cpu.begin(),cpu.end());
    }
    if (!mem.empty()) {
        std::sort(mem.begin(),mem.end());
        auto first=mem.begin()+(mem.size()>1);
        r["MEMMin"]=*first;
        r["MEMAvg"]=std::accumulate(first,mem.end(),uint64_t(0))/uint64_t(std::distance(first,mem.end()));
        r["MEMMax"]=mem.back();
    }
    double avg=r["CPUAvg"].get<double>();
    double tps=rate?r["RecvTimes"].get<double>()/(r["Duration"].get<double>()/1e9):r["TPS"].get<double>();
    r[rate?"EchoEER":"EER"]=avg>0&&std::isfinite(tps)?tps/avg:0.0;
}
inline void resourceStats(json &r,const Options &o,bool rate,const PSSetup &ps) {
    std::vector<double> cpu;
    std::vector<uint64_t> mem;
    std::string trouble;
    if (ps.local) ps.local->samples(cpu,mem);
    // The server's own samples: either it is the only one sampling, or the
    // sampler here has nothing yet - a phase shorter than one -pi interval -
    // and the server was asked to sample as well.
    if (cpu.empty() && (!ps.local || ps.serverSampling)) {
        try {
            auto answer=json::parse(httpRetry(controlURL(o)+"/ps"));
            if (answer.contains("cpu") && answer["cpu"].is_array())
                cpu=answer["cpu"].get<std::vector<double>>();
            if (answer.contains("mem") && answer["mem"].is_array())
                for (const auto &v:answer["mem"]) if (v.is_object()) mem.push_back(v.value("rss",uint64_t(0)));
        } catch (const std::exception &e) { trouble=e.what(); }
    }
    applyResourceStats(r,cpu,mem,rate);
    if (cpu.empty()) {
        std::cerr<<"server resource statistics unavailable, "<<(rate?"EchoEER":"EER")<<" reads 0: ";
        if (!trouble.empty()) std::cerr<<trouble<<'\n';
        else std::cerr<<"nothing was sampled, so either the sampling never started or the phase was"
                        " shorter than the -pi sampling interval\n";
    }
}
inline std::future<void> profile(const Options &o,const std::string &kind,bool enabled,int seconds) {
    if (!enabled || !servesPprof(o.get("f"))) return {};
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
