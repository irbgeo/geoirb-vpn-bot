package bot

import (
	"context"
	"testing"
	"time"

	tgbot "github.com/irbgeo/go-tgbot"
	"github.com/stretchr/testify/require"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// romashka: leader 42 (@boss) and one employee 7 (@ivan), paid till April.
func romashka() *service.CompanyInfo {
	return &service.CompanyInfo{
		Company: &service.Company{
			ID:            "abcd2345",
			Name:          "Ромашка",
			LeaderID:      42,
			PaidUntil:     time.Date(2027, 4, 26, 9, 0, 0, 0, time.UTC),
			MaxMembers:    20,
			KeysPerMember: 2,
		},
		Members: []*service.User{
			{ID: 42, Username: "boss", Role: service.RoleMember},
			{ID: 7, Username: "ivan", Role: service.RoleMember},
		},
	}
}

func callbacksOf(kb *tgbot.InlineKeyboardMarkup) []string {
	if kb == nil {
		return nil
	}
	return callbacks(kb.InlineKeyboard)
}

func TestInviteLinkJoinsTheCompany(t *testing.T) {
	svc := &fakeService{
		role:    service.RoleUser,
		company: romashka(),
	}
	r, s := newRouter(svc)

	require.NoError(t, r.Handle(context.Background(), startUpdate("/start c_abcd2345")))

	require.Len(t, svc.registered, 1, "registered before joining")
	require.Equal(t, []service.JoinCompanyInput{{UserID: 42, CompanyID: "abcd2345"}}, svc.joined)
	require.Contains(t, s.sent[0].Text, "«Ромашка»")
	require.Contains(t, s.sent[0].Text, "26.04.2027")
	got := callbacksOf(s.sent[0].Keyboard)
	require.Contains(t, got, cbCreateKey)
	require.NotContains(t, got, cbBuy, "the company pays")
}

func TestInviteLinkErrorsExplained(t *testing.T) {
	cases := map[error]string{
		service.ErrNoCompany:    companyLinkBadText,
		service.ErrCompanyFull:  companyFullText,
		service.ErrCompanyEnded: companyEndedJoinText,
		service.ErrInCompany:    inOtherCompanyText,
		service.ErrCompanyRole:  adminNoCompanyText,
	}
	for cause, want := range cases {
		r, s := newRouter(
			&fakeService{
				company: romashka(),
				joinErr: cause,
			},
		)

		require.NoError(t, r.Handle(context.Background(), startUpdate("/start c_abcd2345")), "expected: not logged")
		require.Equal(t, want, s.sent[0].Text)
	}
}

func TestPlainStartDoesNotJoin(t *testing.T) {
	svc := &fakeService{}
	r, _ := newRouter(svc)

	require.NoError(t, r.Handle(context.Background(), startUpdate("/start")))
	require.NoError(t, r.Handle(context.Background(), startUpdate("/start other")))
	require.Empty(t, svc.joined)
}

func TestLeaderMenuHasTheCompanyButtonEmployeeDoesNot(t *testing.T) {
	leader := &fakeService{
		role:      service.RoleMember,
		companyID: "abcd2345",
		company:   romashka(),
	}
	r, s := newRouter(leader)
	require.NoError(t, r.Handle(context.Background(), startUpdate("/menu")))
	require.Contains(t, callbacksOf(s.sent[0].Keyboard), cbMyCompany)
	require.NotContains(t, callbacksOf(s.sent[0].Keyboard), cbBuy)

	employee := &fakeService{
		role:       service.RoleMember,
		companyID:  "abcd2345",
		companyErr: service.ErrNotLeader,
	}
	r, s = newRouter(employee)
	require.NoError(t, r.Handle(context.Background(), startUpdate("/menu")))
	require.NotContains(t, callbacksOf(s.sent[0].Keyboard), cbMyCompany)
}

func TestLeaderCardShowsTheLinkAndTheStaff(t *testing.T) {
	r, s := newRouter(
		&fakeService{
			role:      service.RoleMember,
			companyID: "abcd2345",
			company:   romashka(),
		},
	)

	require.NoError(t, r.Handle(context.Background(), press(cbMyCompany)))

	text := s.sent[0].Text
	require.Contains(t, text, "«Ромашка»")
	require.Contains(t, text, "https://t.me/testbot?start=c_abcd2345")
	require.Contains(t, text, "1 из 20")
	require.Contains(t, text, "@ivan")
	got := callbacksOf(s.sent[0].Keyboard)
	require.Contains(t, got, cbCompanyRemoveAsk+"abcd2345:7")
	require.NotContains(t, got, cbCompanyRemoveAsk+"abcd2345:42", "the leader can't remove themselves")
	require.NotContains(t, got, cbAdminCoExt+"abcd2345:6", "extending is the admin's")
}

func TestLeaderRemovesAnEmployee(t *testing.T) {
	svc := &fakeService{
		role:      service.RoleMember,
		companyID: "abcd2345",
		company:   romashka(),
	}
	r, s := newRouter(svc)

	require.NoError(t, r.Handle(context.Background(), press(cbCompanyRemoveAsk+"abcd2345:7")))
	require.Contains(t, s.sent[0].Text, "@ivan")
	require.Contains(t, callbacksOf(s.sent[0].Keyboard), cbCompanyRemove+"abcd2345:7")
	require.Empty(t, svc.removed, "only asked")

	require.NoError(t, r.Handle(context.Background(), press(cbCompanyRemove+"abcd2345:7")))
	require.Equal(t, []service.RemoveMemberInput{{ActorID: 42, CompanyID: "abcd2345", UserID: 7}}, svc.removed)
	require.Contains(t, s.sent[len(s.sent)-1].Text, "«Ромашка»", "the card again")
}

func TestRemoveRefusedForNonLeader(t *testing.T) {
	r, s := newRouter(
		&fakeService{
			role:      service.RoleMember,
			company:   romashka(),
			removeErr: service.ErrNotLeader,
		},
	)

	require.NoError(t, r.Handle(context.Background(), press(cbCompanyRemove+"abcd2345:7")), "expected: not logged")
	require.Equal(t, notLeaderText, s.sent[0].Text)
}

func TestAdminCreatesACompanyFromTheUserCard(t *testing.T) {
	svc := adminService()
	svc.company = romashka()
	r, s := newRouter(svc)
	ctx := context.Background()

	require.NoError(t, r.Handle(ctx, press(cbAdminUser+"7")))
	require.Contains(t, buttons(s.edits[0]), cbAdminCoNew+"7")

	require.NoError(t, r.Handle(ctx, press(cbAdminCoNew+"7")))
	require.Contains(t, buttons(s.edits[1]), cbAdminCoTerm+"7:6")

	require.NoError(t, r.Handle(ctx, press(cbAdminCoTerm+"7:6")))
	require.Equal(t, askCompanyNameText, s.sent[0].Text)

	require.NoError(t, r.Handle(ctx, startUpdate("Ромашка")))
	require.Equal(t, []service.CreateCompanyInput{{LeaderID: 7, Name: "Ромашка", Months: 6}}, svc.createdCo)
	require.Contains(t, s.sent[1].Text, "https://t.me/testbot?start=c_abcd2345")
	require.Contains(t, callbacksOf(s.sent[1].Keyboard), cbAdminCoExt+"abcd2345:6")
}

func TestAdminCompanyListAndExtend(t *testing.T) {
	svc := adminService()
	svc.company = romashka()
	svc.companyList = []*service.Company{svc.company.Company}
	r, s := newRouter(svc)
	ctx := context.Background()

	require.NoError(t, r.Handle(ctx, startUpdate("/menu")))
	require.Contains(t, callbacksOf(s.sent[0].Keyboard), cbAdminCos)

	require.NoError(t, r.Handle(ctx, press(cbAdminCos)))
	require.Contains(t, buttons(s.edits[0]), cbAdminCo+"abcd2345")

	require.NoError(t, r.Handle(ctx, press(cbAdminCo+"abcd2345")))
	require.Contains(t, s.edits[1].Text, "«Ромашка»")

	require.NoError(t, r.Handle(ctx, press(cbAdminCoExt+"abcd2345:6")))
	require.Equal(t, []service.ExtendCompanyInput{{CompanyID: "abcd2345", Months: 6}}, svc.extendedCo)
	require.NoError(t, r.Handle(ctx, press(cbAdminCoExt+"abcd2345:6")))
	require.Len(t, svc.extendedCo, 1, "a double press extends once")
}

func TestCompanyNotices(t *testing.T) {
	svc := &fakeService{
		admins: []*service.User{
			{ID: 1},
		},
	}
	r, s := newRouter(svc)
	m := maintenance()
	m.CompanyExpired = []service.CompanyKey{
		{
			Peer: &service.Peer{
				PublicKey: "PUB1=",
				UserID:    7,
				Name:      "tg:ivan",
			},
			Company: "Ромашка",
		},
	}
	m.CompanyRemind = []service.CompanyRemind{
		{
			Company: romashka().Company,
			Days:    3,
		},
	}

	r.notify.DeliverMaintenance(context.Background(), m)

	require.Equal(t, int64(7), s.sent[0].ChatID)
	require.Contains(t, s.sent[0].Text, "Доступ компании «Ромашка» закончился")
	require.Nil(t, s.sent[0].Keyboard, "nothing to buy")
	got := map[int64]string{}
	for _, msg := range s.sent[1:] {
		got[msg.ChatID] = msg.Text
	}
	require.Contains(t, got[42], "«Ромашка»", "the leader")
	require.Contains(t, got[42], "26.04.2027")
	require.Contains(t, got[1], "«Ромашка»", "the admins")
}

func TestMemberCannotBuy(t *testing.T) {
	text, known := invoiceErrorText(service.ErrCompanyPaid)
	require.True(t, known)
	require.Equal(t, companyPaysText, text)
	require.Equal(t, companyPaysText, preCheckoutErrorText(service.ErrCompanyPaid))
	text, known = createKeyErrorText(service.ErrCompanyEnded)
	require.True(t, known)
	require.Contains(t, text, "компании закончился")
}

func TestCompanyErrorsExplained(t *testing.T) {
	cases := map[error]string{
		service.ErrNotLeader: notLeaderText,
		service.ErrIsLeader:  isLeaderText,
		service.ErrNotFound:  memberGoneText,
		service.ErrNoCompany: companyLinkBadText,
	}
	for cause, want := range cases {
		text, known := companyErrorText(cause)
		require.True(t, known)
		require.Equal(t, want, text)
	}
	_, known := companyErrorText(context.DeadlineExceeded)
	require.False(t, known, "an unexpected error goes to the log")
}

func TestAdminRemovesFromAnyCompanyAndSeesTheAdminCard(t *testing.T) {
	svc := adminService()
	svc.company = romashka()
	r, s := newRouter(svc)
	ctx := context.Background()

	require.NoError(t, r.Handle(ctx, press(cbCompanyRemoveAsk+"abcd2345:7")))
	require.Contains(t, s.sent[0].Text, "@ivan")

	require.NoError(t, r.Handle(ctx, press(cbCompanyRemove+"abcd2345:7")))
	require.Contains(t, callbacksOf(s.sent[1].Keyboard), cbAdminCoExt+"abcd2345:6", "an admin gets the admin card back")
}

func TestLeaderCannotOpenAnotherCompany(t *testing.T) {
	r, s := newRouter(
		&fakeService{
			role:    service.RoleMember,
			company: romashka(),
		},
	)

	require.NoError(t, r.Handle(context.Background(), press(cbCompanyRemoveAsk+"zzzz9999:7")), "expected: not logged")
	require.Equal(t, notLeaderText, s.sent[0].Text)
}

func TestEndedCompanyCardAndEmptyList(t *testing.T) {
	info := romashka()
	info.Company.PaidUntil = time.Now().Add(-time.Hour)
	info.Members = info.Members[:1]
	v := companyView{
		Info: info,
		Link: "/start c_abcd2345",
	}

	text := companyText(v)
	require.Contains(t, text, "Срок закончился")
	require.Contains(t, text, "Сотрудников пока нет")
	require.Contains(t, companiesText(nil), "Компаний пока нет")
	remind := companyRemindText(service.CompanyRemind{Company: info.Company, Days: 1})
	require.Contains(t, remind, "меньше суток")
}
