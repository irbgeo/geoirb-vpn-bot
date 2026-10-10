package sysload

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeProc is a /proc with the files the monitor reads.
type fakeProc struct {
	t     *testing.T
	root  string
	user  int
	idle  int
	steal int
}

func newFakeProc(t *testing.T) *fakeProc {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sys/net/netfilter"), 0o755))
	p := &fakeProc{
		t:    t,
		root: root,
	}
	p.conntrack(100)
	p.memory(10)
	p.cpu(0)
	return p
}

// procFile is one file of a fakeProc: its path under the root and content.
type procFile struct {
	name string
	text string
}

func (s *fakeProc) write(f procFile) {
	require.NoError(s.t, os.WriteFile(filepath.Join(s.root, f.name), []byte(f.text), 0o644))
}

// conntrack sets the table to count of 1000.
func (s *fakeProc) conntrack(count int) {
	s.write(procFile{
		name: "sys/net/netfilter/nf_conntrack_count",
		text: fmt.Sprintf("%d\n", count),
	})
	s.write(procFile{
		name: "sys/net/netfilter/nf_conntrack_max",
		text: "1000\n",
	})
}

// memory sets used memory to percent of 1000000 kB.
func (s *fakeProc) memory(percent int) {
	s.write(procFile{
		name: "meminfo",
		text: fmt.Sprintf("MemTotal:        1000000 kB\nMemFree:          1000 kB\nMemAvailable:    %d kB\n", 1000000-percent*10000),
	})
}

// cpu adds 100 jiffies to the counters, busy of them busy: half user,
// half steal (steal counts as busy: the hypervisor took the CPU).
func (s *fakeProc) cpu(busy int) {
	s.user += busy - busy/2
	s.steal += busy / 2
	s.idle += 100 - busy
	// cpu user nice system idle iowait irq softirq steal guest guest_nice
	s.write(procFile{
		name: "stat",
		text: fmt.Sprintf("cpu  %d 0 0 %d 0 0 0 %d 0 0\ncpu0 1 0 0 1 0 0 0 0 0 0\n", s.user, s.idle, s.steal),
	})
}

func newMonitor(p *fakeProc) *monitor {
	return New(
		p.root,
		"",
	)
}

func TestAlertOnceWhenConntrackPassesLimitAndWhenItRecovers(t *testing.T) {
	p := newFakeProc(t)
	m := newMonitor(p)

	alerts, err := m.Check()
	require.NoError(t, err)
	require.Empty(t, alerts)

	p.conntrack(850)
	alerts, err = m.Check()
	require.NoError(t, err)
	require.Equal(
		t,
		[]Alert{
			{
				Metric:  Conntrack,
				Percent: 85,
				Limit:   80,
			},
		},
		alerts,
	)

	p.conntrack(900)
	alerts, _ = m.Check()
	require.Empty(t, alerts, "no repeat while still high")

	p.conntrack(750)
	alerts, _ = m.Check()
	require.Empty(t, alerts, "just under the limit is not yet normal")

	p.conntrack(600)
	alerts, _ = m.Check()
	require.Equal(
		t,
		[]Alert{
			{
				Metric:    Conntrack,
				Percent:   60,
				Limit:     80,
				Recovered: true,
			},
		},
		alerts,
	)

	p.conntrack(850)
	alerts, _ = m.Check()
	require.Len(t, alerts, 1, "alerts again after it went back to normal")
}

func TestMemoryAlert(t *testing.T) {
	p := newFakeProc(t)
	m := newMonitor(p)

	p.memory(95)
	alerts, err := m.Check()
	require.NoError(t, err)
	require.Equal(
		t,
		[]Alert{
			{
				Metric:  Memory,
				Percent: 95,
				Limit:   90,
			},
		},
		alerts,
	)
}

func TestCPUAlertOnlyAfterFiveBusyMinutes(t *testing.T) {
	p := newFakeProc(t)
	m := newMonitor(p)
	_, err := m.Check() // first sample: no CPU usage yet
	require.NoError(t, err)

	for i := 1; i <= 4; i++ {
		p.cpu(90)
		alerts, err := m.Check()
		require.NoError(t, err)
		require.Empty(t, alerts, "minute %d", i)
	}
	p.cpu(90)
	alerts, _ := m.Check()
	require.Equal(
		t,
		[]Alert{
			{
				Metric:  CPU,
				Percent: 90,
				Limit:   85,
			},
		},
		alerts,
	)
}

func TestCPUShortPeakDoesNotAlert(t *testing.T) {
	p := newFakeProc(t)
	m := newMonitor(p)
	_, _ = m.Check()

	for i := 0; i < 10; i++ {
		busy := 90
		if i%4 == 3 {
			busy = 20 // a quiet minute resets the count
		}
		p.cpu(busy)
		alerts, err := m.Check()
		require.NoError(t, err)
		require.Empty(t, alerts)
	}
}

func TestDiskIsRead(t *testing.T) {
	p := newFakeProc(t)
	u, err := New(
		p.root,
		t.TempDir(),
	).usage()
	require.NoError(t, err)
	require.Contains(t, u, Disk)
	require.GreaterOrEqual(t, u[Disk], 0)
	require.LessOrEqual(t, u[Disk], 100)
}

func TestMissingFileIsAnErrorButOtherMetricsStillWork(t *testing.T) {
	p := newFakeProc(t)
	require.NoError(t, os.Remove(filepath.Join(p.root, "sys/net/netfilter/nf_conntrack_count")))
	p.memory(95)
	m := newMonitor(p)

	alerts, err := m.Check()
	require.Error(t, err)
	require.Len(t, alerts, 1)
	require.Equal(t, Memory, alerts[0].Metric)
}

// Off Linux there is no /proc: the monitor checks nothing and reports
// nothing, so main builds it on every OS.
func TestMonitorWithoutProcChecksNothing(t *testing.T) {
	m := New(
		filepath.Join(t.TempDir(), "no-proc"),
		"/",
	)

	for range 3 {
		alerts, err := m.Check()
		require.NoError(t, err, "not an error every minute in the log")
		require.Empty(t, alerts)
	}
}
