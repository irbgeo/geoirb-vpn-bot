package amnezia

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const confDir = "/opt/amnezia/awg"

// Runner runs a command inside the Amnezia container.
type Runner interface {
	Exec(ctx context.Context, in ExecInput) (string, error)
}

// Server manages the AmneziaWG interface inside the container.
type Server struct {
	run      Runner
	confPath string // /opt/amnezia/awg/awg0.conf (or wg0.conf)
	iface    string // awg0 (or wg0)
	tool     string // awg (or wg)
	// mu serializes read-modify-write of the config: two concurrent updates
	// would otherwise lose one of the changes.
	mu sync.Mutex
}

// Open detects the config file and tool inside the container.
func Open(
	ctx context.Context,
	run Runner,
) (*Server, error) {
	files, err := run.Exec(ctx, cmd("ls", confDir))
	if err != nil {
		return nil, err
	}
	conf := ""
	for _, name := range []string{
		"awg0.conf",
		"wg0.conf",
	} {
		if slices.Contains(strings.Fields(files), name) {
			conf = name
			break
		}
	}
	if conf == "" {
		return nil, fmt.Errorf("amnezia: no awg0.conf or wg0.conf in %s", confDir)
	}

	tool, err := run.Exec(ctx, cmd("sh", "-c", "command -v awg || command -v wg"))
	if err != nil {
		return nil, fmt.Errorf("amnezia: neither awg nor wg found: %w", err)
	}

	return &Server{
		run:      run,
		confPath: path.Join(confDir, conf),
		iface:    strings.TrimSuffix(conf, ".conf"),
		tool:     path.Base(strings.TrimSpace(tool)),
	}, nil
}

// GenKeys generates a client private key, its public key and a preshared key.
func (s *Server) GenKeys(ctx context.Context) (Keys, error) {
	priv, err := s.exec(ctx, cmd(s.tool, "genkey"))
	if err != nil {
		return Keys{}, err
	}
	in := cmd(s.tool, "pubkey")
	in.Stdin = priv
	pub, err := s.exec(ctx, in)
	if err != nil {
		return Keys{}, err
	}
	psk, err := s.exec(ctx, cmd(s.tool, "genpsk"))
	if err != nil {
		return Keys{}, err
	}
	return Keys{
		Private: priv,
		Public:  pub,
		PSK:     psk,
	}, nil
}

// ServerPublicKey returns the interface public key for client configs.
func (s *Server) ServerPublicKey(ctx context.Context) (string, error) {
	return s.exec(ctx, cmd(s.tool, "show", s.iface, "public-key"))
}

// ReadConf returns the current server config.
func (s *Server) ReadConf(ctx context.Context) (*ServerConf, error) {
	text, err := s.run.Exec(ctx, cmd("cat", s.confPath))
	if err != nil {
		return nil, err
	}
	return ParseServerConf(text)
}

// Update reads the config, lets fn change it, applies the result to the
// live interface without a restart, then saves it to disk.
// If fn fails nothing is written. If the live apply fails the file is not
// touched, so disk and interface stay in sync.
func (s *Server) Update(ctx context.Context, fn func(*ServerConf) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	text, err := s.run.Exec(ctx, cmd("cat", s.confPath))
	if err != nil {
		return err
	}
	c, err := ParseServerConf(text)
	if err != nil {
		return err
	}
	if err := fn(c); err != nil {
		return err
	}
	// s.mu covers this process only. The Amnezia app or a second bot can
	// write the file too: both steps run only if it is still what we read.
	read := sha256Hex(text)
	if err := s.syncLive(
		ctx,
		liveSync{
			Conf:   c,
			Expect: read,
		},
	); err != nil {
		return err
	}
	if err := s.persist(
		ctx,
		persistInput{
			Path:    s.confPath,
			Content: c.String(),
			Expect:  read,
		},
	); err != nil {
		return fmt.Errorf("amnezia: live interface updated but config not saved: %w", err)
	}
	return nil
}

// Stats returns live handshake and traffic data for every peer.
func (s *Server) Stats(ctx context.Context) ([]PeerStat, error) {
	out, err := s.run.Exec(ctx, cmd(s.tool, "show", s.iface, "dump"))
	if err != nil {
		return nil, err
	}
	return parseDump(out)
}

func cmd(args ...string) ExecInput {
	return ExecInput{
		Args: args,
	}
}

func (s *Server) exec(ctx context.Context, in ExecInput) (string, error) {
	out, err := s.run.Exec(ctx, in)
	return strings.TrimSpace(out), err
}

// syncLive applies the stripped config with `awg syncconf`: it adds and
// removes only the changed peers, other clients stay connected.
func (s *Server) syncLive(ctx context.Context, l liveSync) error {
	script := fmt.Sprintf(
		`set -e; f=%q; %s t=$(mktemp); trap 'rm -f "$t"' EXIT; cat > "$t"; %s syncconf %s "$t"`,
		s.confPath,
		unchangedCheck(l.Expect),
		s.tool,
		s.iface,
	)
	in := cmd("sh", "-c", script)
	in.Stdin = l.Conf.Stripped()
	_, err := s.run.Exec(ctx, in)
	return err
}

// persist saves a file: backup to .bak, write .tmp with the same owner
// and mode as the original, then an atomic mv.
func (s *Server) persist(ctx context.Context, in persistInput) error {
	script := fmt.Sprintf(
		`set -e; f=%q; %s cp -p "$f" "$f.bak"; cat > "$f.tmp"; `+
			`chown "$(stat -c %%u:%%g "$f")" "$f.tmp"; chmod "$(stat -c %%a "$f")" "$f.tmp"; mv "$f.tmp" "$f"`,
		in.Path,
		unchangedCheck(in.Expect),
	)
	run := cmd("sh", "-c", script)
	run.Stdin = in.Content
	_, err := s.run.Exec(ctx, run)
	return err
}

// parseDump reads `awg show <iface> dump`. The first line is the interface
// itself; each next line is: public-key, preshared-key, endpoint,
// allowed-ips, latest-handshake (unix), rx, tx, keepalive.
func parseDump(out string) ([]PeerStat, error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	stats := make([]PeerStat, 0, len(lines))
	for _, line := range lines[1:] {
		f := strings.Split(line, "\t")
		if len(f) < 8 {
			return nil, fmt.Errorf("amnezia: bad dump line with %d fields", len(f))
		}
		hs, err1 := strconv.ParseInt(f[4], 10, 64)
		rx, err2 := strconv.ParseInt(f[5], 10, 64)
		tx, err3 := strconv.ParseInt(f[6], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil {
			return nil, fmt.Errorf("amnezia: bad numbers in dump for peer %s", f[0])
		}
		st := PeerStat{
			PublicKey:  f[0],
			AllowedIPs: f[3],
			RX:         rx,
			TX:         tx,
		}
		if f[2] != "(none)" {
			st.Endpoint = f[2]
		}
		if hs > 0 {
			st.LatestHandshake = time.Unix(hs, 0)
		}
		stats = append(stats, st)
	}
	return stats, nil
}

// unchangedCheck is a shell step that fails (exit 3) when the file "$f"
// no longer has the sha256 expect; empty expect = no check.
func unchangedCheck(expect string) string {
	if expect == "" {
		return ""
	}
	return fmt.Sprintf(
		`[ "$(sha256sum "$f" | cut -d' ' -f1)" = %q ] || { echo "amnezia: $f was changed by another writer, try again" >&2; exit 3; };`,
		expect,
	)
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
