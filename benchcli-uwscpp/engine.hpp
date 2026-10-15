#pragma once
#include "options.hpp"
#include <WebSocketProtocol.h>
#include <WebSocketHandshake.h>
#include <atomic>
#include <deque>
#include <list>
#include <memory>
#include <mutex>
#include <queue>
#include <sstream>
#include <tuple>
#include <cctype>
#include <random>
#include <vector>
#include <sys/socket.h>
#include <netinet/tcp.h>
#include <netinet/in.h>

// A loop and every socket belonging to it are used only by their worker thread.
// Stage publication/done use release/acquire to make reports and assignments safe.
enum Stage { Dial=1, Warmup, Echo, Rate, Stop };
struct TokenBucket {
    std::mutex mutex;
    double tokens=0;
    int limit=0;
    int64_t last=0;
    void reset(int value) {
        std::lock_guard<std::mutex> guard(mutex);
        limit=value; tokens=value; last=nowNs();
    }
    bool take(int n) {
        if (!limit) return true;
        std::lock_guard<std::mutex> guard(mutex);
        auto now=nowNs();
        tokens=std::min(double(limit),tokens+double(now-last)*limit/1e9); last=now;
        if (tokens<n) return false;
        tokens-=n; return true;
    }
};
// A worker's share of -rl in Rate: rate messages per second, with a one-second burst to start;
// a rate of 0 is no limit. The worker's own rather than one bucket all of them ask in turn,
// which hands each refill to whichever worker happens to ask first. A share smaller than a
// batch sends one once the bucket is full and owes the rest, so it neither starves nor
// exceeds its share over time.
struct RateBucket {
    double rate=0,tokens=0;
    int64_t last=0;
    void reset(double r) {rate=r;tokens=r;last=nowNs();}
    bool take(int n) {
        if (rate==0) return true;
        auto now=nowNs();
        tokens=std::min(rate,tokens+double(now-last)*rate/1e9);last=now;
        if (tokens<std::min(double(n),rate)) return false;
        tokens-=n;return true;
    }
};
struct Stats {
    int64_t success=0,failed=0,sent=0,received=0,recvBytes=0,begin=0,end=0;
    std::vector<int64_t> latency;
};
inline std::string frame(std::string_view payload,uWS::OpCode op=uWS::BINARY) {
    std::string result(payload.size()+14,'\0');
    result.resize(uWS::protocol::formatMessage<false>(result.data(),payload.data(),payload.size(),op,payload.size(),false,true));
    return result;
}
struct Shared {
    std::atomic<int> stage{Dial};
    std::atomic<bool> fatal{false};
    TokenBucket limiter;
    std::vector<std::string> payloads,frames;
    std::string batchFrame;
    int batch=1;
    bool unlimitedRate=false;
    int64_t rateEnd=0,rateStart=0;
    explicit Shared(const Options &o) {
        std::mt19937_64 random(std::random_device{}());
        int size=o.integer("b"); if (!size) size=256;
        if (size>INT32_MAX-14 || (o.number("m") && int64_t(size)*2050>o.number("m")))
            throw std::runtime_error("payload pool exceeds -m memory budget or maximum frame size");
        // Same 1024-message payload pool as benchcli-go; immutable during benchmarking.
        for (int i=0;i<1024;++i) {
            std::string data(size,'\0');
            for (auto &v:data) v=char(random());
            payloads.push_back(std::move(data)); frames.push_back(frame(payloads.back()));
        }
        // Messages per write, as protocol.Pipeline picks them: -rpl when set, or else as many
        // as -rbs bytes hold; at least one, at most -rr and -rl, and a divisor of -rr.
        // -rr=0 is unlimited: every connection sends its batch as fast as it can, so the run
        // measures the server's ceiling rather than the client's send rate. There is no rate
        // to divide then, so only -rpl and -rl bound the batch.
        int rate=std::max(1,o.integer("rr"));
        unlimitedRate=o.integer("rr")==0;
        batch=o.integer("rpl")>0?o.integer("rpl"):int(o.integer("rbs")/frames[0].size());
        batch=std::max(1,unlimitedRate?batch:std::min(rate,batch));
        if (o.integer("rl")>0) batch=std::min(batch,o.integer("rl"));
        if (!unlimitedRate) while (rate%batch) --batch;
        for (int i=0;i<batch;++i) batchFrame+=frames[0];
    }
};
struct Worker;
struct Connection {
    Worker *worker;
    us_socket_t *socket=nullptr;
    uWS::WebSocketState<false> parser;
    int id,attempt=0,payload=0,port=0;
    uint64_t generation=0;
    bool ready=false,completed=false,inflight=false;
    int64_t started=0,echoStarted=0,sent=0,received=0;
    std::string pending,handshake,accept,message,control;
    size_t offset=0;
    std::list<Connection *>::iterator outstanding;
    Connection(Worker *w,int index):worker(w),id(index) {}
};
struct DialEvent {
    int64_t due;
    Connection *conn;
    uint64_t generation;
    bool retry;
    bool operator<(const DialEvent &v) const {return due>v.due;}
};
// cursor, left and due are where the team is in sending its current batch round: the member
// it goes on from, how many members it has still to visit, and when the round is due (the
// interval schedule, which a refused round keeps falling behind). -rl refusing a member leaves
// the round unfinished, to be taken up from that member on the next tick rather than dropped
// until the next interval: every worker then asks the shared bucket each tick while it is
// short, not once an interval, so the tokens do not all go to whichever worker happens to ask
// first, and the members after the refused one are not the ones that always miss out.
struct Team { std::vector<Connection *> conns; size_t cursor=0,left=0; int64_t due=0; };
struct Worker {
    const Options &options;
    Shared &shared;
    int id,workerCount,count,dialConcurrency;
    bool checkValid,enableTPN;
    int firstPort,lastPort,retries,sendRate;
    std::string host;
    // Written by main only while this worker is done with its previous stage.
    int echoConcurrency=0,rateConcurrency=0;
    // This worker's share of -rl, messages per second; 0 is no limit.
    double rateLimit=0;
    RateBucket rateBucket;
    int64_t target=0;
    std::atomic<int> done{0},live{0};
    Stats dialStats,echoStats,rateStats;
    std::thread thread;
    std::string error;
    us_loop_t *loop=nullptr;
    us_socket_context_t *context=nullptr;
    us_timer_t *timer=nullptr;
    int stage=Dial,next=0,connecting=0,completed=0;
    int64_t issued=0,ioTimeout,dialTimeout,retryInterval;
    bool stopping=false,filling=false;
    std::vector<std::unique_ptr<Connection>> connections;
    std::deque<Connection *> idle;
    std::list<Connection *> outstanding;
    std::priority_queue<DialEvent> dialEvents;
    std::vector<Team> teams;
    // (at, served, seq, team): when the team is next looked at. One -rl refused (served false)
    // goes before the ones it did not at the same time, so the teams take turns at the tokens;
    // seq keeps the rest in the order they went last time.
    using TeamEvent=std::tuple<int64_t,bool,uint64_t,size_t>;
    std::priority_queue<TeamEvent,std::vector<TeamEvent>,std::greater<TeamEvent>> teamEvents;
    uint64_t teamSeq=0;
    std::mt19937_64 random{std::random_device{}()};
    Worker(const Options &o,Shared &s,int i,int n,int c,int dc):options(o),shared(s),id(i),workerCount(n),count(c),dialConcurrency(dc),
        checkValid(o.boolean("check")),enableTPN(o.boolean("tpn")),
        firstPort(metadata["ports"][o.get("f")][0]),lastPort(metadata["ports"][o.get("f")][1]),
        retries(o.integer("dr")?o.integer("dr"):3),sendRate(std::max(1,o.integer("rr"))),host(o.get("ip")),
        ioTimeout(o.duration("io-timeout")),dialTimeout(o.duration("dt")),retryInterval(o.duration("dri")) {
        if (host.front()=='[' && host.back()==']') host=host.substr(1,host.size()-2);
        if (!ioTimeout) ioTimeout=30'000'000'000LL;
        if (!dialTimeout) dialTimeout=1'000'000'000LL;
        if (!retryInterval) retryInterval=100'000'000;
    }
    static Connection &conn(us_socket_t *s) {return **static_cast<Connection **>(us_socket_ext(0,s));}
    static void noop(us_loop_t *) {}
    void start() {thread=std::thread([this] { run(); });}
    void run();
    void tick();
    void beginStage(int nextStage);
    void dial(Connection &c);
    void lost(Connection &c);
    void close(Connection &c);
    void fillEcho();
    void finishEcho(Connection &c,bool valid);
    void receive(Connection &c,std::string_view data,uWS::OpCode opcode);
    void consume(Connection &c,char *data,int length);
    void write(Connection &c,std::string_view data);
    void flush(Connection &c);
    void pumpRate(Connection &c);
    void attemptDone(Connection &c,bool success);
    std::string handshakeKey() {
        static constexpr char alphabet[]="ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
        unsigned char bytes[18]={};
        for (int i=0;i<16;++i) bytes[i]=static_cast<unsigned char>(random());
        std::string result;
        for (int i=0;i<18;i+=3) {
            unsigned value=(unsigned(bytes[i])<<16)|(unsigned(bytes[i+1])<<8)|bytes[i+2];
            result+=alphabet[value>>18]; result+=alphabet[(value>>12)&63];
            result+=alphabet[(value>>6)&63]; result+=alphabet[value&63];
        }
        result[22]='=';result[23]='=';return result;
    }
};
struct Protocol {
    static bool setCompressed(uWS::WebSocketState<false> *,void *) {return false;}
    static bool refusePayloadLength(uint64_t size,uWS::WebSocketState<false> *,void *user) {
        auto &c=*static_cast<Connection *>(user);
        return size>std::max<size_t>(125,c.worker->shared.payloads[0].size());
    }
    static void forceClose(uWS::WebSocketState<false> *,void *user,std::string_view) {
        auto &c=*static_cast<Connection *>(user);c.worker->close(c);
    }
    static bool handleFragment(char *data,size_t length,unsigned int remaining,int opcode,bool fin,uWS::WebSocketState<false> *,void *user) {
        auto &c=*static_cast<Connection *>(user);
        auto &buffer=opcode>=8?c.control:c.message;
        size_t maximum=opcode>=8?125:c.worker->shared.payloads[0].size();
        if (buffer.size()+length>maximum) {c.worker->close(c);return true;}
        if (buffer.empty() && fin && !remaining) {
            c.worker->receive(c,std::string_view(data,length),uWS::OpCode(opcode));
        } else {
            buffer.append(data,length);
            if (fin && !remaining) {
                c.worker->receive(c,buffer,uWS::OpCode(opcode));
                buffer.clear();
            }
        }
        return !c.socket;
    }
};
using Parser=uWS::WebSocketProtocol<false,Protocol>;

inline void Worker::write(Connection &c,std::string_view data) {
    if (!c.socket) return;
    if (!c.pending.empty()) {c.pending.append(data);return;}
    int n=us_socket_write(0,c.socket,data.data(),int(data.size()),0);
    if (n<0) {close(c);return;}
    if (size_t(n)<data.size()) {c.pending.assign(data.substr(n));c.offset=0;}
}
inline void Worker::flush(Connection &c) {
    if (!c.socket || c.pending.empty()) return;
    int n=us_socket_write(0,c.socket,c.pending.data()+c.offset,int(c.pending.size()-c.offset),0);
    if (n<0) {close(c);return;}
    c.offset+=size_t(n);
    if (c.offset==c.pending.size()) {c.pending.clear();c.offset=0;pumpRate(c);}
}
// pumpRate sends c its next batch the moment it has room for one, when -rr is unlimited and
// -rl sets no limit. tick() alone runs off a 1 ms timer and sends a connection at most one
// batch a tick, which capped every run at -rc x batch x 1000 messages a second - the same
// number for every server - however fast the server answered.
inline void Worker::pumpRate(Connection &c) {
    if (stage!=Rate || done.load()==Rate || !shared.unlimitedRate || rateLimit>0) return;
    if (!c.ready || !c.pending.empty() || c.sent-c.received+shared.batch>=int64_t(shared.batch)*5) return;
    if (nowNs()>=shared.rateEnd) return;
    write(c,shared.batchFrame);
    if (c.ready) {c.sent+=shared.batch;rateStats.sent+=shared.batch;}
}
inline void Worker::close(Connection &c) {
    if (!c.socket) return;
    if (us_socket_is_established(0,c.socket)) us_socket_close(0,c.socket,0,nullptr);
    else {us_socket_close_connecting(0,c.socket);lost(c);}
}
inline void Worker::attemptDone(Connection &c,bool success) {
    c.completed=true; ++completed; --connecting;
    if (success) {
        ++dialStats.success;
        if (enableTPN) dialStats.latency.push_back(nowNs()-c.started);
    } else ++dialStats.failed;
}
inline void Worker::lost(Connection &c) {
    c.socket=nullptr;
    c.pending.clear();c.offset=0;
    ++c.generation;
    if (c.ready) {c.ready=false;--live;}
    if (stopping) return;
    if (!c.completed) {
        if (c.attempt<retries)
            dialEvents.push({nowNs()+retryInterval,&c,c.generation,true});
        else attemptDone(c,false);
    } else if (c.inflight) finishEcho(c,false);
}
inline void Worker::dial(Connection &c) {
    ++c.attempt;++c.generation;
    c.handshake.clear();c.parser={};
    c.port=firstPort+(int64_t(c.id)+c.attempt)%(lastPort-firstPort+1);
    c.socket=us_socket_context_connect(0,context,host.c_str(),c.port,nullptr,0,sizeof(Connection *));
    if (!c.socket) {lost(c);return;}
    *static_cast<Connection **>(us_socket_ext(0,c.socket))=&c;
    dialEvents.push({nowNs()+dialTimeout,&c,c.generation,false});
}
inline void Worker::finishEcho(Connection &c,bool valid) {
    if (!c.inflight) return;
    c.inflight=false;
    outstanding.erase(c.outstanding);
    if (valid) {
        ++echoStats.success;
        if (enableTPN && stage==Echo) echoStats.latency.push_back(nowNs()-c.echoStarted);
    } else ++echoStats.failed;
    if (c.ready) idle.push_back(&c);
    // Do not recurse into the parser: sends only queue kernel writes.
    fillEcho();
}
inline void Worker::fillEcho() {
    if (filling || done.load()==stage || (stage!=Warmup && stage!=Echo)) return;
    filling=true;
    while (issued<target && outstanding.size()<size_t(echoConcurrency) && !idle.empty()) {
        auto *c=idle.front();
        if (!c->ready) {idle.pop_front();continue;}
        if (!shared.limiter.take(1)) break;
        idle.pop_front();++issued;
        c->payload=int(random()%shared.payloads.size());
        c->echoStarted=nowNs();c->inflight=true;
        outstanding.push_back(c);c->outstanding=std::prev(outstanding.end());
        write(*c,shared.frames[c->payload]);
    }
    if (live.load()==0 || echoConcurrency==0) {
        echoStats.failed+=target-issued;issued=target;
    }
    if (issued==target && outstanding.empty()) {echoStats.end=nowNs();done.store(stage,std::memory_order_release);}
    filling=false;
}
inline void Worker::receive(Connection &c,std::string_view data,uWS::OpCode opcode) {
    if (opcode==uWS::CLOSE) {close(c);return;}
    if (opcode==uWS::PING) {
        if (c.pending.size()>shared.batchFrame.size()+1024) {close(c);return;}
        write(c,frame(data,uWS::PONG));return;
    }
    if (opcode==uWS::PONG) return;
    if (done.load()==stage) return;
    if (stage==Echo || stage==Warmup) {
        if (!c.inflight) {close(c);return;}
        bool valid=!checkValid || (opcode==uWS::BINARY && data==shared.payloads[c.payload]);
        finishEcho(c,valid);
    } else if (stage==Rate) {
        if (!checkValid || (opcode==uWS::BINARY && data==shared.payloads[0])) {
            ++c.received;++rateStats.received;rateStats.recvBytes+=int64_t(data.size());
            pumpRate(c);
        }
    }
}
inline bool validUpgrade(const std::string &header,const std::string &accept) {
    auto end=header.find("\r\n");
    if (end==std::string::npos) return false;
    auto status=header.substr(0,end);
    if (status!="HTTP/1.1 101" && status.rfind("HTTP/1.1 101 ",0)!=0) return false;
    std::map<std::string,std::string> fields;
    size_t start=end+2;
    while ((end=header.find("\r\n",start))!=std::string::npos && end>start) {
        auto line=header.substr(start,end-start); auto colon=line.find(':');
        if (colon==std::string::npos) return false;
        auto key=line.substr(0,colon),value=line.substr(colon+1);
        std::transform(key.begin(),key.end(),key.begin(),[](unsigned char c){return char(std::tolower(c));});
        auto left=value.find_first_not_of(" \t"),right=value.find_last_not_of(" \t");
        value=left==std::string::npos?"":value.substr(left,right-left+1);
        if (fields.count(key)) fields[key]+=","+value; else fields[key]=value;
        start=end+2;
    }
    if (fields["sec-websocket-accept"]!=accept) return false;
    auto upgrade=fields["upgrade"],connection=fields["connection"];
    for (auto *v:{&upgrade,&connection}) std::transform(v->begin(),v->end(),v->begin(),[](unsigned char c){return char(std::tolower(c));});
    std::istringstream tokens(connection);
    std::string token;bool hasUpgrade=false;
    while (std::getline(tokens,token,',')) {
        token.erase(std::remove_if(token.begin(),token.end(),[](unsigned char c){return std::isspace(c);}),token.end());
        if (token=="upgrade") hasUpgrade=true;
    }
    return upgrade=="websocket" && hasUpgrade && !fields.count("sec-websocket-extensions");
}
inline void Worker::consume(Connection &c,char *data,int length) {
    if (c.ready) {Parser::consume(data,unsigned(length),&c.parser,&c);return;}
    c.handshake.append(data,size_t(length));
    auto end=c.handshake.find("\r\n\r\n");
    if (end==std::string::npos) {if (c.handshake.size()>16384) close(c);return;}
    if (end>16384 || !validUpgrade(c.handshake.substr(0,end+4),c.accept)) {close(c);return;}
    c.ready=true;++live;++c.generation;
    attemptDone(c,true);
    auto remaining=c.handshake.substr(end+4);
    c.handshake.clear();
    if (!remaining.empty()) {
        std::vector<char> padded(Parser::CONSUME_PRE_PADDING+remaining.size()+Parser::CONSUME_POST_PADDING);
        auto *p=padded.data()+Parser::CONSUME_PRE_PADDING;
        std::copy(remaining.begin(),remaining.end(),p);
        Parser::consume(p,unsigned(remaining.size()),&c.parser,&c);
    }
}
inline void Worker::beginStage(int nextStage) {
    stage=nextStage;
    if (stage==Warmup || stage==Echo) {
        echoStats={};echoStats.begin=nowNs();issued=0;idle.clear();outstanding.clear();
        for (auto &c:connections) if (c->ready) idle.push_back(c.get());
        fillEcho();
    } else if (stage==Rate) {
        rateStats={};teams.clear();
        int n=std::min(rateConcurrency,live.load());
        teams.resize(size_t(std::max(0,n)));
        size_t i=0;
        for (auto &c:connections) if (c->ready && n) {c->sent=0;c->received=0;teams[i++%size_t(n)].conns.push_back(c.get());}
        // Rate covers [start, end): send the first batch at start, then at each
        // interval. A one-second run therefore sends one full second's quota
        // and leaves time for the final echoes to arrive before its snapshot.
        // Unlimited has no interval: the batches go out as fast as the
        // connections drain them, so only the start event is queued.
        for (size_t j=0;j<teams.size();++j) {teams[j].due=shared.rateStart;teamEvents.push({shared.rateStart,true,teamSeq++,j});}
        rateBucket.reset(rateLimit);
    }
}
inline void Worker::tick() {
    int desired=shared.stage.load(std::memory_order_acquire);
    if (desired==Stop || shared.fatal.load()) {
        stopping=true;
        for (auto &c:connections) close(*c);
        us_timer_close(timer);timer=nullptr;return;
    }
    if (stage!=desired) beginStage(desired);
    auto now=nowNs();
    if (stage==Dial && done.load()!=Dial) {
        if (!dialStats.begin) dialStats.begin=nowNs();
        while (!dialEvents.empty() && dialEvents.top().due<=now) {
            auto event=dialEvents.top();dialEvents.pop();
            auto &c=*event.conn;
            if (c.generation!=event.generation || c.completed) continue;
            if (event.retry) dial(c); else close(c);
        }
        while (next<count && connecting<dialConcurrency) {
            auto &c=*connections[next++];++connecting;c.started=nowNs();dial(c);
        }
        if (completed==count) {
            dialEvents={};dialStats.end=nowNs();done.store(Dial,std::memory_order_release);
        }
    } else if (stage==Warmup || stage==Echo) {
        while (!outstanding.empty() && now-outstanding.front()->echoStarted>=ioTimeout) close(*outstanding.front());
        fillEcho();
    } else if (stage==Rate && done.load()!=Rate) {
        int rate=sendRate;
        auto interval=std::max<int64_t>(1,1'000'000'000LL*shared.batch/rate);
        auto sendThrough=shared.unlimitedRate?now:std::min(now,shared.rateEnd-1);
        // Set once -rl refuses a batch: every batch is the same size, so the rest of this pass
        // would be refused too and is not offered the bucket.
        bool throttled=false;
        while (!teamEvents.empty() && std::get<0>(teamEvents.top())<=sendThrough) {
            auto j=std::get<3>(teamEvents.top());teamEvents.pop();
            auto &team=teams[j];
            if (!team.left) team.left=team.conns.size();
            bool refused=false;
            for (;team.left;team.cursor=(team.cursor+1)%team.conns.size(),--team.left) {
                auto *c=team.conns[team.cursor];
                if (!c->ready || !c->pending.empty() || c->sent-c->received+shared.batch>=int64_t(shared.batch)*5) continue;
                if (throttled || !rateBucket.take(shared.batch)) {throttled=refused=true;break;}
                write(*c,shared.batchFrame);
                if (c->ready) {c->sent+=shared.batch;rateStats.sent+=shared.batch;}
            }
            if (!refused) team.due=shared.unlimitedRate?now+1:std::max(team.due+interval,now+1);
            teamEvents.push({refused?now+1:team.due,!refused,teamSeq++,j});
        }
        if (now>=shared.rateEnd) done.store(Rate,std::memory_order_release);
    }
}
inline void Worker::run() {
    try {
        connections.reserve(size_t(count));
        for (int i=0;i<count;++i) connections.push_back(std::make_unique<Connection>(this,id+i*workerCount));
        loop=us_create_loop(nullptr,noop,noop,noop,0);
        if (!loop) throw std::runtime_error("cannot allocate uSockets loop");
        context=us_create_socket_context(0,loop,0,{});
        if (!context) throw std::runtime_error("cannot allocate uSockets context");
        us_socket_context_on_open(0,context,[](us_socket_t *s,int,char *,int) {
            auto &c=conn(s);auto &w=*c.worker;
            int noDelay=w.options.boolean("nodelay");
            int fd=int(reinterpret_cast<intptr_t>(us_socket_get_native_handle(0,s)));
            setsockopt(fd,IPPROTO_TCP,TCP_NODELAY,&noDelay,sizeof(noDelay));
            auto key=w.handshakeKey();char accept[28];uWS::WebSocketHandshake::generate(key.data(),accept);c.accept.assign(accept,28);
            std::string host=w.options.get("ip");
            if (host.find(':')!=std::string::npos && host.front()!='[') host="["+host+"]";
            w.write(c,"GET /ws HTTP/1.1\r\nHost: "+host+":"+std::to_string(c.port)+"\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: "+key+"\r\nSec-WebSocket-Version: 13\r\n\r\n");
            return s;
        });
        us_socket_context_on_data(0,context,[](us_socket_t *s,char *data,int length) {auto &c=conn(s);c.worker->consume(c,data,length);return s;});
        us_socket_context_on_writable(0,context,[](us_socket_t *s) {auto &c=conn(s);c.worker->flush(c);return s;});
        us_socket_context_on_close(0,context,[](us_socket_t *s,int,void *) {auto &c=conn(s);c.worker->lost(c);return s;});
        us_socket_context_on_connect_error(0,context,[](us_socket_t *s,int) {auto &c=conn(s);c.worker->lost(c);return s;});
        us_socket_context_on_end(0,context,[](us_socket_t *s) {auto &c=conn(s);c.worker->close(c);return s;});
        us_socket_context_on_timeout(0,context,[](us_socket_t *s) {auto &c=conn(s);c.worker->close(c);return s;});
        timer=us_create_timer(loop,0,sizeof(Worker *));
        if (!timer) throw std::runtime_error("cannot allocate uSockets timer");
        *static_cast<Worker **>(us_timer_ext(timer))=this;
        us_timer_set(timer,[](us_timer_t *t){(*static_cast<Worker **>(us_timer_ext(t)))->tick();},1,1);
        us_loop_run(loop);
    } catch (const std::exception &e) {
        error=e.what();shared.fatal.store(true);
        stopping=true;
        for (auto &c:connections) if (c->socket) close(*c);
        if (timer) {us_timer_close(timer);timer=nullptr;}
        // Flush sockets queued for deferred deletion before freeing the loop.
        if (loop && context) us_loop_run(loop);
    }
    if (context) us_socket_context_free(0,context);
    if (loop) us_loop_free(loop);
}
