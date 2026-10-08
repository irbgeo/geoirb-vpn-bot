// Package sysload watches the server's limits (connection table, memory,
// disk, CPU) and says when one is passed or back to normal.
package sysload

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// recoverGap: a metric is back to normal only this many points under its
// limit, so a value going up and down around it doesn't alert every minute.
const recoverGap = 10

// Monitor keeps what it needs between checks: the last CPU counters and
// which metrics are high. Not safe for concurrent use (one worker calls it).
type Monitor struct {
	proc    string
	disk    string
	prevCPU *cpuTimes
	hot     map[Metric]int // checks in a row at or over the limit
	alerted map[Metric]bool
}

// New creates a Monitor.
func New(
	in *Input,
) *Monitor {
	return &Monitor{
		proc:    in.ProcRoot,
		disk:    in.DiskPath,
		hot:     map[Metric]int{},
		alerted: map[Metric]bool{},
	}
}

// Check reads the metrics and returns the ones that just passed their
// limit or just came back to normal. A metric that can't be read is
// skipped (its state kept) and reported in the error; the others still
// work.
func (s *Monitor) Check() ([]Alert, error) {
	u, err := s.usage()
	var out []Alert
	for _, l := range limits() {
		p, ok := u[l.Metric]
		if !ok {
			continue
		}
		if p >= l.Percent {
			s.hot[l.Metric]++
		} else {
			s.hot[l.Metric] = 0
		}
		a := Alert{
			Metric:  l.Metric,
			Percent: p,
			Limit:   l.Percent,
		}
		switch {
		case !s.alerted[l.Metric] && s.hot[l.Metric] >= l.Checks:
			s.alerted[l.Metric] = true
			out = append(out, a)
		case s.alerted[l.Metric] && p < l.Percent-recoverGap:
			s.alerted[l.Metric] = false
			a.Recovered = true
			out = append(out, a)
		}
	}
	return out, err
}

// usage reads every metric it can, in percent.
func (s *Monitor) usage() (map[Metric]int, error) {
	u := map[Metric]int{}
	var errs []error
	p, err := s.conntrack()
	if err != nil {
		errs = append(errs, err)
	} else {
		u[Conntrack] = p
	}
	p, err = s.memory()
	if err != nil {
		errs = append(errs, err)
	} else {
		u[Memory] = p
	}
	if s.disk != "" {
		p, err = diskUsed(s.disk)
		if err != nil {
			errs = append(errs, err)
		} else {
			u[Disk] = p
		}
	}
	p, ok, err := s.cpu()
	if err != nil {
		errs = append(errs, err)
	} else if ok {
		u[CPU] = p
	}
	return u, errors.Join(errs...)
}

func (s *Monitor) conntrack() (int, error) {
	count, err := s.readInt("sys/net/netfilter/nf_conntrack_count")
	if err != nil {
		return 0, err
	}
	limit, err := s.readInt("sys/net/netfilter/nf_conntrack_max")
	if err != nil {
		return 0, err
	}
	return share{
		Part:  count,
		Whole: limit,
	}.percent(), nil
}

func (s *Monitor) readInt(name string) (uint64, error) {
	b, err := os.ReadFile(filepath.Join(s.proc, name))
	if err != nil {
		return 0, fmt.Errorf("sysload: %w", err)
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("sysload: %s: %w", name, err)
	}
	return n, nil
}

func (s *Monitor) memory() (int, error) {
	b, err := os.ReadFile(filepath.Join(s.proc, "meminfo"))
	if err != nil {
		return 0, fmt.Errorf("sysload: %w", err)
	}
	var total, avail uint64
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		n, _ := strconv.ParseUint(f[1], 10, 64)
		switch f[0] {
		case "MemTotal:":
			total = n
		case "MemAvailable:":
			avail = n
		}
	}
	if total == 0 {
		return 0, errors.New("sysload: no MemTotal in meminfo")
	}
	return share{
		Part:  total - min(avail, total),
		Whole: total,
	}.percent(), nil
}

// diskUsed is the used share of the file system at path, as df shows it
// (space kept for root doesn't count as free).
func diskUsed(path string) (int, error) {
	var st syscall.Statfs_t
	err := syscall.Statfs(path, &st)
	if err != nil {
		return 0, fmt.Errorf("sysload: statfs %s: %w", path, err)
	}
	used := st.Blocks - st.Bfree
	return share{
		Part:  used,
		Whole: used + st.Bavail,
	}.percent(), nil
}

// cpu is the busy share since the last call; ok is false on the first call.
func (s *Monitor) cpu() (p int, ok bool, err error) {
	now, err := s.cpuTimes()
	if err != nil {
		return 0, false, err
	}
	prev := s.prevCPU
	s.prevCPU = &now
	if prev == nil || now.Total <= prev.Total {
		return 0, false, nil
	}
	return share{
		Part:  now.Busy - prev.Busy,
		Whole: now.Total - prev.Total,
	}.percent(), true, nil
}

// cpuTimes reads the first line of /proc/stat: "cpu user nice system idle
// iowait irq softirq steal guest guest_nice". Guest time is already inside
// user and nice, so it is not added again.
func (s *Monitor) cpuTimes() (cpuTimes, error) {
	b, err := os.ReadFile(filepath.Join(s.proc, "stat"))
	if err != nil {
		return cpuTimes{}, fmt.Errorf("sysload: %w", err)
	}
	line, _, _ := strings.Cut(string(b), "\n")
	f := strings.Fields(line)
	if len(f) < 9 || f[0] != "cpu" {
		return cpuTimes{}, errors.New("sysload: bad /proc/stat")
	}
	var t cpuTimes
	for i, v := range f[1:9] {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return cpuTimes{}, fmt.Errorf("sysload: /proc/stat: %w", err)
		}
		t.Total += n
		if i != 3 && i != 4 { // idle, iowait
			t.Busy += n
		}
	}
	return t, nil
}

// percent is Part as a share of Whole, 0 when Whole is 0.
func (s share) percent() int {
	if s.Whole == 0 {
		return 0
	}
	return int(s.Part * 100 / s.Whole)
}

// limits: CPU must be high for 5 checks (minutes) in a row, short peaks are
// normal; the rest alert at once.
func limits() []limit {
	return []limit{
		{
			Metric:  Conntrack,
			Percent: 80,
			Checks:  1,
		},
		{
			Metric:  Memory,
			Percent: 90,
			Checks:  1,
		},
		{
			Metric:  Disk,
			Percent: 90,
			Checks:  1,
		},
		{
			Metric:  CPU,
			Percent: 85,
			Checks:  5,
		},
	}
}
