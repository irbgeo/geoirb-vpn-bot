package bot

import (
	"context"
	"testing"

	tgbot "github.com/irbgeo/go-tgbot"
	"github.com/stretchr/testify/require"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// Audit 2026-10-26, finding 13: a key bought from a group chat goes to the
// payer in private, never into the group.
func TestPaidKeyGoesToThePayerNotTheGroup(t *testing.T) {
	svc := &fakeService{
		payRes: &service.PayResult{
			Peer: &service.Peer{
				PublicKey: "PUB1=",
				Name:      "tg:bob",
			},
			NewKey: true,
			Days:   30,
		},
	}
	r, s := newRouter(svc)
	upd := paid()
	upd.Message.Chat = tgbot.Chat{
		ID:   -100500,
		Type: "supergroup",
	}

	require.NoError(t, r.Handle(context.Background(), upd))

	for _, m := range s.sent {
		require.NotEqual(t, int64(-100500), m.ChatID, "nothing in the group")
	}
	for _, f := range s.files {
		require.Equal(t, int64(42), f.ChatID, "the config goes to the payer")
	}
	require.NotEmpty(t, s.files)
}

// Finding 14: buttons that read the server are rate-limited too.
func TestReadButtonsAreRateLimited(t *testing.T) {
	for _, data := range []string{cbMyAccess, cbBuy, cbReissueAsk + "PUB1=", cbDeleteAsk + "PUB1=", cbMyCompany} {
		r, s := newRouter(
			&fakeService{
				company:    romashka(),
				companyErr: service.ErrNotLeader,
			},
		)
		for range configsPerMinute {
			require.NoError(t, r.Handle(context.Background(), press(data)))
		}
		before := len(s.sent)

		require.NoError(t, r.Handle(context.Background(), press(data)))
		require.Equal(t, tooOftenText, s.sent[before].Text, data)
	}
}

// Finding 6: invite links are rate-limited per user.
func TestInviteLinkIsRateLimited(t *testing.T) {
	svc := &fakeService{
		company: romashka(),
		joinErr: service.ErrCompanyFull,
	}
	r, s := newRouter(svc)
	for range joinsPerMinute {
		require.NoError(t, r.Handle(context.Background(), startUpdate("/start c_inv23456")))
	}

	require.NoError(t, r.Handle(context.Background(), startUpdate("/start c_inv23456")))
	require.Len(t, svc.joined, joinsPerMinute)
	require.Equal(t, tooOftenText, s.sent[len(s.sent)-1].Text)
}

// Finding 6: the leader replaces a leaked link.
func TestLeaderRotatesTheInviteLink(t *testing.T) {
	svc := &fakeService{
		role:      service.RoleMember,
		companyID: "abcd2345",
		company:   romashka(),
	}
	r, s := newRouter(svc)

	require.NoError(t, r.Handle(context.Background(), press(cbMyCompany)))
	require.Contains(t, callbacksOf(s.sent[0].Keyboard), cbCompanyRotate+"abcd2345")

	require.NoError(t, r.Handle(context.Background(), press(cbCompanyRotate+"abcd2345")))
	require.Equal(t, []service.RotateInviteInput{{ActorID: 42, CompanyID: "abcd2345"}}, svc.rotated)
	require.Contains(t, s.sent[1].Text, "https://t.me/testbot?start=c_new23456")
}

// Finding 18: admin buttons take only the values their keyboards offer.
func TestAdminButtonValuesAreBounded(t *testing.T) {
	svc := adminService()
	svc.company = romashka()
	r, _ := newRouter(svc)
	ctx := context.Background()

	require.Error(t, r.Handle(ctx, press(cbAdminIssD+"7:-30")))
	require.Error(t, r.Handle(ctx, press(cbAdminIssD+"7:5")))
	require.Empty(t, svc.issued)

	require.Error(t, r.Handle(ctx, press(cbAdminCoExt+"abcd2345:120")))
	require.Empty(t, svc.extendedCo)

	require.NoError(t, r.Handle(ctx, press(cbAdminCoTerm+"7:2")))
	require.NoError(t, r.Handle(ctx, startUpdate("Ромашка")))
	require.Empty(t, svc.createdCo, "a term the keyboard does not offer")
}

func TestAuditErrorTexts(t *testing.T) {
	text, known := companyJoinErrorText(service.ErrBlocked)
	require.True(t, known)
	require.Contains(t, text, "отключил администратор")
	text, known = companyJoinErrorText(service.ErrCompanyKeys)
	require.True(t, known)
	require.Contains(t, text, "Мой доступ")
	text, known = createKeyErrorText(service.ErrTrialBusy)
	require.True(t, known)
	require.Contains(t, text, "через час")
	text, known = createKeyErrorText(service.ErrBlocked)
	require.True(t, known)
	require.Contains(t, text, "отключил администратор")
	require.Contains(t, expiredText(&service.Peer{Name: "tg:bob"}), "30 дней", "the user knows the key will go")
}
