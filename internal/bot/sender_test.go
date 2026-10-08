package bot

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/irbgeo/geoirb-vpn-bot/internal/config"
)

func TestNewTelegramClient(t *testing.T) {
	_, err := NewTelegramClient(&config.Config{})
	require.ErrorContains(t, err, "token is required")

	client, err := NewTelegramClient(
		&config.Config{
			BotToken:        "123:abc",
			TelegramTestEnv: true,
		},
	)
	require.NoError(t, err)
	require.NotNil(t, client)
}
