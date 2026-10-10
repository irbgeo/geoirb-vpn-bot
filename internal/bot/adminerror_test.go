package bot

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// Every expected error gets its own text (a word of it is checked, not a
// copy of the text); anything else gets the generic one and is not "known".
func TestAdminErrorText(t *testing.T) {
	cases := map[string]struct {
		err   error
		word  string
		known bool
	}{
		"expired": {
			err:   service.ErrExpired,
			word:  "Срок ключа закончился",
			known: true,
		},
		"no private key": {
			err:   service.ErrNoPrivateKey,
			word:  "только на устройстве",
			known: true,
		},
		"not found": {
			err:   service.ErrNotFound,
			word:  "Не найдено",
			known: true,
		},
		"payment not found": {
			err:   errPaymentNotFound,
			word:  "Оплата не найдена",
			known: true,
		},
		"ip taken": {
			err:   service.ErrIPTaken,
			word:  "IP этого ключа уже занят",
			known: true,
		},
		"unreadable": {
			err:   service.ErrUnreadable,
			word:  "не расшифровываются",
			known: true,
		},
		"blocked": {
			err:   service.ErrBlocked,
			word:  "сначала включите",
			known: true,
		},
		"unknown": {
			err:  errors.New("awg down"),
			word: "подробности в журнале",
		},
	}
	seen := map[string]bool{}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			text, known := adminErrorText(c.err)
			require.Contains(t, text, c.word)
			require.Equal(t, c.known, known)
			require.NotContains(t, text, c.err.Error(), "the raw error stays out of the chat")
			require.False(t, seen[text], "each case has its own text")
			seen[text] = true
		})
	}
}
