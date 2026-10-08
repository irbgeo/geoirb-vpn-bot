package config

import (
	"encoding/base64"
	"fmt"
	"time"

	"github.com/kelseyhightower/envconfig"
)

// Config holds runtime configuration from the environment (.env).
type Config struct {
	BotToken string `envconfig:"BOT_TOKEN"`
	// TelegramTestEnv: talk to Telegram's test environment (free Stars
	// payments). Needs a bot created there, with its own BOT_TOKEN.
	TelegramTestEnv bool   `envconfig:"TELEGRAM_TEST_ENV" default:"false"`
	MongoURI        string `envconfig:"MONGO_URI" default:"mongodb://localhost:27017"`
	MongoDB         string `envconfig:"MONGO_DB" default:"geoirb_vpn"`
	// DBSecretKey is base64 of 32 random bytes (openssl rand -base64 32);
	// it encrypts client private keys in the DB.
	DBSecretKey string `envconfig:"DB_SECRET_KEY"`
	// ServerID names this VPN server in the DB (ready for several servers).
	ServerID string `envconfig:"SERVER_ID" default:"geoirb-vpn"`
	// EndpointHost goes into client configs. Prefer a domain: moving the
	// server then only needs a DNS change.
	EndpointHost string `envconfig:"ENDPOINT_HOST"`
	ClientDNS    string `envconfig:"CLIENT_DNS" default:"1.1.1.1, 1.0.0.1"`
	// ClientMTU goes into client configs; 0 = not written (the app's default).
	ClientMTU int `envconfig:"CLIENT_MTU"`
	// Tariffs: days of access → price in Telegram Stars, e.g.
	// TARIFFS=30:150,90:400,365:1500.
	Tariffs map[int]int `envconfig:"TARIFFS" default:"30:150,90:400,365:1500"`
	// SupportContact: where users write for help (/support, /paysupport, /terms).
	SupportContact string `envconfig:"SUPPORT_CONTACT" default:"@geoirb"`
	// BackupStamp: a file the server backup touches after every good run
	// (deploy sets it); admins are told when it gets old. Empty = no check.
	BackupStamp string `envconfig:"BACKUP_STAMP"`
	// MaintenanceFlag: a file that exists while the admin's "maintenance"
	// is on (deploy sets it), so a restart keeps the state. Empty = memory.
	MaintenanceFlag string `envconfig:"MAINTENANCE_FLAG"`
	// TrialDays: a plain user's first key is a free trial of this length.
	TrialDays int `envconfig:"TRIAL_DAYS" default:"7"`
	// AWGContainer: empty = find amnezia-awg2 / amnezia-awg via docker ps.
	AWGContainer string `envconfig:"AWG_CONTAINER"`
	// DockerBin: "docker" on the server; a local wrapper for development.
	DockerBin     string        `envconfig:"DOCKER_BIN" default:"docker"`
	DockerTimeout time.Duration `envconfig:"DOCKER_TIMEOUT" default:"20s"`

	// SecretKey is DBSecretKey decoded.
	SecretKey []byte `ignored:"true"`
}

// Load reads the config from the environment and checks it.
func Load() (*Config, error) {
	var c Config
	err := envconfig.Process("", &c)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	for name, v := range map[string]string{
		"BOT_TOKEN":     c.BotToken,
		"DB_SECRET_KEY": c.DBSecretKey,
		"ENDPOINT_HOST": c.EndpointHost,
	} {
		if v == "" {
			return nil, fmt.Errorf("config: %s is required", name)
		}
	}
	if c.TrialDays <= 0 {
		// Zero would issue keys that never expire, not "no trial".
		return nil, fmt.Errorf("config: TRIAL_DAYS must be positive, got %d", c.TrialDays)
	}
	if c.DockerTimeout <= 0 {
		return nil, fmt.Errorf("config: DOCKER_TIMEOUT must be positive, got %s", c.DockerTimeout)
	}
	if c.ClientMTU != 0 && (c.ClientMTU < 1280 || c.ClientMTU > 1500) {
		return nil, fmt.Errorf("config: CLIENT_MTU must be 1280..1500 or empty, got %d", c.ClientMTU)
	}
	for days, stars := range c.Tariffs {
		if days <= 0 || stars <= 0 {
			return nil, fmt.Errorf("config: TARIFFS: days and stars must be positive, got %d:%d", days, stars)
		}
	}
	key, err := base64.StdEncoding.DecodeString(c.DBSecretKey)
	if err != nil {
		return nil, fmt.Errorf("config: DB_SECRET_KEY is not base64: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("config: DB_SECRET_KEY must be 32 bytes, got %d", len(key))
	}
	c.SecretKey = key
	return &c, nil
}
