package main

import (
	"context"
	"log"
	"os/signal"
	"runtime"
	"slices"
	"syscall"
	"time"

	tgbot "github.com/irbgeo/go-tgbot"

	"github.com/irbgeo/geoirb-vpn-bot/internal/bot"
	"github.com/irbgeo/geoirb-vpn-bot/internal/bypass"
	"github.com/irbgeo/geoirb-vpn-bot/internal/config"
	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
	"github.com/irbgeo/geoirb-vpn-bot/internal/store"
	"github.com/irbgeo/geoirb-vpn-bot/internal/sysload"
	"github.com/irbgeo/geoirb-vpn-bot/internal/tunnel"
	"github.com/irbgeo/geoirb-vpn-bot/internal/vpn/amnezia"
	"github.com/irbgeo/geoirb-vpn-bot/internal/worker"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("fatal: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	st, err := store.Connect(connectCtx, cfg)
	if err != nil {
		log.Fatalf("fatal: %v", err)
	}
	defer st.Disconnect(context.Background()) //nolint:errcheck

	runner := amnezia.NewLocalRunner(cfg)
	server, err := amnezia.Open(ctx, runner, cfg.AWGConf)
	if err != nil {
		log.Fatalf("fatal: %v", err)
	}
	vpn := amnezia.NewVPN(server)

	// TARIFFS (days → stars) as a list sorted by days.
	tariffs := make([]service.Tariff, 0, len(cfg.Tariffs))
	for days, stars := range cfg.Tariffs {
		tariff := service.Tariff{
			Days:  days,
			Stars: stars,
		}
		tariffs = append(tariffs, tariff)
	}
	slices.SortFunc(tariffs, func(a, b service.Tariff) int { return a.Days - b.Days })

	serviceDeps := service.Deps{
		Users:    st.Users,
		Peers:    st.Peers,
		Payments: st.Payments,
		Feedback: st.Feedback,
		VPN:      vpn,
		Settings: service.Settings{
			ServerID:     cfg.ServerID,
			EndpointHost: cfg.EndpointHost,
			DNS:          cfg.ClientDNS,
			MTU:          cfg.ClientMTU,
			TrialDays:    cfg.TrialDays,
			Tariffs:      tariffs,
		},
	}
	svc := service.New(&serviceDeps)

	client, err := bot.NewTelegramClient(cfg)
	if err != nil {
		log.Fatalf("fatal: %v", err)
	}
	sender := bot.NewTelegramSender(client)

	// Server load alerts watch this machine's limits; none off Linux, e.g.
	// when running the bot on a laptop.
	var load bot.ServerLoad
	if runtime.GOOS == "linux" {
		load = sysload.New(
			"/proc",
			"/",
		)
	}
	notifier := bot.NewNotifier(
		svc,
		sender,
		cfg.BackupStamp,
		load,
		cfg.RUNetsStamp,
	)
	lists := bypass.New()
	deps := bot.Deps{
		Users:    svc,
		Keys:     svc,
		Billing:  svc,
		Ops:      svc,
		Feedback: svc,
		Sender:   sender,
		Notifier: notifier,
		Bypass:   lists,
		Config:   cfg,
	}
	router := bot.New(&deps)

	// Run: startup checks, the worker, the load monitor and long polling;
	// then stop them in order.
	// The tunnel watcher owns the bot's ip rules: through the exit tunnel
	// while it works, direct while it is down. Its first check runs before
	// any Telegram call, so a stale rule left with the tunnel down is gone.
	if cfg.ExitIface != "" {
		hostNet := tunnel.NewHostNet(cfg)
		watcher := tunnel.New(
			hostNet,
			3*time.Minute,
		)
		notifier.WatchTunnel(ctx, watcher)
	}
	_, err = client.SetMyCommands(ctx, bot.Commands())
	if err != nil {
		log.Printf("telegram: set commands: %v", err)
	}
	router.Reconcile(ctx)

	w := worker.New(
		svc,
		notifier,
		time.Minute,
	)
	workerDone := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(workerDone)
	}()
	go notifier.WatchServerLoad(ctx)

	// Dispatcher: chats are handled in parallel (one slow awg command must
	// not stall everyone), updates of one chat in order.
	dispatcher := tgbot.NewDispatcher(
		router.Handle,
		func(err error) { log.Printf("handle: %v", err) },
	)
	log.Println("bot started (long polling)")
	pollOptions := tgbot.PollOptions{
		Timeout: 10, // below go-tgbot's 15s HTTP timeout
		OnError: func(err error) { log.Printf("poll: %v", err) },
	}
	err = client.Poll(ctx, pollOptions, dispatcher.Handle)
	dispatcher.Shutdown(30 * time.Second)
	router.Close() // a running broadcast stops and sends its report
	<-workerDone   // let a running maintenance pass finish
	if err != nil {
		log.Fatalf("fatal: %v", err)
	}
}
