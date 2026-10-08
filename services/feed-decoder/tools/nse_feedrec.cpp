// nse-feedrec: read-only NSE F&O multicast recorder for feed audits.
//
// Opens its OWN socket on the same multicast group/port as feed-decoder
// (SO_REUSEADDR; the kernel hands every joined socket a copy), so it never
// touches the decoder's hot path. For each 7208/17208 record of a watched
// token it prints one CSV line:
//
//   NSE,rx_unix_ns,token,exch_send_unix_ns,ltt_unix_s,ltp,ltq,volume,bid,ask
//
// exch_send is the packet header's TimeStamp2 (1/65536 s since 1980 IST,
// ~15us resolution); ltt is the record's last-trade time (whole seconds).
// scripts/feed_compare.py reads these files against the Apollo recording.
//
// usage: nse-feedrec SECONDS TOKEN[,TOKEN...] [GROUP] [PORT]
#include "decoder/decompressor.hpp"
#include "decoder/minute_close.hpp"

#include <arpa/inet.h>
#include <netinet/in.h>
#include <sys/socket.h>

#include <chrono>
#include <cstdio>
#include <cstdlib>
#include <set>
#include <sstream>
#include <string>
#include <vector>

namespace {

uint16_t be16(const uint8_t* p) { return static_cast<uint16_t>((p[0] << 8) | p[1]); }
uint32_t be32(const uint8_t* p) {
    return (uint32_t(p[0]) << 24) | (uint32_t(p[1]) << 16) | (uint32_t(p[2]) << 8) | p[3];
}
uint64_t be64(const uint8_t* p) { return (uint64_t(be32(p)) << 32) | be32(p + 4); }

int64_t now_ns() {
    return std::chrono::duration_cast<std::chrono::nanoseconds>(
               std::chrono::system_clock::now().time_since_epoch()).count();
}

constexpr size_t kHeader = 40, kCount = 2, kRecord = 258, kLadder = 68, kLevel = 16;

}  // namespace

int main(int argc, char** argv) {
    if (argc < 3) {
        std::fprintf(stderr, "usage: %s SECONDS TOKEN[,TOKEN...] [GROUP] [PORT]\n", argv[0]);
        return 2;
    }
    const long secs = std::atol(argv[1]);
    std::set<uint32_t> want;
    {
        std::stringstream ss(argv[2]);
        std::string t;
        while (std::getline(ss, t, ',')) {
            if (!t.empty()) want.insert(static_cast<uint32_t>(std::stoul(t)));
        }
    }
    const char* group = argc > 3 ? argv[3] : "233.1.2.5";
    const int port = argc > 4 ? std::atoi(argv[4]) : 34330;

    int fd = socket(AF_INET, SOCK_DGRAM, 0);
    int one = 1;
    setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &one, sizeof one);
    int rcvbuf = 4 << 20;
    setsockopt(fd, SOL_SOCKET, SO_RCVBUF, &rcvbuf, sizeof rcvbuf);
    sockaddr_in addr{};
    addr.sin_family = AF_INET;
    addr.sin_port = htons(static_cast<uint16_t>(port));
    if (bind(fd, reinterpret_cast<sockaddr*>(&addr), sizeof addr) != 0) {
        std::perror("bind");
        return 1;
    }
    ip_mreq mreq{};
    mreq.imr_multiaddr.s_addr = inet_addr(group);
    if (setsockopt(fd, IPPROTO_IP, IP_ADD_MEMBERSHIP, &mreq, sizeof mreq) != 0) {
        std::perror("join");
        return 1;
    }
    timeval tv{1, 0};
    setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &tv, sizeof tv);
    std::fprintf(stderr, "[nse-feedrec] %s:%d, %zu tokens, %lds\n", group, port, want.size(), secs);

    decoder::Decompressor dc;
    static uint8_t buf[65536];
    std::vector<uint8_t> out;
    const int64_t end = now_ns() + int64_t(secs) * 1000000000LL;
    while (now_ns() < end) {
        const ssize_t n = recv(fd, buf, sizeof buf, 0);
        if (n <= 4) continue;
        const int64_t rx = now_ns();
        const uint16_t packets = be16(buf + 2);
        if (packets == 0 || packets > 50) continue;
        size_t pos = 4;
        for (uint16_t i = 0; i < packets; ++i) {
            if (pos + 2 > static_cast<size_t>(n)) break;
            const uint16_t clen = be16(buf + pos);
            pos += 2;
            if (clen == 0 || pos + clen > static_cast<size_t>(n)) break;
            if (dc.decompress(buf + pos, clen, out) && out.size() > 8 + kHeader + kCount) {
                const uint8_t* m = out.data() + 8;
                const size_t len = out.size() - 8;
                const uint16_t tx = be16(m + 10);
                if (tx == 7208 || tx == 17208) {
                    const int64_t send_ns = static_cast<int64_t>(
                        (be64(m + 22) / 65536.0 + decoder::kNseEpochToUnixS) * 1e9);
                    const uint16_t recs = be16(m + kHeader);
                    for (uint16_t r = 0; r < recs; ++r) {
                        const size_t off = kHeader + kCount + size_t(r) * kRecord;
                        if (off + kRecord > len) break;
                        const uint8_t* rec = m + off;
                        if (!want.count(be32(rec))) continue;
                        std::printf("NSE,%lld,%u,%lld,%lld,%.2f,%u,%llu,%.2f,%.2f\n",
                                    static_cast<long long>(rx), be32(rec), static_cast<long long>(send_ns),
                                    static_cast<long long>(decoder::nse_time_to_unix(be32(rec + 30))),
                                    be32(rec + 16) / 100.0, be32(rec + 26),
                                    static_cast<unsigned long long>(be64(rec + 8)),
                                    be32(rec + kLadder) / 100.0, be32(rec + kLadder + 5 * kLevel) / 100.0);
                    }
                    std::fflush(stdout);
                }
            }
            pos += clen;
        }
    }
    return 0;
}
