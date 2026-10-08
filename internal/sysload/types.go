package sysload

// Metric is one server resource the monitor watches.
type Metric string

// Watched metrics.
const (
	Conntrack Metric = "conntrack" // NAT connection table, % of nf_conntrack_max
	Memory    Metric = "memory"    // RAM in use, % (MemAvailable counts as free)
	Disk      Metric = "disk"      // disk in use, %, like df
	CPU       Metric = "cpu"       // CPU busy since the last check, %, steal included
)

// Alert is a metric that passed its limit, or came back to normal.
type Alert struct {
	Metric    Metric
	Percent   int
	Limit     int
	Recovered bool // back under Limit - recoverGap
}

// limit is when a metric alerts: Percent or more for Checks checks in a row.
type limit struct {
	Metric  Metric
	Percent int
	Checks  int
}

// cpuTimes are the "cpu" line of /proc/stat, in jiffies since boot.
type cpuTimes struct {
	Busy  uint64
	Total uint64
}

// share is Part out of Whole, for percent.
type share struct {
	Part  uint64
	Whole uint64
}
