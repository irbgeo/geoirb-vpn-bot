package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tgbot "github.com/irbgeo/go-tgbot"
	"github.com/stretchr/testify/require"

	"github.com/irbgeo/geoirb-vpn-bot/internal/config"
	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

type fakeSender struct {
	editErr   error
	mu        sync.Mutex
	invoices  []*outInvoice
	answers   []preCheckoutAnswer
	refunds   []refundInput
	refundErr error
	// refundAlready: Telegram says the charge was refunded before.
	refundAlready bool
	edits         []editMessage
	sent          []outMessage
	files         []outFile
	videos        []outVideo
	// videoHold: an upload waits until it is closed (or its ctx ends).
	videoHold chan struct{}
	// videoErr decides whether a video send fails; nil = all work.
	videoErr func(v *outVideo) error
	answered []string
	fail     map[int64]bool
}

func (s *fakeSender) SendInvoice(_ context.Context, m *outInvoice) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invoices = append(s.invoices, m)
	return nil
}

func (s *fakeSender) AnswerPreCheckout(_ context.Context, a preCheckoutAnswer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.answers = append(s.answers, a)
	return nil
}

func (s *fakeSender) Refund(ctx context.Context, in refundInput) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := ctx.Err()
	if err != nil {
		return false, err // like a real API call on a cancelled context
	}
	s.refunds = append(s.refunds, in)
	return s.refundAlready, s.refundErr
}

func (s *fakeSender) Edit(_ context.Context, m editMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.editErr != nil {
		return s.editErr
	}
	s.edits = append(s.edits, m)
	return nil
}

func (s *fakeSender) SendDocument(_ context.Context, m outFile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files = append(s.files, m)
	return nil
}

func (s *fakeSender) SendPhoto(_ context.Context, m outFile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files = append(s.files, m)
	return nil
}

func (s *fakeSender) SendVideo(ctx context.Context, m *outVideo) (string, error) {
	s.mu.Lock()
	s.videos = append(s.videos, *m)
	hold, fail := s.videoHold, s.videoErr
	s.mu.Unlock()
	if m.FileID == "" && hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
		}
	}
	err := ctx.Err() // like a real API call on a cancelled context
	if err == nil && fail != nil {
		err = fail(m)
	}
	if err != nil {
		return "", err
	}
	return "VID1", nil
}

// sentVideos is a copy of the video sends so far.
func (s *fakeSender) sentVideos() []outVideo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]outVideo(nil), s.videos...)
}

func (s *fakeSender) Answer(_ context.Context, callbackID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.answered = append(s.answered, callbackID)
	return nil
}

func (s *fakeSender) Send(_ context.Context, m outMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail[m.ChatID] {
		return errors.New("blocked")
	}
	s.sent = append(s.sent, m)
	return nil
}

// setFail makes sends to a chat fail (or work again) while a background
// goroutine may be sending.
func (s *fakeSender) setFail(chatID int64, fail bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail[chatID] = fail
}

type fakeService struct {
	// askedUsers / askedAccess / askedPayments: the user IDs User, Access
	// and Payments were called with, in order.
	askedUsers    []int64
	askedAccess   []int64
	askedPayments []int64
	userErr       error // User fails
	usersErr      error // Users fails
	reissued      *service.Peer
	reissuedFor   []service.UserKey
	deletedOwn    []service.UserKey
	ownKeyErr     error
	feedbackList  []*service.Feedback
	feedback      []service.FeedbackInput
	feedbackErr   error
	recipientsErr error
	refundMarkErr error
	createdWith   []service.CreateKeyInput
	registered    []service.RegisterInput
	role          service.Role
	admins        []*service.User
	report        *service.ReconcileReport
	reportErr     error
	created       *service.Peer
	createErr     error
	access        []service.KeyInfo
	askedKey      service.UserKey
	users         []*service.User
	calls         []string
	issued        []service.IssueInput
	invoiceIn     service.PurchaseInput
	invoiceErr    error
	checkErr      error
	payRes        *service.PayResult
	payErr        error
	refunded      []string
	refundedAs    []service.RefundInput // what MarkRefunded got, also when it fails
	payments      []*service.Payment
	unfinished    []*service.Payment
	stats         *service.Stats
	recipients    []int64
	configErr     error
	enableErr     error
}

func (s *fakeService) Stats(context.Context) (*service.Stats, error) {
	return s.stats, nil
}

func (s *fakeService) AddFeedback(_ context.Context, in service.FeedbackInput) error {
	if s.feedbackErr != nil {
		return s.feedbackErr
	}
	s.feedback = append(s.feedback, in)
	return nil
}

func (s *fakeService) Feedbacks(_ context.Context, p service.Page) ([]*service.Feedback, int64, error) {
	end := min(p.Skip+p.Limit, int64(len(s.feedbackList)))
	return s.feedbackList[min(p.Skip, end):end], int64(len(s.feedbackList)), nil
}

func (s *fakeService) BroadcastRecipients(context.Context) ([]int64, error) {
	return s.recipients, s.recipientsErr
}

func (s *fakeService) UnfinishedPayments(context.Context) ([]*service.Payment, error) {
	return s.unfinished, nil
}

func (s *fakeService) Payments(_ context.Context, userID int64) ([]*service.Payment, error) {
	s.askedPayments = append(s.askedPayments, userID)
	return s.payments, nil
}

func (s *fakeService) Tariffs() []service.Tariff {
	return []service.Tariff{
		{
			Days:  30,
			Stars: 150,
		},
		{
			Days:  365,
			Stars: 1500,
		},
	}
}

func (s *fakeService) Invoice(_ context.Context, in service.PurchaseInput) (*service.Invoice, error) {
	s.invoiceIn = in
	if s.invoiceErr != nil {
		return nil, s.invoiceErr
	}
	return &service.Invoice{
		Days:    in.Days,
		Stars:   150,
		Payload: "PAYLOAD",
	}, nil
}

func (s *fakeService) CheckPurchase(context.Context, service.PaymentInput) error {
	return s.checkErr
}

func (s *fakeService) Pay(context.Context, service.PaymentInput) (*service.PayResult, error) {
	return s.payRes, s.payErr
}

func (s *fakeService) MarkRefunded(_ context.Context, in service.RefundInput) error {
	chargeID := in.ChargeID
	s.refundedAs = append(s.refundedAs, in)
	if s.refundMarkErr != nil {
		return s.refundMarkErr
	}
	s.refunded = append(s.refunded, chargeID)
	for _, p := range s.payments {
		if p.ChargeID == chargeID {
			p.RefundedAt = time.Now()
		}
	}
	return nil
}

func (s *fakeService) Issue(_ context.Context, in service.IssueInput) (*service.Peer, error) {
	s.issued = append(s.issued, in)
	return &service.Peer{
		PublicKey: "NEW=",
		UserID:    in.UserID,
		Name:      in.Name,
		IP:        "10.8.1.20",
	}, nil
}

func (s *fakeService) User(_ context.Context, id int64) (*service.User, error) {
	s.askedUsers = append(s.askedUsers, id)
	if s.userErr != nil {
		return nil, s.userErr
	}
	if id == 0 {
		return nil, service.ErrNotFound
	}
	return &service.User{
		ID:        id,
		Username:  "bob",
		Role:      s.role,
		KeysCount: len(s.access),
	}, nil
}

func (s *fakeService) Users(_ context.Context, p service.Page) ([]*service.User, int64, error) {
	if s.usersErr != nil {
		return nil, 0, s.usersErr
	}
	end := min(p.Skip+p.Limit, int64(len(s.users)))
	return s.users[min(p.Skip, end):end], int64(len(s.users)), nil
}

func (s *fakeService) Key(_ context.Context, key string) (*service.Peer, error) {
	for _, a := range s.access {
		if a.Peer.PublicKey == key {
			return a.Peer, nil
		}
	}
	return nil, service.ErrNotFound
}

func (s *fakeService) Disable(_ context.Context, key string) error {
	s.calls = append(s.calls, "disable "+key)
	return nil
}

func (s *fakeService) Enable(_ context.Context, key string) error {
	s.calls = append(s.calls, "enable "+key)
	return s.enableErr
}

func (s *fakeService) Delete(_ context.Context, key string) error {
	s.calls = append(s.calls, "delete "+key)
	return nil
}

func (s *fakeService) Extend(_ context.Context, in service.ExtendInput) (*service.Peer, error) {
	s.calls = append(s.calls, fmt.Sprintf("extend %s %d", in.PublicKey, in.Days))
	return s.Key(context.Background(), in.PublicKey)
}

func (s *fakeService) ReissueKey(_ context.Context, k service.UserKey) (*service.Peer, error) {
	if s.ownKeyErr != nil {
		return nil, s.ownKeyErr
	}
	s.reissuedFor = append(s.reissuedFor, k)
	return s.reissued, nil
}

func (s *fakeService) DeleteOwnKey(_ context.Context, k service.UserKey) error {
	if s.ownKeyErr != nil {
		return s.ownKeyErr
	}
	s.deletedOwn = append(s.deletedOwn, k)
	return nil
}

func (s *fakeService) Access(_ context.Context, userID int64) ([]service.KeyInfo, error) {
	s.askedAccess = append(s.askedAccess, userID)
	return s.access, nil
}

func (s *fakeService) UserConfig(_ context.Context, k service.UserKey) (*service.KeyConfig, error) {
	s.askedKey = k
	if k.PublicKey == "NOPRIV=" {
		return nil, service.ErrNoPrivateKey
	}
	for _, a := range s.access {
		if a.Peer.PublicKey == k.PublicKey {
			return &service.KeyConfig{
				Peer: a.Peer,
				Conf: "[Interface]\n",
			}, nil
		}
	}
	return nil, service.ErrNotFound
}

func (s *fakeService) CreateKey(_ context.Context, in service.CreateKeyInput) (*service.Peer, error) {
	s.createdWith = append(s.createdWith, in)
	if strings.Contains(in.Name, "\n") {
		return nil, service.ErrBadKeyName
	}
	return s.created, s.createErr
}

func (s *fakeService) CheckCreateKey(context.Context, int64) error {
	return s.createErr
}

func (s *fakeService) ClientConfig(_ context.Context, key string) (string, error) {
	if s.configErr != nil {
		return "", s.configErr
	}
	return "[Interface]\nPrivateKey = " + key + "\n", nil
}

func (s *fakeService) Reconcile(context.Context) (*service.ReconcileReport, error) {
	return s.report, s.reportErr
}

func (s *fakeService) Register(_ context.Context, in service.RegisterInput) (*service.User, error) {
	s.registered = append(s.registered, in)
	return &service.User{
		ID:       in.ID,
		Username: in.Username,
		Role:     s.role,
	}, nil
}

func (s *fakeService) Admins(context.Context) ([]*service.User, error) {
	return s.admins, nil
}

func newRouter(svc *fakeService) (*router, *fakeSender) {
	return newRouterWith(
		svc,
		&config.Config{
			SupportContact: "@help_me",
		},
	)
}

// newRouterWith builds the notifier and the router from cfg, as main does
// (stamp files, the maintenance flag), with no load monitor.
func newRouterWith(svc *fakeService, cfg *config.Config) (*router, *fakeSender) {
	s := &fakeSender{
		fail: map[int64]bool{},
	}
	r := New(
		&Deps{
			Users:    svc,
			Keys:     svc,
			Billing:  svc,
			Ops:      svc,
			Feedback: svc,
			Sender:   s,
			Notifier: NewNotifier(
				svc,
				s,
				cfg,
				nil,
			),
			Config: cfg,
		},
	)
	return r, s
}

func startUpdate(text string) tgbot.Update {
	return tgbot.Update{
		Message: &tgbot.Message{
			Text: text,
			Chat: tgbot.Chat{
				ID:   42,
				Type: "private",
			},
			From: &tgbot.User{
				ID:       42,
				Username: "alice",
			},
		},
	}
}

func TestStartRegistersAndGreets(t *testing.T) {
	svc := &fakeService{
		role: service.RoleUser,
	}
	r, s := newRouter(svc)

	require.NoError(t, r.Handle(context.Background(), startUpdate("/start")))

	require.Equal(
		t,
		[]service.RegisterInput{
			{
				ID:       42,
				Username: "alice",
			},
		},
		svc.registered,
	)
	require.Len(t, s.sent, 1)
	require.Equal(t, int64(42), s.sent[0].ChatID)
	require.Contains(t, s.sent[0].Text, "Привет")
	require.Equal(t, "key:create", s.sent[0].Keyboard.InlineKeyboard[0][0].CallbackData)
	require.Equal(t, "my", s.sent[0].Keyboard.InlineKeyboard[1][0].CallbackData)
	require.Equal(t, "buy", s.sent[0].Keyboard.InlineKeyboard[2][0].CallbackData, "plain users can buy")
	require.Equal(t, "support", s.sent[0].Keyboard.InlineKeyboard[3][0].CallbackData)
	require.Equal(t, "terms", s.sent[0].Keyboard.InlineKeyboard[3][1].CallbackData)
}

func TestMenuCommandShowsTheSameMenuAsStart(t *testing.T) {
	svc := &fakeService{
		role: service.RoleUser,
	}
	r, s := newRouter(svc)

	require.NoError(t, r.Handle(context.Background(), startUpdate("/menu")))

	require.Len(t, svc.registered, 1, "registers like /start")
	require.Len(t, s.sent, 1)
	require.Equal(t, "key:create", s.sent[0].Keyboard.InlineKeyboard[0][0].CallbackData)
}

func TestTextsPointToMenuNotStart(t *testing.T) {
	for _, text := range []string{
		trialUsedText,
		configsNoticeText,
		unknownCommandText,
	} {
		require.NotContains(t, text, "/start")
		require.Contains(t, text, "/menu")
	}
}

func TestStartShowsAdminRole(t *testing.T) {
	r, s := newRouter(
		&fakeService{
			role: service.RoleAdmin,
		},
	)

	require.NoError(t, r.Handle(context.Background(), startUpdate("/start")))
	require.Contains(t, s.sent[0].Text, "админ")
}

func pressCreateKey() tgbot.Update {
	return tgbot.Update{
		CallbackQuery: &tgbot.CallbackQuery{
			ID:   "cb1",
			Data: "key:create",
			From: tgbot.User{
				ID: 42,
			},
			Message: &tgbot.Message{
				Chat: tgbot.Chat{
					ID:   42,
					Type: "private",
				},
			},
		},
	}
}

func TestCreateKeyStepOneAsksToInstallAnApp(t *testing.T) {
	r, s := newRouter(
		&fakeService{
			created: &service.Peer{
				PublicKey: "PUB=",
			},
		},
	)

	require.NoError(t, r.Handle(context.Background(), pressCreateKey()))

	require.Equal(t, []string{"cb1"}, s.answered, "button spinner stopped")
	require.Empty(t, s.files, "no key yet")
	require.Len(t, s.sent, 1)
	for _, app := range []string{
		"AmneziaVPN",
		"AmneziaWG",
		"WG Tunnel",
		"DefaultVPN",
		"AWG Manager",
	} {
		require.Contains(t, s.sent[0].Text, app)
	}
	kb := s.sent[0].Keyboard.InlineKeyboard
	require.Equal(t, "https://apps.apple.com/app/id1600529900", kb[0][0].URL)
	require.Equal(t, "https://play.google.com/store/apps/details?id=org.amnezia.vpn", kb[0][1].URL)
	require.Equal(t, "key:issue", kb[len(kb)-2][0].CallbackData, "next step")
	require.Equal(t, cbMenu, kb[len(kb)-1][0].CallbackData, "then back to the menu")
}

func TestCreateKeyStepTwoSendsKeyAndHowToImport(t *testing.T) {
	r, s := newRouter(
		&fakeService{
			created: &service.Peer{
				PublicKey: "PUB=",
				Name:      "tg:bob #2",
				ExpiresAt: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC),
			},
		},
	)

	require.NoError(t, r.Handle(context.Background(), press("key:issue")))
	require.NoError(t, r.Handle(context.Background(), press("key:noname")))

	require.Len(t, s.files, 2, "config and QR")
	conf, qr := s.files[0], s.files[1]
	require.Equal(t, "key_bob_2.conf", conf.Name)
	require.Equal(t, "[Interface]\nPrivateKey = PUB=\n", string(conf.Data))
	require.Contains(t, conf.Caption, "до 04.10.2026 15:00 по Москве", "12:00 UTC is 15:00 MSK")
	require.Equal(t, "qr.png", qr.Name)
	require.Equal(t, "\x89PNG", string(qr.Data[:4]))
	require.Len(t, s.sent, 2, "the name question, then the steps")
	require.Contains(t, s.sent[1].Text, "QR")
	require.Contains(t, s.sent[1].Text, "Подключиться")
	require.True(t, hasMenuButton(s.sent[1].Keyboard), "the last step leads back to the menu")
}

func TestCreateKeyAsksForANameFirst(t *testing.T) {
	svc := &fakeService{
		created: &service.Peer{
			PublicKey: "PUB=",
			Name:      "iPhone",
		},
	}
	r, s := newRouter(svc)
	ctx := context.Background()

	require.NoError(t, r.Handle(ctx, press("key:issue")))
	require.Empty(t, svc.createdWith, "no key yet")
	require.Empty(t, s.files)
	ask := s.sent[0]
	require.Contains(t, ask.Text, "Как назвать ключ")
	require.Equal(t, "key:noname", ask.Keyboard.InlineKeyboard[0][0].CallbackData, "skip = the old name")

	require.NoError(t, r.Handle(ctx, startUpdate("iPhone")))
	require.Equal(
		t,
		[]service.CreateKeyInput{
			{
				UserID: 42,
				Name:   "iPhone",
			},
		},
		svc.createdWith,
	)
	require.Len(t, s.files, 2, "the key and its QR")

	require.NoError(t, r.Handle(ctx, startUpdate("iPad")))
	require.Len(t, svc.createdWith, 1, "a later text is not another key")
}

func TestCreateKeySkipNameKeepsTheOldScheme(t *testing.T) {
	svc := &fakeService{
		created: &service.Peer{
			PublicKey: "PUB=",
			Name:      "tg:bob #1",
		},
	}
	r, s := newRouter(svc)
	ctx := context.Background()

	require.NoError(t, r.Handle(ctx, press("key:issue")))
	require.NoError(t, r.Handle(ctx, press("key:noname")))
	require.Equal(
		t,
		[]service.CreateKeyInput{
			{
				UserID: 42,
			},
		},
		svc.createdWith,
	)
	require.Len(t, s.files, 2)

	require.NoError(t, r.Handle(ctx, startUpdate("iPad")))
	require.Len(t, svc.createdWith, 1, "skipping ends the question")
}

func TestCreateKeyBadNameAsksAgain(t *testing.T) {
	svc := &fakeService{
		created: &service.Peer{
			PublicKey: "PUB=",
			Name:      "Mac",
		},
	}
	r, s := newRouter(svc)
	ctx := context.Background()

	require.NoError(t, r.Handle(ctx, press("key:issue")))
	require.NoError(t, r.Handle(ctx, startUpdate("two\nlines")))
	require.Empty(t, s.files)
	require.Contains(t, s.sent[len(s.sent)-1].Text, "до 32")
	require.Equal(t, "key:noname", s.sent[len(s.sent)-1].Keyboard.InlineKeyboard[0][0].CallbackData)

	require.NoError(t, r.Handle(ctx, startUpdate("Mac")))
	require.Len(t, s.files, 2, "still waiting for a name after the bad one")
}

func TestCreateKeyNameIgnoresMessagesWithoutText(t *testing.T) {
	svc := &fakeService{
		created: &service.Peer{
			PublicKey: "PUB=",
		},
	}
	r, s := newRouter(svc)
	ctx := context.Background()

	require.NoError(t, r.Handle(ctx, press("key:issue")))
	require.NoError(t, r.Handle(ctx, startUpdate(""))) // a sticker or a photo
	require.Empty(t, svc.createdWith, "only the skip button means no name")
	require.Equal(t, "key:noname", s.sent[len(s.sent)-1].Keyboard.InlineKeyboard[0][0].CallbackData)
}

// The split-tunneling step is gone (the server routes Russian addresses
// directly); its button in old messages does nothing.
func TestOldBypassButtonDoesNothing(t *testing.T) {
	r, s := newRouter(&fakeService{})

	require.NoError(t, r.Handle(context.Background(), press("bypass")))
	require.Empty(t, s.sent)
	require.Empty(t, s.files)
}

func TestCreateKeyForeverCaption(t *testing.T) {
	r, s := newRouter(
		&fakeService{
			created: &service.Peer{
				PublicKey: "PUB=",
				Name:      "tg:bob #1",
			},
		},
	)

	require.NoError(t, r.Handle(context.Background(), press("key:issue")))
	require.NoError(t, r.Handle(context.Background(), press("key:noname")))
	require.Contains(t, s.files[0].Caption, "бессрочный")
}

func TestCreateKeyErrorsExplained(t *testing.T) {
	for err, want := range map[error]string{
		service.ErrHasKey:    "уже есть ключ",
		service.ErrTrialUsed: "Пробный период уже использован",
		service.ErrKeyLimit:  "максимум — 3",
	} {
		r, s := newRouter(
			&fakeService{
				createErr: err,
			},
		)

		require.NoError(t, r.Handle(context.Background(), pressCreateKey()))
		require.Empty(t, s.files)
		require.Len(t, s.sent, 1)
		require.Contains(t, s.sent[0].Text, want)
	}
}

func TestCreateKeyUnexpectedErrorIsReturned(t *testing.T) {
	r, s := newRouter(
		&fakeService{
			createErr: errors.New("awg down"),
		},
	)

	err := r.Handle(context.Background(), pressCreateKey())
	require.ErrorContains(t, err, "awg down", "logged by the poll loop")
	require.Len(t, s.sent, 1)
	require.Contains(t, s.sent[0].Text, "Не получилось", "the user still gets an answer")
}

func press(data string) tgbot.Update {
	u := pressCreateKey()
	u.CallbackQuery.Data = data
	return u
}

func TestMyAccessListsKeys(t *testing.T) {
	msk := time.FixedZone("MSK", 3*60*60)
	r, s := newRouter(
		&fakeService{
			access: []service.KeyInfo{
				{
					Peer: &service.Peer{
						PublicKey: "PUB1=",
						Name:      "tg:bob",
						IP:        "10.8.1.10",
						Enabled:   true,
						ExpiresAt: time.Date(2026, 10, 4, 15, 5, 0, 0, msk),
					},
					Online:        true,
					LastHandshake: time.Date(2026, 9, 27, 15, 10, 0, 0, msk),
					Sent:          300 << 20,
					Received:      3 << 30,
				},
				{
					Peer: &service.Peer{
						PublicKey: "PUB2=",
						Name:      "tg:bob #2",
						IP:        "10.8.1.11",
					},
				},
			},
		},
	)

	require.NoError(t, r.Handle(context.Background(), press("my")))

	require.Len(t, s.sent, 1)
	text := s.sent[0].Text
	for _, want := range []string{
		"tg:bob — 10.8.1.10",
		"🟢 в сети",
		"до 04.10.2026 15:05 по Москве",
		"27.09.2026 15:10 по Москве",
		"↓ 3.0 ГБ",
		"↑ 300.0 МБ",
		"tg:bob #2 — 10.8.1.11",
		"⛔️ отключён",
		"никогда",
	} {
		require.Contains(t, text, want)
	}
	kb := s.sent[0].Keyboard.InlineKeyboard
	require.Equal(t, "cfg:PUB1=", kb[0][0].CallbackData)
	require.Equal(t, "kr?:PUB1=", kb[1][0].CallbackData, "each key: config row, then reissue / delete")
	require.Equal(t, "kd?:PUB1=", kb[1][1].CallbackData)
	require.Equal(t, "cfg:PUB2=", kb[2][0].CallbackData)
}

func TestMyAccessWithoutKeys(t *testing.T) {
	r, s := newRouter(&fakeService{})

	require.NoError(t, r.Handle(context.Background(), press("my")))
	require.Contains(t, s.sent[0].Text, "нет ключей")
	require.Equal(t, "key:create", s.sent[0].Keyboard.InlineKeyboard[0][0].CallbackData)
}

func TestConfigAgainSendsOwnKeyOnly(t *testing.T) {
	svc := &fakeService{
		access: []service.KeyInfo{
			{
				Peer: &service.Peer{
					PublicKey: "PUB1=",
					Name:      "tg:bob",
				},
			},
		},
	}
	r, s := newRouter(svc)

	require.NoError(t, r.Handle(context.Background(), press("cfg:PUB1=")))
	require.Equal(
		t,
		service.UserKey{
			UserID:    42,
			PublicKey: "PUB1=",
		},
		svc.askedKey,
		"asks for the presser's own key",
	)
	require.Len(t, s.files, 2, "config and QR")
	require.Equal(t, "key_bob.conf", s.files[0].Name)

	require.NoError(t, r.Handle(context.Background(), press("cfg:OTHER=")))
	require.Contains(t, s.sent[len(s.sent)-1].Text, "не найден")
}

func TestHumanBytes(t *testing.T) {
	require.Equal(t, "512 Б", humanBytes(512))
	require.Equal(t, "1.5 КБ", humanBytes(1536))
	require.Equal(t, "2.0 ПБ", humanBytes(2<<50))
	require.Equal(t, "2048.0 ПБ", humanBytes(2<<60), "no panic past the last unit")
}

func TestConfigOfImportedKeyExplains(t *testing.T) {
	r, s := newRouter(&fakeService{})

	require.NoError(t, r.Handle(context.Background(), press("cfg:NOPRIV=")))
	require.Empty(t, s.files)
	require.Contains(t, s.sent[0].Text, "только на устройстве")
}

func TestUnknownUpdateIsIgnored(t *testing.T) {
	r, s := newRouter(&fakeService{})

	require.NoError(t, r.Handle(context.Background(), startUpdate("hello")))
	require.NoError(t, r.Handle(context.Background(), tgbot.Update{}))
	require.Empty(t, s.sent)
}

func TestNotifyAdminsReachesEveryAdmin(t *testing.T) {
	r, s := newRouter(
		&fakeService{
			admins: []*service.User{
				{
					ID: 1,
				},
				{
					ID: 2,
				},
				{
					ID: 3,
				},
			},
		},
	)
	s.fail[2] = true

	r.notify.NotifyAdmins(context.Background(), "alert")

	require.Equal(
		t,
		[]outMessage{
			{
				ChatID: 1,
				Text:   "alert",
			},
			{
				ChatID: 3,
				Text:   "alert",
			},
		},
		s.sent,
		"one blocked admin doesn't stop the rest",
	)
}

func TestReconcileNotifiesAdminsOnlyOnDifferences(t *testing.T) {
	svc := &fakeService{
		admins: []*service.User{
			{
				ID: 1,
			},
		},
		report: &service.ReconcileReport{
			Manual: 9,
		},
	}
	r, s := newRouter(svc)

	r.Reconcile(context.Background())
	require.Empty(t, s.sent, "all good: log only")

	svc.report.MissingOnServer = []*service.Peer{
		{
			Name: "tg:alice",
			IP:   "10.8.1.10",
		},
	}
	r.Reconcile(context.Background())
	require.Len(t, s.sent, 1)
	require.Contains(t, s.sent[0].Text, "tg:alice")

	svc.reportErr = errors.New("awg down")
	r.Reconcile(context.Background())
	require.Len(t, s.sent, 2)
	require.Contains(t, s.sent[1].Text, "awg down")
}

func TestReconcileText(t *testing.T) {
	text := ReconcileText(
		&service.ReconcileReport{
			MissingOnServer: []*service.Peer{
				{
					Name: "tg:alice",
					IP:   "10.8.1.10",
				},
			},
			DisabledButOnServer: []*service.Peer{
				{
					Name: "tg:bob",
					IP:   "10.8.1.11",
				},
			},
			Manual: 9,
		},
	)

	require.Contains(t, text, "tg:alice (10.8.1.10)")
	require.Contains(t, text, "tg:bob (10.8.1.11)")
	require.Contains(t, text, "вручную: 9")
	require.Contains(t, text, "ничего не менял")
}

func TestMyAccessExtendButtonOnlyForPayingUsers(t *testing.T) {
	for role, canBuy := range map[service.Role]bool{
		service.RoleUser:      true,
		service.RoleUnlimited: false,
		service.RoleAdmin:     false,
	} {
		r, s := newRouter(
			&fakeService{
				role: role,
				access: []service.KeyInfo{
					{
						Peer: &service.Peer{
							PublicKey: "PUB1=",
							Name:      "tg:bob",
							ExpiresAt: time.Now().Add(24 * time.Hour),
						},
					},
				},
			},
		)

		require.NoError(t, r.Handle(context.Background(), press("my")))
		row := s.sent[0].Keyboard.InlineKeyboard[0]
		require.Equal(t, canBuy, len(row) == 2, role)
		if canBuy {
			require.Equal(t, cbBuyKey+"PUB1=", row[1].CallbackData)
		}
	}
}

func TestMyAccessBuyButtonOnlyForTimedKeys(t *testing.T) {
	forever := &service.Peer{
		PublicKey: "PUB1=",
		Name:      "tg:bob",
	}
	timed := &service.Peer{
		PublicKey: "PUB2=",
		Name:      "tg:bob #2",
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	r, s := newRouter(
		&fakeService{
			role: service.RoleUser,
			access: []service.KeyInfo{
				{Peer: forever},
				{Peer: timed},
			},
		},
	)

	require.NoError(t, r.Handle(context.Background(), press("my")))

	var got []string
	for _, row := range s.sent[0].Keyboard.InlineKeyboard {
		for _, b := range row {
			got = append(got, b.CallbackData)
		}
	}
	require.NotContains(t, got, "buyk:PUB1=")
	require.Contains(t, got, "buyk:PUB2=")
}

// CheckCreateKey passed at step 1, then the real CreateKey at step 2 says
// no (a key got there in between, or the server failed).
func TestCreateKeyErrorsAtTheCreateStep(t *testing.T) {
	cases := map[string]struct {
		err    error
		text   string
		logged bool
	}{
		"has a key": {
			err:  service.ErrHasKey,
			text: hasKeyText,
		},
		"trial used": {
			err:  service.ErrTrialUsed,
			text: trialUsedText,
		},
		"key limit": {
			err:  service.ErrKeyLimit,
			text: keyLimitText,
		},
		"server down": {
			err:    errors.New("awg down"),
			text:   internalErrorText,
			logged: true,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			svc := &fakeService{}
			r, s := newRouter(svc)
			ctx := context.Background()
			require.NoError(t, r.Handle(ctx, press(cbIssueKey)))
			svc.createErr = c.err

			err := r.Handle(ctx, press(cbKeyNoName))

			require.Len(t, svc.createdWith, 1, "it got to the real create")
			require.Equal(t, c.logged, err != nil, "only an unexpected error is returned for the log")
			require.Equal(t, c.text, s.sent[len(s.sent)-1].Text)
			require.Empty(t, s.files)
		})
	}
}

func TestConfigTooLongForAQRCodeGetsANote(t *testing.T) {
	r, s := newRouter(&fakeService{})

	err := r.sendQR(
		context.Background(),
		outFile{
			ChatID: 42,
			Data:   []byte(strings.Repeat("x", 4000)), // a QR code holds under 3000 bytes
		},
	)
	require.NoError(t, err)
	require.Empty(t, s.files, "no picture")
	require.Equal(t, qrTooLongText, s.sent[0].Text)
}
