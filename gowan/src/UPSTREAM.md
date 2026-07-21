# Upstream Provenance

This directory vendors the source of **go-dispatch-proxy** and extends it
in place. Upstream code is MIT-licensed; the original license is preserved
in [LICENSE](LICENSE).

| | |
|---|---|
| Upstream repo | https://github.com/extremecoders-re/go-dispatch-proxy |
| Vendored commit | `3b2d7cd7d0dc3232471f328aef6d3df52b15f4f1` (2025-05-17) |
| Upstream release at that commit | `v7` |
| Vendored on | 2026-07-21 |

## File map

| File | Origin |
|------|--------|
| `constants.go` | upstream, unmodified |
| `socks.go` | upstream, unmodified apart from `gofmt` formatting |
| `main.go` | upstream, modified: `lb_list` holds pointers, health fields on `load_balancer`, health-aware `get_load_balancer` with exclusion set, new CLI flags, tunnel-mode dial fallback |
| `dial_linux.go` | replaces upstream `servers_response_linux.go`: keeps the `SO_BINDTODEVICE` dialer, drops the stray trailing `"\x00"` on the interface name (Go's `BindToDevice` NUL-terminates itself) |
| `dial_other.go` | replaces upstream `servers_response.go`: source-address-only dialer for non-Linux |
| `dispatch.go` | new: shared `server_response` with dial-failure fallback across backends |
| `health.go` | new: per-backend check goroutines, fail/rise threshold state machine |
| `state.go` | new: atomic health.json writer + `-on-change` exec hook |
| `*_test.go` | new: unit tests |

Upstream has been effectively frozen since 2023 (the vendored 2025 commit
is a README-only change). If it ever revives, diff against the commit
above before merging anything.
