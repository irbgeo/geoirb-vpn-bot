package bot

import (
	"errors"
	"testing"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAdminErrorText(t *testing.T) {
	cases := map[string]struct {
		err   error
		text  string
		known bool
	}{
		"ip taken": {
			err:   service.ErrIPTaken,
			text:  "⚠️ IP этого ключа уже занят другим ключом на сервере. Проверьте сверку (Reconcile) в журнале.",
			known: true,
		},
		"unreadable": {
			err:   service.ErrUnreadable,
			text:  "⚠️ Данные ключа повреждены (не расшифровываются): вернуть его на сервер нельзя. Удалите ключ и выдайте новый.",
			known: true,
		},
		"blocked": {
			err:   service.ErrBlocked,
			text:  "⚠️ Ключ отключён администратором: сначала включите его.",
			known: true,
		},
		"unknown": {
			err:  errors.New("docker down"),
			text: "⚠️ Не получилось, подробности в журнале бота.",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			text, known := adminErrorText(c.err)
			require.Equal(t, c.text, text)
			require.Equal(t, c.known, known)
		})
	}
}
