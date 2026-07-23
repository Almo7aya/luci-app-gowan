// usage.go
//
// Per-interface cumulative data-usage accounting for metered links.
// Samples each backend's whole-interface /proc/net/dev counters, folds
// the deltas into all-time / month / day totals (handling counter resets
// and period rollover), keeps a daily history ring for graphs, and
// persists across restarts and reboots.
package main

import (
	"bufio"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type day_total struct {
	Day string `json:"day"`
	RX  uint64 `json:"rx"`
	TX  uint64 `json:"tx"`
}

type dev_usage struct {
	TotalRX  uint64      `json:"total_rx"`
	TotalTX  uint64      `json:"total_tx"`
	MonthKey string      `json:"month"`
	MonthRX  uint64      `json:"month_rx"`
	MonthTX  uint64      `json:"month_tx"`
	DayKey   string      `json:"day"`
	DayRX    uint64      `json:"day_rx"`
	DayTX    uint64      `json:"day_tx"`
	History  []day_total `json:"history"`
	LastRX   uint64      `json:"last_raw_rx"`
	LastTX   uint64      `json:"last_raw_tx"`
	Seen     bool        `json:"seen"` // baseline established
}

const usage_history_days = 90

func day_key(t time.Time) string   { return t.Format("2006-01-02") }
func month_key(t time.Time) string { return t.Format("2006-01") }

func (u *dev_usage) push_history(day string, rx, tx uint64) {
	u.History = append(u.History, day_total{Day: day, RX: rx, TX: tx})
	if len(u.History) > usage_history_days {
		u.History = u.History[len(u.History)-usage_history_days:]
	}
}

/*
Folds one raw cumulative counter reading into the totals. Handles:
  - period rollover (new day archives the finished day; new month resets
    the month accumulator)
  - counter resets (raw < last, e.g. reboot/interface recreation) by
    counting the new raw as the delta instead of going negative
  - the very first reading, which only establishes a baseline
*/
func (u *dev_usage) apply_sample(rawRX, rawTX uint64, now time.Time) {
	dk, mk := day_key(now), month_key(now)

	if u.DayKey != dk {
		if u.DayKey != "" {
			u.push_history(u.DayKey, u.DayRX, u.DayTX)
		}
		u.DayKey, u.DayRX, u.DayTX = dk, 0, 0
	}
	if u.MonthKey != mk {
		u.MonthKey, u.MonthRX, u.MonthTX = mk, 0, 0
	}

	if u.Seen {
		drx := delta(rawRX, u.LastRX)
		dtx := delta(rawTX, u.LastTX)
		u.TotalRX += drx
		u.TotalTX += dtx
		u.MonthRX += drx
		u.MonthTX += dtx
		u.DayRX += drx
		u.DayTX += dtx
	}
	u.LastRX, u.LastTX, u.Seen = rawRX, rawTX, true
}

func delta(cur, last uint64) uint64 {
	if cur >= last {
		return cur - last
	}
	return cur // counter reset: count from zero
}

var (
	usage_mu    sync.Mutex
	usage_map   = map[string]*dev_usage{}
	usage_flash string
	usage_tmpfs string
)

func usage_get(dev string) *dev_usage {
	u := usage_map[dev]
	if u == nil {
		u = &dev_usage{}
		usage_map[dev] = u
	}
	return u
}

// Reads device -> {rxBytes, txBytes} from /proc/net/dev.
func read_proc_net_dev() map[string][2]uint64 {
	out := map[string][2]uint64{}
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return out
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		dev := strings.TrimSpace(line[:colon])
		fields := strings.Fields(line[colon+1:])
		if len(fields) < 9 {
			continue
		}
		rx, _ := strconv.ParseUint(fields[0], 10, 64)
		tx, _ := strconv.ParseUint(fields[8], 10, 64)
		out[dev] = [2]uint64{rx, tx}
	}
	return out
}

// Current backend device names, deduplicated.
func backend_devices() []string {
	mutex.Lock()
	defer mutex.Unlock()
	seen := map[string]bool{}
	var devs []string
	for _, lb := range lb_list {
		if lb.iface != "" && !seen[lb.iface] {
			seen[lb.iface] = true
			devs = append(devs, lb.iface)
		}
	}
	return devs
}

func sample_usage(now time.Time) {
	counters := read_proc_net_dev()
	devs := backend_devices()

	usage_mu.Lock()
	for _, dev := range devs {
		c, ok := counters[dev]
		if !ok {
			continue
		}
		usage_get(dev).apply_sample(c[0], c[1], now)
	}
	usage_mu.Unlock()
}

func write_usage(path string) {
	if path == "" {
		return
	}
	usage_mu.Lock()
	data, err := json.Marshal(usage_map)
	usage_mu.Unlock()
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, data, 0o644) == nil {
		os.Rename(tmp, path)
	}
}

func load_usage(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	m := map[string]*dev_usage{}
	if json.Unmarshal(data, &m) != nil {
		return
	}
	usage_mu.Lock()
	usage_map = m
	usage_mu.Unlock()
}

/*
Starts the usage sampler. Live snapshots go to the tmpfs file every
interval; the persistent (flash) copy is flushed every flush interval
and on shutdown, minimising flash writes while surviving reboots.
*/
func start_usage_tracking(flashPath, tmpfsPath string, interval, flush time.Duration) {
	usage_flash = flashPath
	usage_tmpfs = tmpfsPath
	load_usage(flashPath)

	go func() {
		tick := time.NewTicker(interval)
		flushTick := time.NewTicker(flush)
		for {
			select {
			case <-tick.C:
				sample_usage(time.Now())
				write_usage(tmpfsPath)
			case <-flushTick.C:
				write_usage(flashPath)
			}
		}
	}()
}

// Flush the persistent copy (call on shutdown).
func flush_usage() {
	write_usage(usage_flash)
	write_usage(usage_tmpfs)
}
