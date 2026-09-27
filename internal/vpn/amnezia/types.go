package amnezia

import (
	"errors"
	"time"
)

// ErrNotPersisted: Update changed the live interface but could not save
// the config file. The file is now behind the live state: to undo, re-sync
// the live interface from the file.
var ErrNotPersisted = errors.New("amnezia: live interface updated but config not saved")

// KV is one "Key = Value" line of a config section.
type KV struct {
	Key   string
	Value string
}

// Peer is one [Peer] section of the server config.
type Peer struct {
	PublicKey    string
	PresharedKey string
	AllowedIPs   string
	// Other keeps any extra lines verbatim (e.g. PersistentKeepalive).
	Other []string
}

// ServerConf is the parsed awg0.conf. The [Interface] lines are kept
// verbatim (comments included) so the file is written back unchanged.
type ServerConf struct {
	Interface []string
	Peers     []Peer
}

// ClientConf is everything needed to render a client .conf file.
type ClientConf struct {
	Address         string
	DNS             string
	PrivateKey      string
	Params          []KV
	ServerPublicKey string
	PresharedKey    string
	Endpoint        string
}

// ExecInput is one command to run inside the container.
type ExecInput struct {
	Args []string
	// Stdin carries secrets (keys, configs) so they never show up in argv.
	Stdin string
}

// Keys is a fresh client key set.
type Keys struct {
	Private string
	Public  string
	PSK     string
}

// PeerStat is live data for one peer from `awg show <iface> dump`.
type PeerStat struct {
	PublicKey  string
	Endpoint   string
	AllowedIPs string
	// LatestHandshake is zero when the peer never connected.
	LatestHandshake time.Time
	RX              int64
	TX              int64
}

// ClientEntry is one client to show in the Amnezia app (clientsTable).
type ClientEntry struct {
	PublicKey  string
	Name       string
	AllowedIPs string
	CreatedAt  time.Time
}

// tableEntry is one clientsTable item. UserData stays a map so the fields
// the app writes (traffic, handshake) survive a rewrite.
type tableEntry struct {
	ClientID string         `json:"clientId"`
	UserData map[string]any `json:"userData"`
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
	Conf   *ServerConf
	Expect string
}
