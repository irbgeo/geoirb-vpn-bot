package config

import (
	"bytes"
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var key = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))

func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("BOT_TOKEN", "123:abc")
	t.Setenv("DB_SECRET_KEY", key)
	t.Setenv("ENDPOINT_HOST", "35.217.30.38")
}

func TestLoadDefaults(t *testing.T) {
	setRequired(t)

	c, err := Load()
	require.NoError(t, err)
	require.Equal(
		t,
		&Config{
			BotToken:       "123:abc",
			MongoURI:       "mongodb://localhost:27017",
			MongoDB:        "geoirb_vpn",
			DBSecretKey:    key,
			ServerID:       "geoirb-vpn",
			EndpointHost:   "35.217.30.38",
			ClientDNS:      "1.1.1.1, 1.0.0.1",
			TrialDays:      7,
			SupportContact: "@geoirb",
			BackupStamp:    "",
			Tariffs: map[int]int{
				30:  150,
				90:  400,
				365: 1500,
			},
			DockerBin:     "docker",
			DockerTimeout: 20 * time.Second,
			SecretKey:     bytes.Repeat([]byte{1}, 32),
		},
		c,
	)
}

func TestLoadOverrides(t *testing.T) {
	setRequired(t)
	t.Setenv("MONGO_URI", "mongodb://u:p@127.0.0.1:27017/?authSource=geoirb_vpn")
	t.Setenv("AWG_CONTAINER", "amnezia-awg")
	t.Setenv("DOCKER_TIMEOUT", "5s")
	t.Setenv("CLIENT_MTU", "1380")

	c, err := Load()
	require.NoError(t, err)
	require.Equal(t, "mongodb://u:p@127.0.0.1:27017/?authSource=geoirb_vpn", c.MongoURI)
	require.Equal(t, "amnezia-awg", c.AWGContainer)
	require.Equal(t, 5*time.Second, c.DockerTimeout)
	require.Equal(t, 1380, c.ClientMTU)
}

func TestLoadClientMTURange(t *testing.T) {
	for _, mtu := range []string{
		"-1",
		"1279",
		"1501",
	} {
		setRequired(t)
		t.Setenv("CLIENT_MTU", mtu)
		_, err := Load()
		require.ErrorContains(t, err, "CLIENT_MTU", mtu)
	}
}

func TestLoadRequired(t *testing.T) {
	for _, name := range []string{
		"BOT_TOKEN",
		"DB_SECRET_KEY",
		"ENDPOINT_HOST",
	} {
		setRequired(t)
		t.Setenv(name, "")
		_, err := Load()
		require.ErrorContains(t, err, name)
	}
}

func TestLoadTelegramTestEnv(t *testing.T) {
	setRequired(t)
	c, err := Load()
	require.NoError(t, err)
	require.False(t, c.TelegramTestEnv, "production by default")

	t.Setenv("TELEGRAM_TEST_ENV", "true")
	c, err = Load()
	require.NoError(t, err)
	require.True(t, c.TelegramTestEnv)
}

func TestLoadRejectsZeroDurations(t *testing.T) {
	for name, value := range map[string]string{
		"TRIAL_DAYS":     "0",
		"DOCKER_TIMEOUT": "0s",
	} {
		t.Run(name, func(t *testing.T) {
			setRequired(t)
			t.Setenv(name, value)
			_, err := Load()
			require.ErrorContains(t, err, name)
		})
	}
}

func TestLoadTariffs(t *testing.T) {
	setRequired(t)
	t.Setenv("TARIFFS", "30:200")

	c, err := Load()
	require.NoError(t, err)
	require.Equal(
		t,
		map[int]int{
			30: 200,
		},
		c.Tariffs,
	)

	for _, bad := range []string{
		"30:0",
		"0:200",
	} {
		t.Setenv("TARIFFS", bad)
		_, err = Load()
		require.ErrorContains(t, err, "TARIFFS", bad)
	}
}

func TestLoadBadSecretKey(t *testing.T) {
	setRequired(t)

	t.Setenv("DB_SECRET_KEY", "not base64!")
	_, err := Load()
	require.ErrorContains(t, err, "DB_SECRET_KEY")

	t.Setenv("DB_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte("short")))
	_, err = Load()
	require.ErrorContains(t, err, "32 bytes")
}
