# GoWAN — Multi-WAN SOCKS5 Load Balancer for OpenWrt

> **A lightweight mwan3 alternative** — SOCKS5-based load balancing across multiple internet connections with full UCI config and LuCI web interface.

---

## Revision History

**Rev 5 (current):** Self-review fixes. (1) **Patches → vendored source:** with the transparent listener, our additions (~350–500 lines) would out-mass the ~500-line frozen upstream, so the source is vendored into `gowan/src/` (MIT, provenance in `src/UPSTREAM.md`) and built from the local directory — normal Go development with unit tests and no patch-file ceremony; the weekly upstream version-check workflow is deleted; the Phase 2 "fork" becomes simply promoting the directory to its own repo. (2) **Testing Strategy section added:** Go unit tests, `go vet`, shellcheck, and a network-namespace failover smoke test, all in a new `ci.yml` on every PR. (3) **Router-originated traffic resilience:** per-WAN dnsmasq upstreams (`server=8.8.8.8@device`) and optional default-route failover driven by the `-on-change` hook (new roadmap item 24). (4) **Packaging hygiene:** `prerm` tears down the service and nft table on uninstall; rpcd validates ubus arguments against UCI sections before they reach any shell. (5) **Docs honesty:** `/proc/net/dev` counters labeled as whole-interface traffic, and unreleased Phase 2 LuCI controls are hidden rather than greyed out.

**Rev 4:** Dual front-door design. **Transparent mode becomes the Phase 2 flagship**: an nft `redirect` rule sends all LAN TCP into a new `-transparent` listener that learns each connection's real destination via `SO_ORIGINAL_DST` — zero client configuration, no TUN device, no userspace TCP stack (~100–150 patched lines). It **replaces the redsocks/tun2socks integration plan**. The SOCKS5 listener stays — it is the only mode that sees destination *domains* (needed for domain policies) and serves opt-in clients; users enable either or both via UCI. A TUN/netstack interface mode ("launch an interface") is parked in Phase 3, pursued only if UDP demand materializes. New section: Transparent Mode — Detailed Spec.

**Rev 3:** Native health checks move into **Phase 1 as package patches** against the pinned upstream commit (the SDK applies `gowan/patches/*.patch` automatically). The shell watchdog is retired; the proxy no longer restarts on WAN state changes; dial-failure fallback (retry the next backend when an outbound connect fails) ships in Phase 1. Health-check config becomes global options in `main` (per-backend tuning returns with the fork's config file), and ICMP checks are dropped in favor of an equivalent interface-bound TCP dial. Sections updated: Architecture, Roadmap, UCI, Core Package, Native Health Checks (replaces the Watchdog spec), Notifications, Risks.

**Rev 2:** Key corrections vs. the original draft, based on verifying upstream behavior and OpenWrt 24.10 realities:

1. **Upstream facts verified** — go-dispatch-proxy is TCP-only, has no auth/health-checks/config-file/hot-reload, latest release is `v7` (Dec 2023). On **Linux it binds via `SO_BINDTODEVICE`** (interface binding), which requires root or `cap_net_raw` — fine under procd (runs as root), and it means egress interface selection works without custom `ip rule` source routing.
2. **WANs are now referenced by OpenWrt interface, not hardcoded source IP** — 4G modems hand out DHCP addresses; hardcoding `source_ip` breaks on lease change. The init script resolves IPs at start via `/lib/functions/network.sh`, and hotplug triggers restart on interface up/down.
3. **Health checks must bind to the WAN interface** (`ping -I`, `curl --interface`) — the original plan's checks went out the default route and tested nothing.
4. **Sticky sessions and generic policy routing moved to Phase 2 (fork)** — the Phase 1 iptables/multi-instance workarounds were fragile and contradictory. Only *client-IP → WAN* policy survives in Phase 1 (via per-WAN instances + nft redirect), marked optional.
5. **LuCI app rewritten for modern JS-based LuCI** — Lua CBI (`luasrc/`, `cbi/`) is deprecated on 24.10; the spec now uses `htdocs/luci-static/resources/view/`, `menu.d`, `acl.d`, and an rpcd backend.
6. **iptables → nftables** — OpenWrt 24.10 ships firewall4/nftables. ACL is now an nft input-chain rule set (clients connect *to* the router, so `INPUT`, not NAT PREROUTING).
7. **Watchdog script bugs fixed** — undefined `$LOCK_DIR`, missing `config_load`, bash-only `/dev/tcp` (OpenWrt is ash/BusyBox), unbound checks, UCI-write churn (flash wear) replaced by runtime state in `/var/run`.
8. **Makefile now uses `golang-package.mk`** from the packages feed instead of a hand-rolled `go build`, pinned to a commit hash.
9. **Connection logging simplified** — go-dispatch-proxy already logs each dispatch to stdout; procd forwards to syslog; LuCI reads `logread`. The conntrack-based logger is dropped (it can't correlate client → destination through a terminating proxy anyway).
10. **Trimmed speculative config** (auth/api sections annotated as Phase 2; `bc` and `speedtest.tele2.net` dependencies removed).

---

## Table of Contents

1. [Overview](#overview)
2. [Architecture](#architecture)
3. [Feature Roadmap](#feature-roadmap)
4. [UCI Config Design](#uci-config-design)
5. [Package Structure](#package-structure)
6. [Core Package — Detailed Spec](#core-package--detailed-spec)
7. [LuCI App — Detailed Spec](#luci-app--detailed-spec)
8. [Native Health Checks — Detailed Spec](#native-health-checks--detailed-spec)
9. [ACL / Access Control — Detailed Spec](#acl--access-control--detailed-spec)
10. [Transparent Mode — Detailed Spec](#transparent-mode--detailed-spec)
11. [Policy-Based Routing — Detailed Spec](#policy-based-routing--detailed-spec)
12. [Sticky Sessions — Detailed Spec](#sticky-sessions--detailed-spec)
13. [Failover Notifications — Detailed Spec](#failover-notifications--detailed-spec)
14. [Speed Test Per WAN — Detailed Spec](#speed-test-per-wan--detailed-spec)
15. [Phase 2 Fork — SOCKS5 Auth & Status API](#phase-2-fork--socks5-auth--status-api)
16. [Logging & Diagnostics — Detailed Spec](#logging--diagnostics--detailed-spec)
17. [Testing Strategy](#testing-strategy)
18. [CI/CD — Build Pipeline](#cicd--build-pipeline)
19. [Versioning & Release Strategy](#versioning--release-strategy)
20. [Build Strategy](#build-strategy)
21. [Naming Convention](#naming-convention)
22. [Risks & Known Limitations](#risks--known-limitations)
23. [Open Questions](#open-questions)

---

## Overview

**GoWAN** wraps [go-dispatch-proxy](https://github.com/extremecoders-re/go-dispatch-proxy) into a proper OpenWrt UCI-managed multi-WAN solution with full LuCI web interface.

### Upstream Reality Check (verified)

| Fact | Consequence for GoWAN |
|------|----------------------|
| SOCKS5, **TCP only** — no UDP ASSOCIATE | DNS, QUIC/HTTP3, VoIP won't flow through the proxy. Clients must resolve DNS locally (default for SOCKS5h-unaware apps) or via the router. UDP support = fork work. |
| Linux mode uses `SO_BINDTODEVICE` (needs root or `setcap cap_net_raw`) | procd runs it as root — no setcap needed. Egress is forced out the bound interface, so **no `ip rule` source-routing setup is required**. |
| Selection: round-robin weighted by contention ratio (`IP@ratio`) | Weights map directly to UCI `option ratio`. |
| No health checks, no auth, no config file, no hot reload, no status API | Health checks + dial fallback are **implemented in the vendored source** (Phase 1, `gowan/src/`). Auth, hot reload, status API remain Phase 2. |
| Also has `-tunnel` SSH-tunnel mode | Out of scope. |
| Latest release: `v7` (Dec 2023); sparse maintenance | Source vendored into `gowan/src/` (MIT allows it; provenance in `src/UPSTREAM.md`). The vendored dir is the de-facto fork seed. |

### Why GoWAN over mwan3?

| Aspect | mwan3 | GoWAN |
|--------|-------|-------|
| **Mechanism** | Kernel routing tables + firewall marks | SOCKS5 proxy with per-connection interface binding |
| **Scope** | All traffic, all protocols | All LAN TCP (transparent mode, Phase 2) and/or opt-in SOCKS5 clients (Phase 1); UDP goes direct |
| **Complexity** | Heavy — multiple routing tables, rules, hotplug scripts | Light — single static Go binary, one UCI file |
| **Connection handling** | Per-packet/per-connection marks; NAT breakage possible | Clean per-connection dispatch; each TCP stream exits one WAN |
| **UDP support** | Full | None in Phase 1 (fork work later) |
| **Health checks** | ICMP ping only | Native in-process TCP/HTTP checks + dial-failure fallback |
| **Config** | Complex multi-file config | Single UCI config file |
| **Status visibility** | Basic LuCI page | Rich LuCI dashboard with per-WAN stats |

**Honest positioning:** in Phase 1, GoWAN balances *TCP connections from clients configured to use the proxy*. Once transparent mode lands (Phase 2), it balances **all LAN TCP with zero client configuration** — a genuine mwan3 replacement for TCP — while UDP continues out the normal default route. It stays dramatically simpler and immune to the routing-table fragility that plagues mwan3.

### Design Philosophy

- **Single binary, zero runtime dependencies** — the core dispatcher is a static Go binary (no "Go runtime"; Go compiles to native code)
- **UCI-first** — everything configurable via `/etc/config/gowan`, LuCI is just a UI on top
- **Two front doors, one core** — a SOCKS5 listener (explicit, domain-aware) and a transparent listener (zero-config, Phase 2) feed the same dispatch engine; users enable either or both
- **Phase 1: Vendor + extend, Phase 2: Promote** — upstream's ~500 MIT-licensed lines are vendored into `gowan/src/` and extended in place (health checks, dial fallback, transparent listener); the directory graduates to a standalone repo when it earns it
- **Interfaces, not IPs** — WANs reference OpenWrt logical interfaces; IPs are resolved at runtime (DHCP-safe)
- **Graceful degradation** — the daemon marks a failing WAN down and skips it; a failed outbound dial falls back to the next healthy WAN
- **No flash wear** — runtime state (health, sticky, stats) lives in `/var/run/gowan/`, never written back to UCI

---

## Architecture

```
┌──────────────────────────────────────────────────────────┐
│                      LAN CLIENTS                          │
│   (SOCKS5-configured apps today; in Phase 2 ALL LAN TCP   │
│    is intercepted transparently — no client config)       │
└──────────────────────┬───────────────────────────────────┘
                       │ TCP → 10.0.1.1:1080
                       ▼
┌──────────────────────────────────────────────────────────┐
│                    GoWAN Gateway                          │
│                                                           │
│  nftables ACL (input chain) — allow/deny client subnets   │
│                         │                                 │
│                         ▼                                 │
│  ┌──────────────────────────────────────────────────┐    │
│  │     gowan daemon (patched go-dispatch, :1080)     │    │
│  │   weighted round-robin over UP backends           │    │
│  │   SO_BINDTODEVICE per backend interface           │    │
│  │   health goroutine per backend (TCP/HTTP check    │    │
│  │     bound to its iface) → atomic UP/DOWN flips    │    │
│  │   dial-failure fallback → next healthy backend    │    │
│  │   state change → health.json + on-change hook     │    │
│  └──┬──────────┬──────────┬──────────┬──────────────┘    │
│     │          │          │          │                    │
│     ▼          ▼          ▼          ▼                    │
│   wanA       wanB       wanC       wanD   ← OpenWrt       │
│                                             interfaces    │
└──────────────────────────────────────────────────────────┘
```

### Request Flow (Phase 1)

1. LAN client opens a TCP connection to `10.0.1.1:1080` (SOCKS5)
2. **nftables ACL** — connection from a denied subnet is rejected before it reaches the proxy
3. *(optional)* **Client-IP policy** — an nft rule redirects pinned clients to a per-WAN proxy instance on a dedicated port
4. **Load balance** — the daemon selects a backend by weighted round-robin among backends its health goroutines currently mark UP
5. **Dial fallback** — if the outbound connect fails anyway, the daemon retries the next healthy backend before failing the client
6. Connection established; the whole TCP stream flows out the selected WAN interface

*(In Phase 2, ACL/policy/sticky also move inside the forked binary, and a second, transparent entry point appears: an nft rule redirects LAN TCP to a `-transparent` listener that learns the destination from `SO_ORIGINAL_DST` instead of a SOCKS5 handshake, then joins the same flow at step 4. Health is already in-process as of Phase 1.)*

---

## Feature Roadmap

### Phase 1 — Wrap (MVP, ship fast)

| # | Feature | Where | Description |
|---|---------|-------|-------------|
| 1 | Core package | `gowan/` | Vendored upstream source in `gowan/src/`, built via `golang-package.mk` |
| 2 | UCI config | `gowan/files/` | WAN backends by interface, ratios, global settings |
| 3 | Init script | `gowan/files/` | procd init; resolves interface IPs at start; nft ACL setup; hotplug restart on iface up/down |
| 4 | Native health checks | `gowan/src/` | In-daemon, unit-tested: per-backend TCP/HTTP check goroutines bound to the interface, fail/rise thresholds, in-process UP/DOWN (no restarts), dial-failure fallback, health.json + on-change hook |
| 5 | LuCI status page | `luci-app-gowan/` | JS view: proxy state, per-WAN health + RX/TX, recent connections |
| 6 | LuCI WAN management | `luci-app-gowan/` | Add/edit/remove backends, ratios, health-check params |
| 7 | LuCI global settings + ACL | `luci-app-gowan/` | Listen host/port, enable, health-check settings; allow/deny subnets |
| 8 | Bandwidth monitoring | rpcd script | Per-WAN RX/TX from `/proc/net/dev` (interface counters) |
| 9 | ACL / access control | init + nftables | Allow/deny client IPs/CIDRs on the input chain |
| 10 | Connection logging | procd → syslog | Daemon stdout → logread; LuCI log viewer filters `logread -e gowan` |
| 11 | CI/CD | GitHub Actions | Release: SDK matrix build for ~25 archs × {24.10 ipk, snapshot apk}. PRs: go test, shellcheck, netns failover smoke test (see Testing Strategy) |
| 12 | *(optional)* Client-IP policy | init + nftables | Pin a client IP to a WAN via per-WAN instance + nft redirect. Only if it stays simple. |

### Phase 2 — Fork & Enhance

| # | Feature | Description |
|---|---------|-------------|
| 13 | **Transparent proxy mode** ✅ *shipped in v0.2.0* | Flagship. nft `redirect` + `-transparent` listener reading `SO_ORIGINAL_DST`: every LAN TCP connection balanced with zero client config. Includes QUIC-block rule (UDP/443 → TCP fallback). SOCKS5 listener remains alongside. Replaces the old redsocks/tun2socks plan. Verified on hardware 2026-07-21. |
| 14 | Promote `gowan/src/` to standalone repo | When it earns it (transparent mode shipped, or outside interest); auth, policy, sticky land there |
| 15 | Per-backend check config ✅ *shipped in v0.3.0* | Backends file (`/var/run/gowan/backends.json`, rendered from UCI) carries optional per-WAN check overrides; absent fields inherit global flags |
| 16 | Hot-reload via SIGHUP ✅ *shipped in v0.3.0* | Daemon re-reads the backends file on SIGHUP and swaps the set without dropping connections; surviving backends keep health + counters. Init reloads via signal unless a listener-affecting option changed; hotplug WAN-IP changes are now seamless |
| 17 | JSON status API ✅ *shipped in v0.3.0* | `-api 127.0.0.1:9080` → `GET /status`: uptime, per-backend health and connection counters; localhost-only |
| 18 | Policy routing (client-IP) ✅ *shipped in v0.4.0* | client-IP / CIDR → WAN, evaluated in-process; down-backend falls back to healthy. domain/port/dest-IP still planned |
| 19 | Sticky sessions ✅ *shipped in v0.4.0* | In-memory client-IP → WAN map with TTL, refreshed on use, skips down backends |
| 20 | SOCKS5 authentication ✅ *shipped in v0.4.0* | RFC 1929 username/password (SOCKS5 listener only; transparent mode is ACL-guarded); constant-time compare |
| 21 | Failover notifications ✅ *shipped in v0.4.0* | `notify.sh` on the daemon's `-on-change` hook fires Telegram/Discord/webhook on WAN state change |
| 22 | Speed test per WAN ✅ *shipped in v0.4.0* | LuCI button → rpcd → `curl --interface`, result inline; verified on hardware |
| 23 | Live throughput + graphs ✅ *shipped in v0.4.0* | Overview computes per-WAN down/up rate from /proc/net/dev deltas; rolling multi-line SVG chart (no deps) |
| 24 | Structured logging | JSON logs to syslog; per-connection bytes/duration |
| 25 | Router-traffic resilience | Per-WAN dnsmasq upstreams (`server=8.8.8.8@device`) + optional default-route failover driven by the `-on-change` hook |

### Phase 3 — Only If Demand Materializes

| # | Feature | Description |
|---|---------|-------------|
| 25 | TUN interface mode | Full L3 coverage including UDP, via gVisor netstack or pairing with stock tun2socks. Heavy (RAM/binary size); pursued only if transparent-TCP mode proves insufficient. |
| 26 | UDP ASSOCIATE | SOCKS5 UDP relay in the fork |
| 27 | Per-WAN traffic graphs | Historical bandwidth via rrdtool/luci-app-statistics integration |
| 28 | IPv6 | v6 backends, listeners, and interception rules |

---

## UCI Config Design

### Full `/etc/config/gowan` schema

```bash
# =============================================================================
# Global settings
# =============================================================================
config gowan 'main'
    option enabled '1'              # Master enable/disable (0|1)
    option listen_host '0.0.0.0'    # SOCKS5 listen address (LAN-reachable by design; ACL guards it)
    option listen_port '1080'       # SOCKS5 listen port
    option acl_default 'deny'       # Default ACL verdict when no rule matches (allow|deny)
                                    # 'deny' + an allow rule for the LAN subnet is the safe default
    option log_connections '1'      # Log per-connection dispatch lines to syslog (0|1)

    # Health checks — GLOBAL in Phase 1, mapped to daemon CLI flags;
    # every backend runs the same check bound to its own interface.
    # Per-backend tuning arrives with the fork's config file.
    option check_type 'tcp'          # tcp|http|none
    option check_target '8.8.8.8:53' # host:port for tcp, URL for http
    option check_interval '30'       # Seconds between checks
    option check_timeout '5'         # Seconds
    option check_fail_threshold '3'  # Consecutive failures before DOWN
    option check_rise_threshold '2'  # Consecutive successes before UP

    # Transparent mode (Phase 2) — intercept ALL LAN TCP, no client config.
    # Both front doors can run at once; SOCKS5 stays the domain-aware one.
    option transparent '0'                    # Enable transparent interception (0|1)
    option transparent_port '1081'            # Internal redirect target (clients never use it)
    list   transparent_subnet '10.0.1.0/24'  # Source subnets to intercept
    option block_quic '1'                     # Block UDP/443 from intercepted subnets → TCP fallback
    option failover_default_route '0'         # Repoint the router's OWN default route at a
                                              # healthy WAN on state change (mwan3-lite for
                                              # UDP + router-originated traffic)

# =============================================================================
# WAN backend definitions — referenced by OpenWrt logical interface.
# The init script resolves the interface's current IPv4 address and device
# name at start via /lib/functions/network.sh. No hardcoded IPs.
# =============================================================================
config wan 'wan1'
    option label '4G Router .3'      # Human-readable name (shown in LuCI)
    option interface 'wanb'          # OpenWrt logical interface (from /etc/config/network)
    option ratio '1'                 # Contention ratio (weight in round-robin)
    option enabled '1'

config wan 'wan2'
    option label '4G Router .4'
    option interface 'wanc'
    option ratio '2'                 # Double weight — gets 2x connections
    option enabled '1'

# =============================================================================
# ACL — evaluated as nftables rules on the router's input chain.
# Rules are applied in config order; first match wins; acl_default applies last.
# =============================================================================
config acl
    option enabled '1'
    option verdict 'allow'           # allow|deny
    list subnet '10.0.1.0/24'
    option description 'LAN clients'

config acl
    option enabled '1'
    option verdict 'deny'
    list subnet '10.0.99.0/24'
    option description 'Guest WiFi — no proxy access'

# =============================================================================
# Policy rules — Phase 1 supports ONLY type 'client_ip' (optional feature #12).
# domain/port/dest_ip types are accepted in config but ignored until Phase 2.
# =============================================================================
config policy
    option enabled '1'
    option name 'My laptop via wan1'
    option type 'client_ip'          # Phase 2 adds: domain|port|dest_ip
    option match '10.0.1.100'
    option wan 'wan1'

# =============================================================================
# Notifications (Phase 2)
# =============================================================================
config notify 'alerts'
    option enabled '0'
    option type 'telegram'           # telegram|discord|webhook
    option telegram_bot_token ''
    option telegram_chat_id ''
    option webhook_url ''
    option on_wan_down '1'
    option on_wan_up '1'
    option on_all_down '1'

# =============================================================================
# SOCKS5 Authentication (Phase 2)
# =============================================================================
config auth 'proxy_auth'
    option enabled '0'
    option username ''
    option password ''               # NOTE: UCI files are root-readable plaintext;
                                     # acceptable on a router, document it

# =============================================================================
# Status API (Phase 2)
# =============================================================================
config api 'status_api'
    option enabled '0'
    option listen_host '127.0.0.1'   # localhost-only; rpcd proxies it to LuCI
    option listen_port '9080'
```

### UCI Sections Summary

| Section type | Multiplicity | Purpose | Phase |
|--------------|--------------|---------|-------|
| `gowan` (named `main`) | single | Global settings | 1 |
| `wan` | many | WAN backend, referenced by OpenWrt interface | 1 |
| `acl` | many | Allow/deny client subnets (nftables) | 1 |
| `policy` | many | Policy routing (client_ip only in Phase 1) | 1 (partial) |
| `notify` | single | Failover notifications | 2 |
| `auth` | single | SOCKS5 credentials | 2 |
| `api` | single | Status API | 2 |

**Dropped from the original schema:** `list wan` references in `main` (redundant — `config_foreach` iterates all `wan` sections); `source_ip` (replaced by `interface`); `sticky*` options (Phase 2); all per-WAN `check_*` options (checks are global daemon CLI flags; per-backend tuning returns with the config file); the `icmp` check type (raw sockets would pull `golang.org/x/net` into the build — an interface-bound TCP dial to a public resolver is an equivalent liveness signal); `notify_on_change` per WAN (global toggles suffice); `log_file` (syslog via procd instead).

---

## Package Structure

```
gowan/                              # Root repository
├── PLAN.md                         # This document
├── README.md                       # Install, quick start, config reference, FAQ
├── LICENSE                         # MIT
├── .github/
│   └── workflows/
│       ├── build.yml               # SDK matrix build → release artifacts
│       └── ci.yml                  # PR checks: go vet/test, shellcheck, netns smoke test
│
├── gowan/                          # OpenWrt package: gowan
│   ├── Makefile                    # golang-package.mk based, pinned commit
│   ├── src/                        # Vendored Go source — upstream snapshot + our modules
│   │   ├── UPSTREAM.md                 # provenance: upstream repo, commit, MIT attribution
│   │   ├── LICENSE                     # upstream MIT license (preserved)
│   │   ├── go.mod                      # module gowan — zero external dependencies
│   │   ├── main.go, servers.go, …      # upstream files (SOCKS5, SO_BINDTODEVICE dialing)
│   │   ├── health.go                   # check goroutines, thresholds, skip-DOWN selector
│   │   ├── state.go                    # health.json writer + -on-change exec hook
│   │   ├── transparent.go              # Phase 2: SO_ORIGINAL_DST listener
│   │   └── *_test.go                   # unit tests (thresholds, selector, dial fallback)
│   └── files/
│       ├── gowan.config            # UCI template → /etc/config/gowan
│       ├── gowan.init              # procd init: builds daemon args, nft ACL
│       ├── gowan.defaults          # uci-defaults: seed ACL allow rule for LAN subnet
│       ├── gowan-functions.sh      # Shared shell lib (IP resolution, nft ACL emit)
│       ├── gowan-rpcd              # rpcd backend → /usr/libexec/rpcd/gowan
│       │                           #   methods: status, stats, log, speedtest (Ph2)
│       ├── gowan-hotplug           # /etc/hotplug.d/iface/99-gowan (reload on WAN up/down)
│       └── gowan-speedtest.sh      # Phase 2
│
├── luci-app-gowan/                 # OpenWrt package: luci-app-gowan (JS, no Lua)
│   ├── Makefile                    # luci.mk based
│   ├── htdocs/luci-static/resources/view/gowan/
│   │   ├── overview.js             # Status dashboard (polls ubus gowan status/stats)
│   │   ├── wans.js                 # WAN backends form (form.Map over uci gowan)
│   │   ├── acl.js                  # ACL rules form
│   │   ├── policy.js               # Policy rules form (client_ip only until Phase 2)
│   │   ├── log.js                  # Connection log viewer (ubus gowan log)
│   │   └── settings.js             # Global settings form
│   └── root/usr/share/
│       ├── luci/menu.d/luci-app-gowan.json    # Menu: Network → GoWAN → …
│       └── rpcd/acl.d/luci-app-gowan.json     # Grants: uci gowan, ubus gowan.*
│
└── scripts/
    └── netns-smoke.sh              # CI integration test: veth "WANs", failover assertions
```

---

## Core Package — Detailed Spec

### `gowan/Makefile` — Package Definition

Uses the packages feed's Go infrastructure instead of a hand-rolled `go build` (correct GOARCH/GOARM/GOMIPS/soft-float handling for all ~25 targets comes free):

```makefile
include $(TOPDIR)/rules.mk

PKG_NAME:=gowan
PKG_VERSION:=0.1.0
PKG_RELEASE:=1

# Source is VENDORED in ./src — upstream go-dispatch-proxy snapshot (MIT),
# provenance in src/UPSTREAM.md. No download step, no PKG_MIRROR_HASH.
PKG_LICENSE:=MIT
PKG_LICENSE_FILES:=src/LICENSE
PKG_MAINTAINER:=Ali Almohaya

PKG_BUILD_DEPENDS:=golang/host
PKG_BUILD_PARALLEL:=1
PKG_BUILD_FLAGS:=no-mips16

GO_PKG:=gowan
GO_PKG_LDFLAGS:=-s -w

include $(INCLUDE_DIR)/package.mk
include $(TOPDIR)/feeds/packages/lang/golang/golang-package.mk

define Build/Prepare
	mkdir -p $(PKG_BUILD_DIR)
	$(CP) ./src/* $(PKG_BUILD_DIR)/
endef

define Package/gowan
  SECTION:=net
  CATEGORY:=Network
  TITLE:=GoWAN - Multi-WAN SOCKS5 load balancer
  URL:=https://github.com/<owner>/gowan
  DEPENDS:=$(GO_ARCH_DEPENDS)
endef

define Package/gowan/description
  Multi-WAN SOCKS5 load balancing proxy for OpenWrt, built on
  go-dispatch-proxy: health-checked weighted round-robin across
  multiple WAN interfaces, UCI-configured.
endef

define Package/gowan/install
	$(INSTALL_DIR) $(1)/usr/sbin
	$(INSTALL_BIN) $(GO_PKG_BUILD_BIN_DIR)/gowan $(1)/usr/sbin/gowan

	$(INSTALL_DIR) $(1)/etc/config
	$(INSTALL_CONF) ./files/gowan.config $(1)/etc/config/gowan

	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_BIN) ./files/gowan.init $(1)/etc/init.d/gowan

	$(INSTALL_DIR) $(1)/etc/uci-defaults
	$(INSTALL_BIN) ./files/gowan.defaults $(1)/etc/uci-defaults/90-gowan

	$(INSTALL_DIR) $(1)/etc/hotplug.d/iface
	$(INSTALL_BIN) ./files/gowan-hotplug $(1)/etc/hotplug.d/iface/99-gowan

	$(INSTALL_DIR) $(1)/usr/lib/gowan
	$(INSTALL_DATA) ./files/gowan-functions.sh $(1)/usr/lib/gowan/functions.sh

	$(INSTALL_DIR) $(1)/usr/libexec/rpcd
	$(INSTALL_BIN) ./files/gowan-rpcd $(1)/usr/libexec/rpcd/gowan
endef

define Package/gowan/conffiles
/etc/config/gowan
endef

define Package/gowan/prerm
#!/bin/sh
[ -x /etc/init.d/gowan ] && /etc/init.d/gowan stop
nft delete table inet gowan 2>/dev/null
exit 0
endef

$(eval $(call BuildPackage,gowan))
```

Notes:
- **Vendored source:** upstream's ~500 MIT-licensed lines live in `gowan/src/` alongside our modules (`health.go`, `state.go`, later `transparent.go`) — normal Go development with unit tests, IDE support, and zero patch-file ceremony. `src/UPSTREAM.md` records the upstream repo, commit, and attribution. `go.mod` has no external dependencies, so no Go vendoring machinery is needed.
- **Zero runtime dependencies:** health checks are in-process Go (interface-bound `net.Dialer` / `http.Client`) — no ping/nc/curl needed. curl becomes an optional dep only when the Phase 2 speedtest lands.
- **Uninstall hygiene:** `prerm` stops the service and deletes the `inet gowan` nft table so nothing lingers after removal.
- **Phase 2:** when `src/` graduates to its own repo, either flip to `PKG_SOURCE_URL` downloads or keep vendoring — decide then; nothing else changes.

### `gowan/files/gowan.init` — procd Init Script

One procd instance — the daemon owns health internally. The init script resolves WAN interface IPs, maps the global `check_*` UCI options to CLI flags, applies the nft ACL, and gets out of the way.

```bash
#!/bin/sh /etc/rc.common

START=95
STOP=10
USE_PROCD=1
PROG=/usr/sbin/gowan

. /usr/lib/gowan/functions.sh    # gowan_resolve_wan, gowan_apply_acl

start_service() {
    config_load gowan

    local enabled listen_host listen_port
    local ctype ctarget cint ctmo cfail crise
    config_get_bool enabled main enabled 1
    [ "$enabled" -eq 0 ] && return 0

    config_get listen_host main listen_host '0.0.0.0'
    config_get listen_port main listen_port '1080'
    config_get ctype   main check_type 'tcp'
    config_get ctarget main check_target '8.8.8.8:53'
    config_get cint    main check_interval 30
    config_get ctmo    main check_timeout 5
    config_get cfail   main check_fail_threshold 3
    config_get crise   main check_rise_threshold 2

    mkdir -p /var/run/gowan

    # Build "<ip>@<ratio>" for every enabled WAN whose interface is up.
    # Health is the daemon's job — no state filtering here.
    WAN_ARGS=""
    config_foreach append_wan_arg wan
    if [ -z "$WAN_ARGS" ]; then
        logger -t gowan -p daemon.err "no usable WAN backends; not starting"
        return 1
    fi

    CHECK_ARGS=""
    [ "$ctype" != "none" ] && CHECK_ARGS="-check-type $ctype -check-target $ctarget \
        -check-interval $cint -check-timeout $ctmo -check-fail $cfail -check-rise $crise \
        -state-file /var/run/gowan/health.json"

    gowan_apply_acl "$listen_port"   # nftables ACL (see ACL spec)

    procd_open_instance proxy
    procd_set_param command $PROG -lhost "$listen_host" -lport "$listen_port" $CHECK_ARGS $WAN_ARGS
    procd_set_param respawn 3600 5 0
    procd_set_param stdout 1
    procd_set_param stderr 1
    procd_close_instance
}

append_wan_arg() {
    local section="$1" enabled iface ratio ip
    config_get_bool enabled "$section" enabled 1
    [ "$enabled" -eq 0 ] && return 0

    config_get iface "$section" interface
    config_get ratio "$section" ratio 1
    gowan_resolve_wan "$iface" ip || {           # network_get_ipaddr wrapper
        logger -t gowan "WAN $section: interface $iface has no IPv4, skipping"
        return 0
    }
    WAN_ARGS="$WAN_ARGS ${ip}@${ratio}"
}

stop_service() {
    gowan_teardown_acl
}

service_triggers() {
    procd_add_reload_trigger gowan
}

reload_service() {
    stop
    start
}
```

- **Hotplug** (`/etc/hotplug.d/iface/99-gowan`): on `ifup`/`ifdown` of an interface referenced by any `wan` section, `/etc/init.d/gowan reload`, **debounced** — events arrive in bursts at boot (one per WAN), so a burst coalesces into a single reload 5 s after the first event. This handles DHCP renewals that change the WAN IP — the one remaining event that still restarts the daemon (health flaps no longer do). SIGHUP hot-reload in Phase 2 removes even that.

---

## LuCI App — Detailed Spec

**Modern JS LuCI only** (OpenWrt 24.10 target). No `luasrc/`, no CBI Lua models, no `luci-compat` dependency. Pages are client-side `view.js` modules; config editing goes through the standard `uci` ubus interface; runtime data comes from a small rpcd backend.

### rpcd Backend (`/usr/libexec/rpcd/gowan`)

Shell script implementing ubus object `gowan`:

| Method | Returns |
|--------|---------|
| `status` | proxy running (from `ubus call service list`), listen addr, per-WAN: label, interface, resolved IP, ratio, health state + since (from `/var/run/gowan/health.json`, written by the daemon) |
| `stats` | per-WAN RX/TX bytes and packets from `/proc/net/dev` for each WAN's device — whole-interface counters, so LuCI labels them "interface traffic", never "proxy traffic" |
| `log` | last N `logread -e gowan` lines, parsed to `{time, message}` |
| `speedtest` | *(Phase 2)* runs `gowan-speedtest.sh <section>`, returns `{mbps, latency_ms}` |

Every method validates caller arguments against the actual UCI section list (`config_foreach`) before use — ubus input never reaches shell interpolation. The `speedtest` `section` argument in particular is matched against existing `wan` section names, not passed through.

### Pages & Menu

`menu.d/luci-app-gowan.json` registers **Network → GoWAN** with tabs:

| Path | View | Content |
|------|------|---------|
| `admin/network/gowan` | `overview.js` | Dashboard: proxy state badge, WAN table (health dot, IP, ratio, RX/TX), recent log lines. `L.Poll`-based refresh every 5s via `ubus gowan status/stats`. |
| `admin/network/gowan/wans` | `wans.js` | `form.Map('gowan')` over `wan` sections: label, interface (dropdown from `network.getNetworks()`), ratio, enabled, health-check fields. |
| `admin/network/gowan/acl` | `acl.js` | GridSection over `acl` sections: verdict, subnets, description; plus `acl_default` toggle. |
| `admin/network/gowan/policy` | `policy.js` | GridSection over `policy` sections. Types other than `client_ip` shown disabled with a "Phase 2" hint. |
| `admin/network/gowan/log` | `log.js` | Connection log viewer with client/WAN filter. |
| `admin/network/gowan/settings` | `settings.js` | `main` section: enabled, listen host/port, health-check settings, ACL default. Transparent-mode options appear only once Phase 2 ships — hidden, not greyed out (disabled controls generate support questions). |

`acl.d/luci-app-gowan.json` grants: read/write on `uci.gowan`, call on `gowan.status/stats/log`, and `service list` read.

### `luci-app-gowan/Makefile`

```makefile
include $(TOPDIR)/rules.mk

LUCI_TITLE:=LuCI interface for GoWAN multi-WAN balancer
LUCI_DEPENDS:=+gowan
LUCI_PKGARCH:=all

include $(TOPDIR)/feeds/luci/luci.mk

# call BuildPackage - OpenWrt buildroot signature
```

(LuCI apps built with `luci.mk` need the luci feed available in the SDK — the standard `openwrt/gh-action-sdk` provides it.)

---

## Native Health Checks — Detailed Spec

Health checks live **inside the daemon**, implemented directly in the vendored source (`gowan/src/health.go` + `state.go`) — regular Go code with unit tests, no patch files, no fork repo needed yet. Total scope: roughly 200 lines.

### `health.go` — check goroutines

- New CLI flags (global — every backend runs the same check): `-check-type tcp|http|none`, `-check-target <host:port|url>`, `-check-interval <s>`, `-check-timeout <s>`, `-check-fail <n>`, `-check-rise <n>`.
- One goroutine per backend runs the check each interval, **bound to that backend's interface the same way traffic is**: a `net.Dialer` with `Control` setting `SO_BINDTODEVICE` (upstream already contains this syscall code — reuse it). HTTP checks wrap the same dialer in an `http.Client` transport.
- Consecutive-failure / consecutive-success counters flip an atomic UP/DOWN flag when the threshold crosses. No locks in the hot path — the selector reads an atomic.
- The round-robin selector skips DOWN backends. **All-down guard:** if every backend is DOWN, select among all of them anyway (a proxy failing per-connection beats a dead listener) and log loudly.
- **No ICMP:** raw sockets would pull `golang.org/x/net` into vendoring; an interface-bound TCP dial to `8.8.8.8:53` is an equivalent liveness signal.

### Dial-failure fallback (dispatch path)

On outbound connect failure, immediately retry the next healthy backend (one full pass over the backend list, then fail the client). A dial failure also increments that backend's check-failure counter, so real outages converge faster than the check interval. Smallest change, biggest UX win — upstream today just errors the client connection.

### `state.go` — state file + on-change hook

- `-state-file <path>`: at startup and on every state flip, atomically write (`write tmp + rename`) JSON to `/var/run/gowan/health.json`:

  ```json
  {"backends": [
    {"ip": "10.0.1.21", "ratio": 1, "status": "up", "since": 1753100000,
     "checks_ok": 15230, "checks_failed": 3}
  ]}
  ```

  The rpcd backend reads this file for LuCI — no IPC with the daemon needed.
- `-on-change <cmd>`: exec hook invoked as `<cmd> <backend-ip> <old-state> <new-state>` on every flip. Phase 1 ships nothing attached; Phase 2 notifications plug `notify.sh` in here with zero daemon changes.

### vs. the retired shell watchdog

| | Shell watchdog (dropped) | Native (Phase 1) |
|---|--------------------------|------------------|
| State change | restart daemon, drop all connections | atomic flag flip, connections survive |
| Failover latency | up to check interval + restart | dial fallback ≈ immediate |
| Runtime dependencies | ping/nc/uclient-fetch per check type | none (in-process Go) |
| Per-backend check config | yes | global flags (per-backend returns with the fork's config file) |
| Moving parts | second procd instance, state dir, threshold logic in shell | ~200 lines of unit-tested Go in the vendored source |

---

## ACL / Access Control — Detailed Spec

### Phase 1 Implementation — nftables (fw4), input chain

Clients connect **to the router itself** on the SOCKS5 port, so this is an *input*-chain concern, not NAT. OpenWrt 24.10 ships firewall4/nftables; iptables is gone from default images.

The init script materializes ACL rules into a dedicated table (independent from fw4's, so a firewall reload never wipes it and vice versa):

```bash
gowan_apply_acl() {  # $1 = listen_port
    local port="$1" default
    config_get default main acl_default deny

    nft -f - <<-EOF
	table inet gowan
	delete table inet gowan
	table inet gowan {
	    chain input {
	        type filter hook input priority filter - 1; policy accept;
	        tcp dport $port jump acl
	    }
	    chain acl {
	        $(config_foreach emit_acl_rule acl)
	        $([ "$default" = "deny" ] && echo "reject with tcp reset")
	    }
	}
	EOF
}

emit_acl_rule() {
    local section="$1" enabled verdict subnet
    config_get_bool enabled "$section" enabled 1
    [ "$enabled" -eq 0 ] && return 0
    config_get verdict "$section" verdict allow

    handle_subnet() {
        if [ "$verdict" = allow ]; then
            echo "        ip saddr $1 accept"
        else
            echo "        ip saddr $1 reject with tcp reset"
        fi
    }
    config_list_foreach "$section" subnet handle_subnet
}
```

- First match wins (nft evaluates top-down); `acl_default` supplies the terminal rule.
- `gowan_teardown_acl` = `nft delete table inet gowan 2>/dev/null`.
- The uci-defaults script seeds one allow rule for the current LAN subnet so a fresh install with `acl_default deny` doesn't lock everyone out.

**Phase 2:** ACL moves into the Go binary (evaluated per connection, both listeners) and the nft table becomes defense-in-depth plus the home of the transparent-mode rules below.

---

## Transparent Mode — Detailed Spec

### Phase 2 — the flagship feature

Goal: balance **every LAN TCP connection with zero client configuration** — the real "mwan3 replacement" story — without a TUN device or a userspace TCP stack.

### Mechanism

1. **Interception (nftables, in the existing `inet gowan` table):** a prerouting chain redirects TCP from the configured `transparent_subnet` list to the daemon's transparent port:

   ```
   chain transparent {
       type nat hook prerouting priority dstnat; policy accept;
       ip daddr { 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16 } return   # LAN/WAN-local stays direct
       ip saddr { <transparent_subnet list> } tcp dport != <socks_port> redirect to :<transparent_port>
   }
   ```

2. **Destination recovery (patched daemon):** a `-transparent <port>` listener accepts the redirected connection and reads the original destination with one syscall — `getsockopt(SO_ORIGINAL_DST)` — instead of a SOCKS5 handshake. From there the connection joins the exact same dispatch path (health-filtered weighted round-robin, `SO_BINDTODEVICE`, dial fallback).

3. **QUIC block (`block_quic`):** an nft rule drops outbound UDP/443 from intercepted subnets so browsers fall back from HTTP/3 to TCP and actually get balanced. Without it, most browser traffic silently bypasses GoWAN.

### Scope & exclusions

- Destinations in RFC1918/link-local ranges and the router itself are never intercepted (rule 1 `return`).
- Interception is **opt-in per subnet** via `transparent_subnet` — start with an explicit list, not whole-LAN, so a bad config can't take the network down.
- The redirect rule excludes the SOCKS port itself, so both front doors coexist: transparent-intercepted clients and explicitly-configured SOCKS5 clients at the same time.

### Honest limitations

| Limitation | Detail |
|-----------|--------|
| TCP only | UDP (DNS, VoIP, games) goes direct via the normal default route — unbalanced but functional. Full UDP = TUN/netstack, Phase 3, only on demand. |
| No domain visibility | The daemon sees only the destination IP:port. Domain policies apply exclusively to SOCKS5 connections; SNI sniffing is a possible later enhancement. |
| Router-originated traffic | Not intercepted (prerouting hook); the router's own traffic follows the default route — mitigated by the resilience companion below. |
| IPv6 | Not intercepted until Phase 3 §28. Document that v6-enabled LANs will bypass via v6. |

### Companion: router-originated traffic resilience (roadmap #24)

Transparent mode balances the LAN, but the router's *own* traffic — critically its upstream DNS queries — follows the single default route. If that WAN dies, DNS dies for everyone while the balancer is healthy. Two cheap countermeasures ship alongside transparent mode:

- **Per-WAN DNS upstreams:** dnsmasq supports binding an upstream server to a device — seed `server=8.8.8.8@<wanb-device>` and `server=1.1.1.1@<wanc-device>` (via uci-defaults, user-editable) so the router's resolver survives any single WAN failure.
- **Default-route failover** (`option failover_default_route '1'`): a small script attached to the daemon's `-on-change` hook runs `ip route replace default via <gateway of a healthy WAN>` when the current default-route WAN goes DOWN. A five-line mwan3-lite for UDP and router-originated traffic, built entirely on existing machinery.

### Why this beats the alternatives considered

- **vs. redsocks/tun2socks integration (dropped):** no extra package, no second daemon to babysit, no SOCKS hop through localhost — the same binary terminates the intercepted connection directly. ~100–150 lines in the vendored source (`transparent.go`), blocked on nothing.
- **vs. TUN + gVisor netstack:** no ~10 MB binary growth, no tens-of-MB RAM for a userspace TCP stack, no owning TCP-stack bugs on low-end routers. The kernel keeps doing all packet work.

---

## Policy-Based Routing — Detailed Spec

### Scope decision (revised)

The original draft proposed a front proxy / iptables REDIRECT mesh with N+1 go-dispatch instances to fake generic policy routing in Phase 1, then contradicted itself paragraph-to-paragraph. **Resolved: generic policy routing (domain, port, dest-IP) is Phase 2 fork work — full stop.** The upstream binary never sees rules, and everything bolted on outside the SOCKS5 layer is either impossible (domain, dest-IP, port are only visible *inside* the SOCKS5 CONNECT) or a maintenance trap.

The one type that *is* cleanly implementable outside the proxy is **client_ip**, because the client's source address is visible to nftables before the SOCKS5 handshake:

### Phase 1 (optional feature #12): client_ip policy

1. For each WAN targeted by an enabled `client_ip` policy, the init script launches one extra go-dispatch instance on `listen_port + n` with only that WAN's `ip@ratio`.
2. An nft `dnat`/`redirect` rule in the gowan table maps `ip saddr <client> tcp dport 1080 → redirect to :<port+n>`.
3. Everything else lands on the main balanced instance.

Ship this only if it stays under ~50 lines of init logic; otherwise it waits for Phase 2 too.

### Phase 2 (fork): all rule types, in-process

| Type | Match example | Evaluated on |
|------|---------------|--------------|
| `client_ip` | `10.0.1.100`, `10.0.1.0/24` | TCP peer address |
| `domain` | `*.youtube.com,*.googlevideo.com` | SOCKS5 CONNECT hostname (works when clients send domains — SOCKS5h; clients resolving locally, and all transparent-mode connections, carry no hostname and fall through to IP/port rules) |
| `port` | `6881:6889,51413` | CONNECT destination port |
| `dest_ip` | `5.0.0.0/8` | CONNECT destination IP |

First match wins, config order = priority (drop the separate `priority` option; ordering in UCI/LuCI is the priority).

---

## Sticky Sessions — Detailed Spec

**Moved entirely to Phase 2.** The original Phase 1 ideas (flat-file tracker + front proxy, or dynamic per-client iptables REDIRECT rules with the `recent` module) both amount to reimplementing a proxy in shell — more code than the fork feature itself.

### Phase 2 (fork)

- In-memory map `client IP → backend`, TTL-refreshed on each connection (`sticky_timeout`, default 300s).
- Lookup happens after policy rules, before round-robin selection.
- A backend going DOWN invalidates its sticky entries.
- Exposed in the status API (`"sticky": {"10.0.1.100": {"wan": "wan1", "expires_in": 247}}`).
- UCI: `option sticky '1'`, `option sticky_timeout '300'` return to the `main` section when this ships.

**Note:** stickiness matters less than intuition suggests — each TCP connection already lives entirely on one WAN, so per-connection integrity is never at risk. Stickiness only helps sites that get confused by one *session* spanning multiple source IPs (banking, some CDNs). That framing goes in the README.

---

## Failover Notifications — Detailed Spec

### Phase 2 Implementation

`/usr/lib/gowan/notify.sh`, attached by the init script to the daemon's `-on-change` exec hook (patch 030) when the `notify` section is enabled — the daemon invokes it with `<backend-ip> <old-state> <new-state>` on every flip. It reads the `notify` UCI section and dispatches via curl (making **curl a dependency of the notification feature**, not the core — checked at runtime, warn-and-skip if absent).

```bash
send_telegram() { # token chat_id message
    curl -sS -m 10 "https://api.telegram.org/bot$1/sendMessage" \
        --data-urlencode "chat_id=$2" --data-urlencode "text=$3" >/dev/null
}
send_discord()  { # webhook message  (JSON-escape via jsonfilter/printf, not string interpolation)
    curl -sS -m 10 -H 'Content-Type: application/json' \
        -d "$(json_dump content "$2")" "$1" >/dev/null
}
send_webhook()  { # url message — same shape as discord with {"text": ...}
    ...
}
```

Example alerts:

```
⚠️ GoWAN: WAN '4G Router .3' is DOWN (ICMP check to 8.8.8.8 failed 3x)
   Remaining active: wan2, wan3
✅ GoWAN: WAN '4G Router .3' is UP again
🚨 GoWAN: ALL WANs are DOWN! Proxy kept running with full backend set.
```

Messages must be JSON-encoded properly (labels are user input) — use `json_dump`/`jshn.sh`, never string-interpolated JSON.

---

## Speed Test Per WAN — Detailed Spec

### Phase 2 Implementation

LuCI "Test" button → `ubus call gowan speedtest '{"section":"wan1"}'` → rpcd runs `gowan-speedtest.sh`:

```bash
#!/bin/sh
# gowan-speedtest.sh <wan-section>
. /lib/functions.sh
. /lib/functions/network.sh

config_load gowan
config_get iface "$1" interface
network_get_device device "$iface" || exit 1
network_get_ipaddr ip "$iface" || exit 1

TEST_URL="${2:-https://speed.cloudflare.com/__down?bytes=10000000}"

latency=$(ping -c3 -I "$device" 1.1.1.1 2>/dev/null | awk -F/ 'END{printf "%.0f", $5}')

speed_bps=$(curl -sS --interface "$device" --max-time 20 \
    -o /dev/null -w '%{speed_download}' "$TEST_URL" 2>/dev/null)

# awk, not bc — bc is not in OpenWrt base
mbps=$(awk -v b="$speed_bps" 'BEGIN{printf "%.2f", b*8/1000000}')

echo "{\"wan\":\"$1\",\"latency_ms\":${latency:-null},\"speed_mbps\":${mbps:-null}}"
```

Changes vs. original: binds to the **interface device** (curl `--interface` accepts a device name, which composes with routing correctly), test URL is Cloudflare's (tele2.net is unreliable), `bc` replaced with awk, and the whole feature — including its curl dependency — sits in Phase 2.

---

## Phase 2 Fork — SOCKS5 Auth & Status API

### SOCKS5 Authentication (RFC 1929)

```
Client → greeting (methods: NO_AUTH, USERNAME/PASSWORD)
       ← server selects USERNAME/PASSWORD when auth.enabled=1
Client → username + password
       ← success/failure
Client → CONNECT (normal flow)
```

- `enabled=0` → current unauthenticated behavior.
- Credentials come from the fork's config file, generated by the init script from UCI. Plaintext in `/etc/config/gowan` (root-only readable) — acceptable for a router, stated in the README. Hashing adds nothing when the daemon must compare plaintext per RFC 1929.

### JSON Status API

`GET http://127.0.0.1:9080/status` — **localhost-only by default**; LuCI reaches it through the rpcd backend, external monitoring via an explicit UCI opt-in to a LAN address.

```json
{
  "version": "2.0.0",
  "uptime_seconds": 284700,
  "listen": "0.0.0.0:1080",
  "total_connections": 42390,
  "active_connections": 7,
  "wans": [
    {
      "section": "wan1",
      "label": "4G Router .3",
      "interface": "wanb",
      "ip": "10.0.1.21",
      "ratio": 1,
      "status": "up",
      "connections": 15230,
      "bytes_rx": 2147483648,
      "bytes_tx": 858993459,
      "last_check_ms": 29
    }
  ],
  "policies": [
    { "name": "YouTube via wan1", "type": "domain", "match": "*.youtube.com", "wan": "wan1", "hits": 2300 }
  ],
  "sticky": { "10.0.1.100": { "wan": "wan1", "expires_in": 247 } }
}
```

Once this exists, the LuCI overview switches from the daemon's `health.json` to live API data, and the rpcd `status` method becomes a thin proxy.

---

## Logging & Diagnostics — Detailed Spec

### Phase 1: syslog via procd

go-dispatch-proxy already prints a line per dispatched connection (client, destination, chosen backend) to stdout. With `procd_set_param stdout 1`, those lines land in syslog tagged `gowan`. Therefore:

- **No log file, no logger script, no conntrack parsing.** (The original conntrack idea is dropped — a terminating proxy splits client→proxy and proxy→dest into unrelated flows, so conntrack can never correlate client to destination.)
- LuCI log page = `logread -e gowan` via the rpcd `log` method, parsed client-side.
- Log volume control: `option log_connections '0'` makes the init script pass upstream's quiet flag if available, or the rpcd method just filters.
- Persistence follows the system logging config (`logd` ring buffer by default; users who want history point syslog at a file/remote — not GoWAN's job).

### Phase 2: structured logging (fork)

One JSON line per connection to syslog:

```json
{"ts":"2026-07-21T17:15:01Z","client":"10.0.1.100:52341","dest":"github.com:443",
 "wan":"wan1","policy":"default","duration_ms":12340,"bytes_rx":1048576,"bytes_tx":524288}
```

`duration_ms`/bytes are only knowable at connection close — the fork logs at close, plus an open-event at debug level.

---

## Testing Strategy

All release-blocking, all running in `ci.yml` on every PR — no OpenWrt SDK required except the last item, so the loop stays in minutes:

1. **Go unit tests** (`gowan/src/*_test.go`): the threshold state machine (fail/rise counting, flip exactness), selector behavior (skip DOWN, all-down guard, ratio weighting), and dial fallback (one full pass, then error) — all against fake dialers, no real network.
2. **`go vet` + `gofmt -l`** over the vendored source.
3. **shellcheck** on every shell file (init, functions, rpcd, hotplug, uci-defaults, speedtest) — with BusyBox-ash dialect caveats handled via inline directives where needed.
4. **Network-namespace integration test** (`scripts/netns-smoke.sh`, plain ubuntu runner, root):
   - create two netns "WANs" connected by veth pairs, each fronting a tiny HTTP server
   - start the daemon with both backends and aggressive check timing
   - `curl --socks5` through it repeatedly → assert distribution follows the contention ratios
   - take one veth down → assert `health.json` flips DOWN within the threshold window, traffic continues on the survivor, and the `-on-change` hook fired
   - bring it back → assert rise-threshold recovery
5. **One x86_64 SDK build** on PRs to catch Makefile/packaging regressions early; the full matrix runs only on release.

Accepted gaps: LuCI JS is verified by manual smoke on a test router; real 4G-modem quirks are covered by the reference-topology docs, not CI.

---

## CI/CD — Build Pipeline

### `build.yml` — SDK matrix build

Modeled on [openwrt-bandix](https://github.com/timsaya/openwrt-bandix). Flow: version-check job decides whether the Makefile version is ahead of the latest GitHub release → creates the release → ~50 matrix jobs (25 archs × {24.10.x ipk, snapshot apk}) each run `openwrt/gh-action-sdk` and upload artifacts to the release as they finish.

Corrections/notes vs. the original draft:

- **Trigger model:** the bandix pattern is *Makefile-version-driven* (workflow_dispatch/repository_dispatch compares `PKG_VERSION` to the latest release and tags when ahead), **not** tag-push-driven. Keep the bandix model and drop the "pushing a tag triggers build" claim from the release strategy — one source of truth: bump `PKG_VERSION`, run the workflow.
- `gh-action-sdk` needs `FEEDNAME`/`FEED_DIR` pointing at the repo and must have the **packages feed (for `lang/golang`) and luci feed** enabled — default SDK feeds include both; verify in the first CI run.
- Keep the artifact-rename step (append arch to filenames) and the `V: s` verbose flag from bandix.
- PR-time checks (lint, tests, single-arch compile) live in `ci.yml` — see [Testing Strategy](#testing-strategy) — so release builds stay release-only.
- `TZ` cosmetic env dropped.

```yaml
# skeleton (full matrix elided — same ~25 archs for 24.10.2 ipk and snapshots apk)
jobs:
  job_check:      # compare PKG_VERSION+PKG_RELEASE to latest release tag → outputs has_update, version
  job_build:
    needs: job_check
    if: needs.job_check.outputs.has_update == 'true'
    strategy: { fail-fast: false, matrix: { include: [ ... ] } }
    steps:
      - uses: actions/checkout@v6
      - uses: openwrt/gh-action-sdk@main
        env:
          ARCH: ${{ matrix.sdk == 'snapshots' && matrix.platform || format('{0}-{1}', matrix.platform, matrix.sdk) }}
          PACKAGES: gowan luci-app-gowan
          V: s
      - # collect bin/packages/**/gowan*.{ipk,apk} + luci-app-gowan*, rename with arch suffix
      - uses: softprops/action-gh-release@v3
        with: { tag_name: "${{ needs.job_check.outputs.version }}", files: upload/* }
```

### Target Architectures (from bandix pattern)

**24.10.x (ipk) and snapshots (apk), same list:** x86_64, aarch64_cortex-a53, aarch64_cortex-a72, aarch64_cortex-a76, aarch64_generic, arm_cortex-a5_vfpv4, arm_cortex-a7, arm_cortex-a7_neon-vfpv4, arm_cortex-a7_vfpv4, arm_cortex-a8_vfpv3, arm_cortex-a9, arm_cortex-a9_neon, arm_cortex-a9_vfpv3-d16, arm_cortex-a15_neon-vfpv4, arm_arm1176jzf-s_vfp, arm_arm926ej-s, arm_fa526, arm_xscale, mips_24kc, mips_4kec, mips_mips32, mipsel_24kc, mipsel_24kc_24kf, mipsel_74kc, mipsel_mips32, riscv64_riscv64

(Go does not support all MIPS soft-float variants equally — expect to prune a few MIPS targets on the first CI run; `PKG_BUILD_FLAGS:=no-mips16` and `GO_ARCH_DEPENDS` handle the known cases.)

### `ci.yml` — PR checks

Runs the full [Testing Strategy](#testing-strategy) on every PR: go vet/test, shellcheck, the netns failover smoke test, and one x86_64 SDK build. The weekly upstream version-check workflow from earlier revisions is **deleted** — the source is vendored and upstream has been frozen since 2023; polling a dead repo weekly is noise.

---

## Versioning & Release Strategy

- **Version format:** semver, starting at `v0.1.0`; all `0.x` releases marked pre-release
- **Release flow:** bump `PKG_VERSION` (or `PKG_RELEASE` for packaging-only fixes → tag `vX.Y.Z-rN`) in the Makefile → run the build workflow → it tags, creates the release, and attaches artifacts
- **v1.0.0 gate:** not before explicit approval by the project owner
- **Install story (Phase 1):** download the two packages from GitHub Releases and `opkg install` / `apk add` them. A signed opkg/apk feed (usign keys, `Packages.gz` index) is a *separate deliverable* — `src/gz` pointed at a GitHub release URL does not work without an index file; defer feed hosting until there are real users.

---

## Build Strategy

**⚠️ All release builds happen on GitHub Actions. No local compilation required.**

1. Bump version → run workflow
2. Matrix jobs download the OpenWrt SDK per target
3. SDK compiles the vendored source via `golang-package.mk` (static binary)
4. SDK packages `.ipk` (24.10.x) / `.apk` (snapshots)
5. Artifacts attach to the GitHub Release

Contributors who want to iterate locally *can* (`docker run openwrt/sdk` + `make package/gowan/compile`), documented in README, but it is never required.

---

## Naming Convention

| Thing | Name | Notes |
|-------|------|-------|
| Project/Repo | `gowan` | go + wan |
| OpenWrt package | `gowan` | `opkg install gowan` |
| Binary | `/usr/sbin/gowan` | Renamed from `go-dispatch-proxy` at install |
| LuCI package | `luci-app-gowan` | Depends on `gowan` |
| Config file | `/etc/config/gowan` | UCI |
| Init script | `/etc/init.d/gowan` | procd, single daemon instance |
| Vendored source | `gowan/src/` | Upstream snapshot + `health.go`/`state.go`/`transparent.go` + tests |
| Companion scripts | `/usr/lib/gowan/*.sh` | functions, notify (Ph2), speedtest (Ph2) |
| rpcd backend | `/usr/libexec/rpcd/gowan` | ubus object `gowan` |
| Runtime state | `/var/run/gowan/` | tmpfs — `health.json` written by the daemon |
| nftables table | `inet gowan` | ACL + (optional) client-IP policy redirects + transparent redirect & QUIC block (Ph2) |

---

## Risks & Known Limitations

| Risk | Mitigation |
|------|-----------|
| Clients must speak SOCKS5 (Phase 1 only) | Transparent mode (Phase 2 flagship) removes this — nft redirect + `SO_ORIGINAL_DST`, zero client config |
| No UDP → DNS/QUIC bypass the balancer | UDP goes direct via the default route (functional, unbalanced). Transparent mode ships a QUIC-block rule (UDP/443) forcing TCP fallback; DNS is served by the router. Full UDP = TUN/netstack, Phase 3, only on demand |
| Vendored copy diverges from upstream | Intentional — upstream is frozen; provenance and MIT attribution kept in `src/UPSTREAM.md`; the vendored dir is the fork seed |
| WAN IP change (DHCP) still restarts the daemon | Hotplug reload; rare event vs. health flaps (which no longer restart); SIGHUP hot-reload removes it in Phase 2 |
| Upstream is barely maintained (last release 2023) | Pinned commit; fork planned anyway; wrapper code is upstream-agnostic |
| WAN IP changes under DHCP | Interface-based config + hotplug reload |
| Same-subnet multi-WAN (two 4G routers on one L2) | Works because `SO_BINDTODEVICE` pins egress; but each WAN needs its own interface (e.g., VLANs or separate ports) — document the reference topology |
| Go binary size on 8/16 MB flash devices | `-s -w` ldflags; document ~5-7 MB installed size; not for 8 MB devices |

---

## Open Questions

1. ~~Domain-based policy in Phase 1?~~ → **Resolved: all non-client_ip policy is Phase 2.**
2. ~~Default listen address~~ → **`0.0.0.0:1080`**, guarded by nft ACL with `acl_default deny` + seeded LAN allow rule.
3. ~~Upstream pinning~~ → superseded by **vendoring**: the upstream snapshot commit is recorded in `gowan/src/UPSTREAM.md`; no automatic tracking (upstream is frozen).
4. **Ship the optional client-IP policy in Phase 1?** Decide after the core lands — only if the init-script cost stays trivial.
5. ~~Fork timing~~ → **Dissolved by vendoring**: development happens in `gowan/src/` from day one; promote it to a standalone repo when transparent mode ships or outside interest appears. Nothing is blocked on it.
6. **Minimum OpenWrt version:** 24.10 only, or also 23.05? 23.05 still has fw4/nft and JS LuCI, so it likely works — decide whether CI carries the extra matrix.
7. **GitHub org/repo:** owner's account, private until MVP.
8. ~~Drop SOCKS5 for an interface-based mode?~~ → **Resolved: keep both front doors.** SOCKS5 (explicit, domain-aware) + transparent mode (nft redirect + `SO_ORIGINAL_DST`, Phase 2 flagship); the user enables either or both. TUN/netstack deferred to Phase 3, only if UDP demand appears.
