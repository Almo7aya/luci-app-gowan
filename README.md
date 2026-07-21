# GoWAN — Multi-WAN SOCKS5 Load Balancer for OpenWrt

**A lightweight mwan3 alternative.** GoWAN balances TCP connections across
multiple internet connections from a single SOCKS5 endpoint — with native
per-WAN health checks, instant dial-failure fallback, weighted round-robin,
an nftables ACL, and a full LuCI web interface. One static Go binary, one
UCI config file, zero runtime dependencies.

Built on a vendored, extended snapshot of
[go-dispatch-proxy](https://github.com/extremecoders-re/go-dispatch-proxy)
(MIT — see [gowan/src/UPSTREAM.md](gowan/src/UPSTREAM.md)).

## How it works

Each WAN backend references an OpenWrt logical interface. For every
proxied connection the daemon picks a healthy backend by weighted
round-robin and binds the outbound socket to that interface with
`SO_BINDTODEVICE`, so the whole TCP stream egresses that WAN. A health
goroutine per backend probes through its own interface and flips backends
UP/DOWN in-process — no restarts, active connections survive. If an
outbound dial fails anyway, the daemon retries the next healthy backend
before the client ever sees an error.

```
LAN clients ── SOCKS5 :1080 ──▶ gowan ──▶ wanb (4G router A)
                                     └──▶ wanc (4G router B)
```

## Requirements

- OpenWrt 24.10+ (firewall4/nftables, JS-based LuCI)
- Two or more WAN-capable interfaces configured in `/etc/config/network`
- Clients that can use a SOCKS5 proxy (browsers, torrent clients, download
  managers, most apps). Transparent whole-LAN interception is on the
  roadmap ([PLAN.md](PLAN.md)).

## Install

Download the `gowan` and `luci-app-gowan` packages for your architecture
from [Releases](../../releases), then:

```sh
# OpenWrt 24.10 (opkg)
opkg install ./gowan_*.ipk ./luci-app-gowan_*.ipk

# snapshots (apk)
apk add --allow-untrusted ./gowan-*.apk ./luci-app-gowan-*.apk
```

## Quick start

1. In LuCI: **Network → GoWAN → WAN Backends** — add one entry per WAN
   interface, set contention ratios (a ratio-2 backend gets twice the
   connections of a ratio-1 backend).
2. **Network → GoWAN → Settings** — check the health-check target, then
   set **Enable GoWAN**.
3. Point clients at `socks5://<router-ip>:1080`.

Or via UCI:

```sh
uci batch <<'EOF'
set gowan.wan1=wan
set gowan.wan1.label='4G Router A'
set gowan.wan1.interface='wanb'
set gowan.wan1.ratio='1'
set gowan.wan1.enabled='1'
set gowan.wan2=wan
set gowan.wan2.label='4G Router B'
set gowan.wan2.interface='wanc'
set gowan.wan2.ratio='2'
set gowan.wan2.enabled='1'
set gowan.main.enabled='1'
commit gowan
EOF
/etc/init.d/gowan restart
```

## Notes & limitations

- **TCP only.** UDP (DNS, QUIC, VoIP) follows the router's normal default
  route — functional, just not balanced.
- **Access control:** the SOCKS5 port is guarded by nftables rules built
  from the `acl` UCI sections; the default verdict is deny, and an allow
  rule for your LAN subnet is seeded at install.
- **Health state** lives in `/var/run/gowan/health.json`; the LuCI
  overview and `ubus call gowan status` read it live.
- **Bandwidth numbers** on the overview are whole-interface counters from
  `/proc/net/dev`, not proxy-only traffic.
- Traffic that must survive a WAN change mid-session (banking sites) can
  pin clients per-WAN once policy enforcement lands; see
  [PLAN.md](PLAN.md) for the roadmap.

## Development

- Daemon source (vendored + extended): [`gowan/src/`](gowan/src/) —
  `go test ./...` works out of the box.
- Integration test (root, network namespaces):
  `sudo scripts/netns-smoke.sh` builds the daemon, fakes two WANs, and
  asserts ratio distribution, failover, hook firing, and recovery.
- Release builds run entirely on GitHub Actions via the OpenWrt SDK —
  bump `PKG_VERSION` in `gowan/Makefile` and run the *Build release
  packages* workflow.

Full design document: [PLAN.md](PLAN.md).

## License

MIT. Vendored upstream code remains MIT under its original copyright —
see [gowan/src/LICENSE](gowan/src/LICENSE).
