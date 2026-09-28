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

	"github.com/irbgeo/geoirb-vpn-bot/internal/bypass"
	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

type fakeSender struct {
	editErr   error
	mu        sync.Mutex
	invoices  []*OutInvoice
	answers   []PreCheckoutAnswer
	refunds   []RefundInput
	refundErr error
	edits     []EditMessage
	sent      []OutMessage
	files     []OutFile
	answered  []string
	fail      map[int64]bool
}

func (f *fakeSender) SendInvoice(_ context.Context, m *OutInvoice) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.invoices = append(f.invoices, m)
	return nil
}

func (f *fakeSender) AnswerPreCheckout(_ context.Context, a PreCheckoutAnswer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers = append(f.answers, a)
	return nil
}

func (f *fakeSender) Refund(ctx context.Context, in RefundInput) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err // like a real API call on a cancelled context
	}
	f.refunds = append(f.refunds, in)
	return f.refundErr
}

func (f *fakeSender) Edit(_ context.Context, m EditMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.editErr != nil {
		return f.editErr
	}
	f.edits = append(f.edits, m)
	return nil
}

func (f *fakeSender) SendDocument(_ context.Context, m OutFile) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files = append(f.files, m)
	return nil
}

func (f *fakeSender) SendPhoto(_ context.Context, m OutFile) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files = append(f.files, m)
	return nil
}

func (f *fakeSender) Answer(_ context.Context, callbackID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answered = append(f.answered, callbackID)
	return nil
}

func (f *fakeSender) Send(_ context.Context, m OutMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail[m.ChatID] {
		return errors.New("blocked")
	}
	f.sent = append(f.sent, m)
	return nil
}

type fakeService struct {
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
	payments      []*service.Payment
	unfinished    []*service.Payment
	stats         *service.Stats
	recipients    []int64
	configErr     error
	enableErr     error
}

func (f *fakeService) Stats(context.Context) (*service.Stats, error) {
	return f.stats, nil
}

func (f *fakeService) AddFeedback(_ context.Context, in service.FeedbackInput) error {
	if f.feedbackErr != nil {
		return f.feedbackErr
	}
	f.feedback = append(f.feedback, in)
	return nil
}

func (f *fakeService) Feedbacks(_ context.Context, p service.Page) ([]*service.Feedback, int64, error) {
	end := min(p.Skip+p.Limit, int64(len(f.feedbackList)))
	return f.feedbackList[min(p.Skip, end):end], int64(len(f.feedbackList)), nil
}

func (f *fakeService) BroadcastRecipients(context.Context) ([]int64, error) {
	return f.recipients, f.recipientsErr
}

func (f *fakeService) UnfinishedPayments(context.Context) ([]*service.Payment, error) {
	return f.unfinished, nil
}

func (f *fakeService) Payments(context.Context, int64) ([]*service.Payment, error) {
	return f.payments, nil
}

func (f *fakeService) Tariffs() []service.Tariff {
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

func (f *fakeService) Invoice(_ context.Context, in service.PurchaseInput) (*service.Invoice, error) {
	f.invoiceIn = in
	if f.invoiceErr != nil {
		return nil, f.invoiceErr
	}
	return &service.Invoice{
		Days:    in.Days,
		Stars:   150,
		Payload: "PAYLOAD",
	}, nil
}

func (f *fakeService) CheckPurchase(context.Context, service.PaymentInput) error {
	return f.checkErr
}

func (f *fakeService) Pay(context.Context, service.PaymentInput) (*service.PayResult, error) {
	return f.payRes, f.payErr
}

func (f *fakeService) MarkRefunded(_ context.Context, chargeID string) error {
	if f.refundMarkErr != nil {
		return f.refundMarkErr
	}
	f.refunded = append(f.refunded, chargeID)
	for _, p := range f.payments {
		if p.ChargeID == chargeID {
			p.RefundedAt = time.Now()
		}
	}
	return nil
}

func (f *fakeService) Issue(_ context.Context, in service.IssueInput) (*service.Peer, error) {
	f.issued = append(f.issued, in)
	return &service.Peer{
		PublicKey: "NEW=",
		UserID:    in.UserID,
		Name:      in.Name,
		IP:        "10.8.1.20",
	}, nil
}

func (f *fakeService) User(_ context.Context, id int64) (*service.User, error) {
	if id == 0 {
		return nil, service.ErrNotFound
	}
	return &service.User{
		ID:        id,
		Username:  "bob",
		Role:      f.role,
		KeysCount: len(f.access),
	}, nil
}

func (f *fakeService) Users(_ context.Context, p service.Page) ([]*service.User, int64, error) {
	end := min(p.Skip+p.Limit, int64(len(f.users)))
	return f.users[min(p.Skip, end):end], int64(len(f.users)), nil
}

func (f *fakeService) Key(_ context.Context, key string) (*service.Peer, error) {
	for _, a := range f.access {
		if a.Peer.PublicKey == key {
			return a.Peer, nil
		}
	}
	return nil, service.ErrNotFound
}

func (f *fakeService) Disable(_ context.Context, key string) error {
	f.calls = append(f.calls, "disable "+key)
	return nil
}

func (f *fakeService) Enable(_ context.Context, key string) error {
	f.calls = append(f.calls, "enable "+key)
	return f.enableErr
}

func (f *fakeService) Delete(_ context.Context, key string) error {
	f.calls = append(f.calls, "delete "+key)
	return nil
}

func (f *fakeService) Extend(_ context.Context, in service.ExtendInput) (*service.Peer, error) {
	f.calls = append(f.calls, fmt.Sprintf("extend %s %d", in.PublicKey, in.Days))
	return f.Key(context.Background(), in.PublicKey)
}

func (f *fakeService) ReissueKey(_ context.Context, k service.UserKey) (*service.Peer, error) {
	if f.ownKeyErr != nil {
		return nil, f.ownKeyErr
	}
	f.reissuedFor = append(f.reissuedFor, k)
	return f.reissued, nil
}

func (f *fakeService) DeleteOwnKey(_ context.Context, k service.UserKey) error {
	if f.ownKeyErr != nil {
		return f.ownKeyErr
	}
	f.deletedOwn = append(f.deletedOwn, k)
	return nil
}

func (f *fakeService) Access(context.Context, int64) ([]service.KeyInfo, error) {
	return f.access, nil
}

func (f *fakeService) UserConfig(_ context.Context, k service.UserKey) (*service.KeyConfig, error) {
	f.askedKey = k
	if k.PublicKey == "NOPRIV=" {
		return nil, service.ErrNoPrivateKey
	}
	for _, a := range f.access {
		if a.Peer.PublicKey == k.PublicKey {
			return &service.KeyConfig{
				Peer: a.Peer,
				Conf: "[Interface]\n",
			}, nil
		}
	}
	return nil, service.ErrNotFound
}

func (f *fakeService) CreateKey(_ context.Context, in service.CreateKeyInput) (*service.Peer, error) {
	f.createdWith = append(f.createdWith, in)
	if strings.Contains(in.Name, "\n") {
		return nil, service.ErrBadKeyName
	}
	return f.created, f.createErr
}

func (f *fakeService) CheckCreateKey(context.Context, int64) error {
	return f.createErr
}

func (f *fakeService) ClientConfig(_ context.Context, key string) (string, error) {
	if f.configErr != nil {
		return "", f.configErr
	}
	return "[Interface]\nPrivateKey = " + key + "\n", nil
}

func (f *fakeService) Reconcile(context.Context) (*service.ReconcileReport, error) {
	return f.report, f.reportErr
}

func (f *fakeService) Register(_ context.Context, in service.RegisterInput) (*service.User, error) {
	f.registered = append(f.registered, in)
	return &service.User{
		ID:       in.ID,
		Username: in.Username,
		Role:     f.role,
	}, nil
}

func (f *fakeService) Admins(context.Context) ([]*service.User, error) {
	return f.admins, nil
}

type fakeBypass struct {
	err error
}

func (f *fakeBypass) Files(context.Context) ([]bypass.File, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []bypass.File{
		{
			Name: bypass.ComputerList,
			Data: []byte("[]"),
		},
		{
			Name: bypass.PhoneList,
			Data: []byte("[]"),
		},
	}, nil
}

func newRouter(svc *fakeService) (*Router, *fakeSender) {
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
				&NotifierDeps{
					Users:  svc,
					Sender: s,
				},
			),
			Bypass:         &fakeBypass{},
			SupportContact: "@help_me",
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
	require.Equal(t, "bypass", s.sent[0].Keyboard.InlineKeyboard[3][0].CallbackData)
	require.Equal(t, "support", s.sent[0].Keyboard.InlineKeyboard[4][0].CallbackData)
	require.Equal(t, "terms", s.sent[0].Keyboard.InlineKeyboard[4][1].CallbackData)
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

	require.NoError(t, r.Handle(context.Background(), press("key:noname")))

	require.Len(t, s.files, 2, "config and QR; the lists come in step 3")
	conf, qr := s.files[0], s.files[1]
	require.Equal(t, "vpn_bob_2.conf", conf.Name)
	require.Equal(t, "[Interface]\nPrivateKey = PUB=\n", string(conf.Data))
	require.Contains(t, conf.Caption, "до 04.10.2026 15:00 по Москве", "12:00 UTC is 15:00 MSK")
	require.Equal(t, "qr.png", qr.Name)
	require.Equal(t, "\x89PNG", string(qr.Data[:4]))
	require.Len(t, s.sent, 1)
	require.Contains(t, s.sent[0].Text, "QR")
	require.Contains(t, s.sent[0].Text, "Подключиться")
	require.Equal(t, "bypass", s.sent[0].Keyboard.InlineKeyboard[0][0].CallbackData, "next step")
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

func TestCreateKeyStepThreeTunnelingThenFiles(t *testing.T) {
	r, s := newRouter(&fakeService{})

	require.NoError(t, r.Handle(context.Background(), press("bypass")))
	require.Len(t, s.sent, 1)
	require.Contains(t, s.sent[0].Text, "Адреса из списка НЕ должны использовать VPN")
	require.Len(t, s.files, 2)
	require.Equal(t, bypass.ComputerList, s.files[0].Name)
	require.Contains(t, s.files[0].Caption, "только для компьютера")
	require.Equal(t, bypass.PhoneList, s.files[1].Name)
	require.Contains(t, s.files[1].Caption, "для телефона")
}

func TestBypassDownFallsBackToNote(t *testing.T) {
	r, s := newRouter(&fakeService{})
	r.bypass = &fakeBypass{
		err: errors.New("github down"),
	}

	require.NoError(t, r.Handle(context.Background(), press("bypass")))
	require.Empty(t, s.files)
	require.Contains(t, s.sent[len(s.sent)-1].Text, "недоступен")
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
			createErr: errors.New("docker down"),
		},
	)

	err := r.Handle(context.Background(), pressCreateKey())
	require.ErrorContains(t, err, "docker down", "logged by the poll loop")
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
	require.Len(t, s.files, 2, "config and QR, no bypass lists again")
	require.Equal(t, "vpn_bob.conf", s.files[0].Name)

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
		[]OutMessage{
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

	svc.reportErr = errors.New("docker down")
	r.Reconcile(context.Background())
	require.Len(t, s.sent, 2)
	require.Contains(t, s.sent[1].Text, "docker down")
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
