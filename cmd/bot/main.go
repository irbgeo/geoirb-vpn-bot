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
	"github.com/irbgeo/geoirb-vpn-bot/internal/vpn/amnezia"
	"github.com/irbgeo/geoirb-vpn-bot/internal/worker"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("fatal: %v", err)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	st, err := store.Connect(
		connectCtx,
		store.ConnectInput{
			URI:       cfg.MongoURI,
			DBName:    cfg.MongoDB,
			SecretKey: cfg.SecretKey,
		},
	)
	if err != nil {
		return err
	}
	defer st.Disconnect(context.Background()) //nolint:errcheck

	vpn, err := openVPN(ctx, cfg)
	if err != nil {
		return err
	}

	svc := service.New(
		&service.Deps{
			Users:    st.Users,
			Peers:    st.Peers,
			Payments: st.Payments,
			Feedback: st.Feedback,
			VPN:      amnezia.NewVPN(vpn),
			Settings: service.Settings{
				ServerID:     cfg.ServerID,
				EndpointHost: cfg.EndpointHost,
				DNS:          cfg.ClientDNS,
				MTU:          cfg.ClientMTU,
				TrialDays:    cfg.TrialDays,
				Tariffs:      tariffs(cfg.Tariffs),
			},
		},
	)

	client, err := newTelegramClient(cfg)
	if err != nil {
		return err
	}
	sender := bot.NewTelegramSender(client)
	notifier := bot.NewNotifier(
		&bot.NotifierDeps{
			Users:       svc,
			Sender:      sender,
			BackupStamp: cfg.BackupStamp,
			Load:        serverLoad(),
		},
	)
	router := bot.New(
		&bot.Deps{
			Users:           svc,
			Keys:            svc,
			Billing:         svc,
			Ops:             svc,
			Feedback:        svc,
			Sender:          sender,
			Notifier:        notifier,
			SupportContact:  cfg.SupportContact,
			MaintenanceFlag: cfg.MaintenanceFlag,
			Bypass:          bypass.Lists{},
		},
	)

	return serve(
		ctx,
		serveInput{
			Client:   client,
			Router:   router,
			Notifier: notifier,
			Service:  svc,
		},
	)
}

// openVPN finds the Amnezia container (unless AWG_CONTAINER is set) and
// detects the config layout inside it.
func openVPN(ctx context.Context, cfg *config.Config) (*amnezia.Server, error) {
	name := cfg.AWGContainer
	if name == "" {
		var err error
		if name, err = amnezia.DetectContainer(ctx, cfg.DockerBin); err != nil {
			return nil, err
		}
	}
	log.Printf("vpn: container %s", name)
	return amnezia.Open(
		ctx,
		&amnezia.DockerRunner{
			Bin:       cfg.DockerBin,
			Container: name,
			Timeout:   cfg.DockerTimeout,
		},
	)
}

// tariffs turns TARIFFS (days → stars) into a list sorted by days.
func tariffs(m map[int]int) []service.Tariff {
	out := make([]service.Tariff, 0, len(m))
	for days, stars := range m {
		out = append(
			out,
			service.Tariff{
				Days:  days,
				Stars: stars,
			},
		)
	}
	slices.SortFunc(out, func(a, b service.Tariff) int { return a.Days - b.Days })
	return out
}

// newTelegramClient connects to the Bot API. RetryAfter: a 429 "too many
// requests" waits (≤10 s) and retries once instead of losing the message.
func newTelegramClient(cfg *config.Config) (*tgbot.Client, error) {
	opts := []tgbot.Option{
		tgbot.WithRetryAfter(10 * time.Second),
	}
	if cfg.TelegramTestEnv {
		opts = append(opts, tgbot.WithTestEnvironment())
		log.Println("telegram: TEST environment")
	}
	return tgbot.NewClient(cfg.BotToken, opts...)
}

// serverLoad watches this machine's limits; nil (no alerts) off Linux,
// e.g. when running the bot on a laptop.
func serverLoad() bot.ServerLoad {
	if runtime.GOOS != "linux" {
		return nil
	}
	return sysload.New(
		&sysload.Input{
			ProcRoot: "/proc",
			DiskPath: "/",
		},
	)
}

// serve runs the bot until ctx is done: startup checks, the worker, the
// load monitor and long polling; then it stops them in order.
func serve(ctx context.Context, in serveInput) error {
	if _, err := in.Client.SetMyCommands(ctx, bot.Commands()); err != nil {
		log.Printf("telegram: set commands: %v", err)
	}
	in.Router.Reconcile(ctx)

	w := worker.New(
		&worker.Input{
			Job:      in.Service,
			Delivery: in.Notifier,
			Every:    time.Minute,
		},
	)
	workerDone := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(workerDone)
	}()
	go watchServerLoad(ctx, in.Notifier)

	// Dispatcher: chats are handled in parallel (one slow docker exec must
	// not stall everyone), updates of one chat in order.
	dispatcher := tgbot.NewDispatcher(
		in.Router.Handle,
		func(err error) { log.Printf("handle: %v", err) },
	)
	log.Println("bot started (long polling)")
	err := in.Client.Poll(
		ctx,
		tgbot.PollOptions{
			Timeout: 10, // below go-tgbot's 15s HTTP timeout
			OnError: func(err error) { log.Printf("poll: %v", err) },
		},
		dispatcher.Handle,
	)
	dispatcher.Shutdown(30 * time.Second)
	in.Router.Close() // a running broadcast stops and sends its report
	<-workerDone      // let a running maintenance pass finish
	return err
}

// watchServerLoad checks the server limits every minute until ctx is done.
func watchServerLoad(ctx context.Context, n *bot.Notifier) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n.CheckServerLoad(ctx)
		}
	}
}
