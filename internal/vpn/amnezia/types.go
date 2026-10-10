package amnezia

import (
	"errors"
	"time"
)

// ErrNotPersisted: Update changed the live interface but could not save
// the config file. The file is now behind the live state: to undo, re-sync
// the live interface from the file.
var ErrNotPersisted = errors.New("amnezia: live interface updated but config not saved")

// kv is one "Key = Value" line of a config section.
type kv struct {
	Key   string
	Value string
}

// peer is one [Peer] section of the server config.
type peer struct {
	PublicKey    string
	PresharedKey string
	AllowedIPs   string
	// Other keeps any extra lines verbatim (e.g. PersistentKeepalive).
	Other []string
}

// serverConf is the parsed awg0.conf. The [Interface] lines are kept
// verbatim (comments included) so the file is written back unchanged.
type serverConf struct {
	Interface []string
	Peers     []peer
}

// clientConf is everything needed to render a client .conf file.
type clientConf struct {
	Address         string
	DNS             string
	MTU             int // 0 = not written
	PrivateKey      string
	Params          []kv
	ServerPublicKey string
	PresharedKey    string
	Endpoint        string
}

// execInput is one command to run inside the container.
type execInput struct {
	Args []string
	// Stdin carries secrets (keys, configs) so they never show up in argv.
	Stdin string
}

// keys is a fresh client key set.
type keys struct {
	Private string
	Public  string
	PSK     string
}

// peerStat is live data for one peer from `awg show <iface> dump`.
type peerStat struct {
	PublicKey  string
	Endpoint   string
	AllowedIPs string
	// LatestHandshake is zero when the peer never connected.
	LatestHandshake time.Time
	RX              int64
	TX              int64
}

// persistInput is a file to save atomically inside the container.
type persistInput struct {
	Path    string
	Content string
	// Expect is the sha256 (hex) of the file as it was read; the write is
	// refused if the file changed since (another writer). Empty = no check.
	Expect string
}

// liveSync is a config to apply with `awg syncconf`, if the file on disk is
// still the one it was built from (Expect: its sha256, hex).
type liveSync struct {
	Conf   *serverConf
	Expect string
}
