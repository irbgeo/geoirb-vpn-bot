package bot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
	"github.com/irbgeo/geoirb-vpn-bot/internal/sysload"
	"github.com/irbgeo/geoirb-vpn-bot/internal/tunnel"
)

func TestDeliverExpiredToOwnerWithExtendButton(t *testing.T) {
	r, s := newRouter(&fakeService{})
	m := maintenance()
	m.Expired = []*service.Peer{
		{
			PublicKey: "PUB1=",
			UserID:    42,
			Name:      "tg:bob",
			IP:        "10.8.1.10",
		},
	}

	r.notify.DeliverMaintenance(context.Background(), m)

	require.Len(t, s.sent, 1)
	require.Equal(t, int64(42), s.sent[0].ChatID)
	require.Contains(t, s.sent[0].Text, "отключён")
	require.Equal(t, "buyk:PUB1=", s.sent[0].Keyboard.InlineKeyboard[0][0].CallbackData)
}

func TestDeliverReminders(t *testing.T) {
	r, s := newRouter(&fakeService{})
	m := maintenance()
	end := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	m.Remind3d = []*service.Peer{
		{
			PublicKey: "PUB1=",
			UserID:    42,
			ExpiresAt: end,
		},
	}
	m.Remind1d = []*service.Peer{
		{
			PublicKey: "PUB2=",
			UserID:    7,
			ExpiresAt: end,
		},
	}

	r.notify.DeliverMaintenance(context.Background(), m)

	require.Len(t, s.sent, 2)
	require.Contains(t, s.sent[0].Text, "меньше 3 дней")
	require.Contains(t, s.sent[0].Text, "30.09.2026 15:00 по Москве")
	require.Contains(t, s.sent[1].Text, "сутки")
	require.Equal(t, int64(7), s.sent[1].ChatID)
}

func TestSubnetAlertOnceUntilItDrops(t *testing.T) {
	r, s := newRouter(&fakeService{
		admins: []*service.User{
			{
				ID: 1,
			},
		},
	})
	full := func(used int) *service.Maintenance {
		return &service.Maintenance{
			SubnetUsed:  used,
			SubnetTotal: 254,
		}
	}
	ctx := context.Background()

	r.notify.DeliverMaintenance(ctx, full(210))
	r.notify.DeliverMaintenance(ctx, full(215))
	require.Len(t, s.sent, 1, "one alert while it stays full")
	require.Contains(t, s.sent[0].Text, "210 из 254")

	r.notify.DeliverMaintenance(ctx, full(100))
	r.notify.DeliverMaintenance(ctx, full(220))
	require.Len(t, s.sent, 2, "alerts again after dropping below")
}

func TestUnknownSubnetKeepsAlertState(t *testing.T) {
	r, s := newRouter(&fakeService{
		admins: []*service.User{
			{
				ID: 1,
			},
		},
	})
	ctx := context.Background()
	full := &service.Maintenance{
		SubnetUsed:  210,
		SubnetTotal: 254,
	}

	r.notify.DeliverMaintenance(ctx, full)
	r.notify.DeliverMaintenance(ctx, &service.Maintenance{}) // subnet read failed
	r.notify.DeliverMaintenance(ctx, full)
	require.Len(t, s.sent, 1, "an unknown reading neither alerts nor resets")
}

func TestDeliverMadeForeverToOwner(t *testing.T) {
	r, s := newRouter(&fakeService{})
	m := maintenance()
	m.MadeForever = []*service.Peer{
		{
			PublicKey: "PUB1=",
			UserID:    42,
			Name:      "tg:bob",
		},
	}

	r.notify.DeliverMaintenance(context.Background(), m)

	require.Len(t, s.sent, 1)
	require.Equal(t, int64(42), s.sent[0].ChatID)
	require.Contains(t, s.sent[0].Text, "бессрочн")
	require.Nil(t, s.sent[0].Keyboard, "nothing to buy")
}

func TestBackupAlertWhenTheLastBackupIsOld(t *testing.T) {
	stamp := filepath.Join(t.TempDir(), "last-backup")
	require.NoError(t, os.WriteFile(stamp, nil, 0o600))
	svc := &fakeService{
		admins: []*service.User{
			{
				ID: 1,
			},
		},
	}
	r, s := newRouter(svc)
	r.notify.backup.path = stamp
	ctx := context.Background()

	r.notify.DeliverMaintenance(ctx, maintenance())
	require.Empty(t, s.sent, "a fresh backup: quiet")

	old := time.Now().Add(-30 * time.Hour)
	require.NoError(t, os.Chtimes(stamp, old, old))
	r.notify.DeliverMaintenance(ctx, maintenance())
	r.notify.DeliverMaintenance(ctx, maintenance())
	require.Len(t, s.sent, 1, "one alert while it stays old")
	require.Contains(t, s.sent[0].Text, "бэкап")

	require.NoError(t, os.Chtimes(stamp, time.Now(), time.Now()))
	r.notify.DeliverMaintenance(ctx, maintenance())
	require.NoError(t, os.Remove(stamp))
	r.notify.DeliverMaintenance(ctx, maintenance())
	require.Len(t, s.sent, 2, "a missing mark alerts again after a good one")
}

func TestBackupCheckOffWithoutAPath(t *testing.T) {
	r, s := newRouter(&fakeService{
		admins: []*service.User{
			{
				ID: 1,
			},
		},
	})

	r.notify.DeliverMaintenance(context.Background(), maintenance())
	require.Empty(t, s.sent)
}

type fakeLoad struct {
	alerts []sysload.Alert
	err    error
}

func (s *fakeLoad) Check() ([]sysload.Alert, error) {
	return s.alerts, s.err
}

func TestServerLoadAlertsGoToAdmins(t *testing.T) {
	r, s := newRouter(&fakeService{
		admins: []*service.User{
			{
				ID: 1,
			},
		},
	})
	r.notify.load = &fakeLoad{
		alerts: []sysload.Alert{
			{
				Metric:  sysload.Conntrack,
				Percent: 85,
				Limit:   80,
			},
			{
				Metric:    sysload.CPU,
				Percent:   40,
				Limit:     85,
				Recovered: true,
			},
		},
		err: errors.New("no disk"), // logged; the alerts still go out
	}

	r.notify.CheckServerLoad(context.Background())

	require.Len(t, s.sent, 2)
	require.Equal(t, int64(1), s.sent[0].ChatID)
	require.Contains(t, s.sent[0].Text, "⚠️")
	require.Contains(t, s.sent[0].Text, "таблица соединений")
	require.Contains(t, s.sent[0].Text, "85%")
	require.Contains(t, s.sent[0].Text, "80%")
	require.Contains(t, s.sent[1].Text, "✅")
	require.Contains(t, s.sent[1].Text, "процессор")
}

type countLoad struct {
	calls atomic.Int32
}

func (s *countLoad) Check() ([]sysload.Alert, error) {
	s.calls.Add(1)
	return nil, nil
}

func TestWatchServerLoadChecksEveryMinute(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		load := &countLoad{}
		n := NewNotifier(
			&fakeService{},
			&fakeSender{},
			"",
			load,
			"",
		)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			n.WatchServerLoad(ctx)
			close(done)
		}()

		time.Sleep(150 * time.Second)
		synctest.Wait()
		require.Equal(t, int32(2), load.calls.Load())

		cancel()
		<-done
	})
}

func TestNoLoadMonitorNoLoadAlerts(t *testing.T) {
	r, s := newRouter(&fakeService{
		admins: []*service.User{
			{
				ID: 1,
			},
		},
	})
	r.notify.CheckServerLoad(context.Background())
	require.Empty(t, s.sent)
}

func TestLoadAlertTextForEveryMetric(t *testing.T) {
	for _, m := range []sysload.Metric{
		sysload.Conntrack,
		sysload.Memory,
		sysload.Disk,
		sysload.CPU,
	} {
		text := loadAlertText(
			sysload.Alert{
				Metric:  m,
				Percent: 95,
				Limit:   90,
			},
		)
		require.NotContains(t, text, string(m), "a Russian name, not the code name")
	}
}

func maintenance() *service.Maintenance {
	return &service.Maintenance{
		SubnetUsed:  10,
		SubnetTotal: 254,
	}
}

type tunnelStep struct {
	st      tunnel.State
	changed bool
	err     error
}

// fakeTunnel returns its steps one per Check, then the last one unchanged.
// The watch goroutine and the test both touch steps, hence the lock.
type fakeTunnel struct {
	mu    sync.Mutex
	steps []tunnelStep
}

func (s *fakeTunnel) Check(context.Context) (tunnel.State, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.steps[0]
	if len(s.steps) > 1 {
		s.steps = s.steps[1:]
	} else {
		s.steps[0].changed = false
	}
	return st.st, st.changed, st.err
}

// left is how many steps are still to come.
func (s *fakeTunnel) left() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.steps)
}

func watchTunnel(t *testing.T, steps []tunnelStep) []outMessage {
	r, s := newRouter(&fakeService{
		admins: []*service.User{
			{
				ID: 1,
			},
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	ft := &fakeTunnel{steps: steps}
	r.notify.WatchTunnel(ctx, ft)
	require.Equal(t, max(len(steps)-1, 1), ft.left(), "the first check is done before WatchTunnel returns")
	time.Sleep(time.Duration(len(steps)) * time.Minute)
	synctest.Wait()
	cancel()
	synctest.Wait() // the watch goroutine ends
	return s.sent
}

func TestWatchTunnelAlertsOnChangesOnly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sent := watchTunnel(t, []tunnelStep{
			{
				st:      tunnel.Down,
				changed: true,
			},
			{
				st: tunnel.Down,
			},
			{
				err: errors.New("awg broke"),
			},
			{
				st:      tunnel.Up,
				changed: true,
			},
		})

		require.Len(t, sent, 2)
		require.Equal(t, tunnelDownText, sent[0].Text)
		require.Equal(t, tunnelUpText, sent[1].Text)
	})
}

func TestWatchTunnelUpAtStartIsQuiet(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sent := watchTunnel(t, []tunnelStep{
			{
				st:      tunnel.Up,
				changed: true,
			},
			{
				st:      tunnel.Down,
				changed: true,
			},
		})

		require.Len(t, sent, 1)
		require.Equal(t, tunnelDownText, sent[0].Text)
	})
}

func TestWatchTunnelUnknownThenUpIsQuiet(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sent := watchTunnel(t, []tunnelStep{
			{
				st: tunnel.Unknown,
			},
			{
				st:      tunnel.Up,
				changed: true,
			},
		})

		require.Empty(t, sent, "up after the boot grace is not news")
	})
}

func TestRUNetsAlertWhenTheListIsOld(t *testing.T) {
	stamp := filepath.Join(t.TempDir(), "ru-nets")
	require.NoError(t, os.WriteFile(stamp, nil, 0o600))
	r, s := newRouter(&fakeService{
		admins: []*service.User{
			{
				ID: 1,
			},
		},
	})
	r.notify.ruNets.path = stamp
	ctx := context.Background()

	old := time.Now().Add(-7 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(stamp, old, old))
	r.notify.DeliverMaintenance(ctx, maintenance())
	require.Empty(t, s.sent, "a week old is fine")

	old = time.Now().Add(-9 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(stamp, old, old))
	r.notify.DeliverMaintenance(ctx, maintenance())
	r.notify.DeliverMaintenance(ctx, maintenance())
	require.Len(t, s.sent, 1, "one alert while it stays old")
	require.Equal(t, ruNetsAlertText(old), s.sent[0].Text)
	require.Contains(t, s.sent[0].Text, "journalctl -u geoirb-ru-nets")

	require.NoError(t, os.Chtimes(stamp, time.Now(), time.Now()))
	r.notify.DeliverMaintenance(ctx, maintenance())
	require.NoError(t, os.Chtimes(stamp, old, old))
	r.notify.DeliverMaintenance(ctx, maintenance())
	require.Len(t, s.sent, 2, "alerts again after a fresh touch")
}

// An alert that reached no admin is tried again at the next check instead
// of being lost for as long as the condition holds.
func TestUndeliveredAlertsAreSentAgain(t *testing.T) {
	stamp := filepath.Join(t.TempDir(), "last-backup")
	old := time.Now().Add(-30 * time.Hour)
	require.NoError(t, os.WriteFile(stamp, nil, 0o600))
	require.NoError(t, os.Chtimes(stamp, old, old))
	r, s := newRouter(&fakeService{
		admins: []*service.User{
			{
				ID: 1,
			},
		},
	})
	r.notify.backup.path = stamp
	ctx := context.Background()
	m := &service.Maintenance{
		SubnetUsed:  250,
		SubnetTotal: 254,
		Online:      10,
	}
	s.fail[1] = true                    // Telegram is down
	r.notify.DeliverMaintenance(ctx, m) // also the peak the drop is measured from
	m.Online = 1
	r.notify.DeliverMaintenance(ctx, m)
	require.Empty(t, s.sent)

	s.fail[1] = false
	r.notify.DeliverMaintenance(ctx, m)
	require.Len(t, s.sent, 3, "subnet, backup and clients dropping: all told once Telegram is back")

	r.notify.DeliverMaintenance(ctx, m)
	require.Len(t, s.sent, 3, "and only once")
}

func TestNotifyAdminsSaysWhetherAnyoneGotIt(t *testing.T) {
	svc := &fakeService{
		admins: []*service.User{
			{
				ID: 1,
			},
			{
				ID: 2,
			},
		},
	}
	r, s := newRouter(svc)
	ctx := context.Background()

	s.fail[1] = true
	require.True(t, r.notify.NotifyAdmins(ctx, "x"), "one admin is enough")
	s.fail[2] = true
	require.False(t, r.notify.NotifyAdmins(ctx, "x"))

	svc.admins = nil
	require.True(t, r.notify.NotifyAdmins(ctx, "x"), "no admins: the log line is all there is, nothing to retry")
}

func TestWatchTunnelSendsAnUndeliveredAlertAtTheNextCheck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, s := newRouter(&fakeService{
			admins: []*service.User{
				{
					ID: 1,
				},
			},
		})
		s.fail[1] = true
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r.notify.WatchTunnel(ctx, &fakeTunnel{
			steps: []tunnelStep{
				{
					st:      tunnel.Down,
					changed: true,
				},
				{
					st: tunnel.Down,
				},
			},
		})
		require.Empty(t, s.sentTo(1))

		s.setFail(1, false)
		time.Sleep(time.Minute)
		synctest.Wait()
		require.Len(t, s.sentTo(1), 1)
		require.Equal(t, tunnelDownText, s.sentTo(1)[0].Text)

		time.Sleep(time.Minute)
		synctest.Wait()
		require.Len(t, s.sentTo(1), 1, "told once")
	})
}
