package bot

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// onlineNotifier is a Notifier with one admin and a deliver func that
// reports n clients online, then lets a minute pass.
func onlineNotifier() (*fakeSender, func(n int)) {
	s := &fakeSender{
		fail: map[int64]bool{},
	}
	svc := &fakeService{
		admins: []*service.User{
			{
				ID: 1,
			},
		},
	}
	n := NewNotifier(
		&NotifierDeps{
			Users:  svc,
			Sender: s,
		},
	)
	deliver := func(online int) {
		m := maintenance()
		m.Online = online
		n.DeliverMaintenance(context.Background(), m)
		time.Sleep(time.Minute)
	}
	return s, deliver
}

func TestOnlineDropAlertsOnceUntilItRecovers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, deliver := onlineNotifier()
		for _, n := range []int{10, 9, 8, 6, 4, 3} {
			deliver(n)
		}
		require.Empty(t, s.sent, "3 is still over a quarter of 10")

		deliver(2)
		require.Len(t, s.sent, 1)
		require.Equal(t, int64(1), s.sent[0].ChatID)
		require.Contains(t, s.sent[0].Text, "онлайн 2")
		require.Contains(t, s.sent[0].Text, "до 10")

		deliver(1)
		require.Len(t, s.sent, 1, "one alert while it stays low")

		deliver(9)
		deliver(2)
		require.Len(t, s.sent, 2, "alerts again after it came back")
	})
}

func TestOnlineDropNeedsEnoughClients(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, deliver := onlineNotifier()
		deliver(4)
		deliver(0)
		require.Empty(t, s.sent)
	})
}

func TestOnlineDropForgetsPeaksOlderThanAnHour(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, deliver := onlineNotifier()
		deliver(10)
		time.Sleep(time.Hour)
		deliver(2)
		require.Empty(t, s.sent)
	})
}

func TestUnknownOnlineKeepsState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, deliver := onlineNotifier()
		deliver(10)
		deliver(-1) // stats read failed
		require.Empty(t, s.sent, "unknown is not a drop")
		deliver(2)
		require.Len(t, s.sent, 1, "the peak before it is kept")
	})
}
