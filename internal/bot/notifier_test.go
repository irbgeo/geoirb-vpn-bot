package bot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
	"github.com/irbgeo/geoirb-vpn-bot/internal/sysload"
)

func maintenance() *service.Maintenance {
	return &service.Maintenance{
		SubnetUsed:  10,
		SubnetTotal: 254,
	}
}

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
	r.notify.backupStamp = stamp
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

func (f *fakeLoad) Check() ([]sysload.Alert, error) {
	return f.alerts, f.err
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
