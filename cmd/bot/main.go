package main

import (
	"context"
	"log"
	"os/signal"
	"slices"
	"syscall"
	"time"

	tgbot "github.com/irbgeo/go-tgbot"

	"github.com/irbgeo/geoirb-vpn-bot/internal/bot"
	"github.com/irbgeo/geoirb-vpn-bot/internal/bypass"
	"github.com/irbgeo/geoirb-vpn-bot/internal/config"
	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
	"github.com/irbgeo/geoirb-vpn-bot/internal/store"
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
			VPN:      vpn,
			Settings: service.Settings{
				ServerID:     cfg.ServerID,
				EndpointHost: cfg.EndpointHost,
				DNS:          cfg.ClientDNS,
				TrialDays:    cfg.TrialDays,
				Tariffs:      tariffs(cfg.Tariffs),
			},
		},
	)

	// RetryAfter: a 429 "too many requests" waits (≤10 s) and retries once
	// instead of losing the message.
	opts := []tgbot.Option{
		tgbot.WithRetryAfter(10 * time.Second),
	}
	if cfg.TelegramTestEnv {
		opts = append(opts, tgbot.WithTestEnvironment())
		log.Println("telegram: TEST environment")
	}
	client, err := tgbot.NewClient(cfg.BotToken, opts...)
	if err != nil {
		return err
	}
	router := bot.New(
		&bot.Deps{
			Service:        svc,
			Sender:         bot.NewTelegramSender(client),
			SupportContact: cfg.SupportContact,
			BackupStamp:    cfg.BackupStamp,
			Bypass: bypass.New(
				&bypass.Input{
					URLs: cfg.BypassURLs,
					TTL:  6 * time.Hour,
				},
			),
		},
	)

	if _, err := client.SetMyCommands(ctx, bot.Commands()); err != nil {
		log.Printf("telegram: set commands: %v", err)
	}
	router.Reconcile(ctx)

	w := worker.New(
		&worker.Input{
			Job:      svc,
			Delivery: router,
			Every:    time.Minute,
		},
	)
	workerDone := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(workerDone)
	}()

	// Dispatcher: chats are handled in parallel (one slow docker exec must
	// not stall everyone), updates of one chat in order.
	dispatcher := tgbot.NewDispatcher(
		router.Handle,
		func(err error) { log.Printf("handle: %v", err) },
	)
	log.Println("bot started (long polling)")
	err = client.Poll(
		ctx,
		tgbot.PollOptions{
			Timeout: 10, // below go-tgbot's 15s HTTP timeout
			OnError: func(err error) { log.Printf("poll: %v", err) },
		},
		dispatcher.Handle,
	)
	dispatcher.Shutdown(30 * time.Second)
	router.Close() // a running broadcast stops and sends its report
	<-workerDone   // let a running maintenance pass finish
	return err
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
