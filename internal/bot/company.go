package bot

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strconv"
	"strings"

	tgbot "github.com/irbgeo/go-tgbot"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// The values the admin keyboards offer; a forged button with any other
// value is refused.
var (
	companyTerms = []int{1, 3, 6, 12} // months of a new company
	extendMonths = []int{1, 6}        // months added by «➕»
	issueDays    = []int{0, 7, 30, 90, 365}
)

// joinCompany handles /start with an invite code: the user is registered,
// joins the company and gets the menu of a member.
func (s *router) joinCompany(ctx context.Context, in joinRequest) error {
	m := in.Message
	registerInput := service.RegisterInput{
		ID:       m.From.ID,
		Username: m.From.Username,
	}
	_, err := s.users.Register(ctx, registerInput)
	if err != nil {
		return err
	}
	joinCompanyInput := service.JoinCompanyInput{
		UserID: m.From.ID,
		Invite: in.Code,
	}
	c, err := s.companies.JoinCompany(ctx, joinCompanyInput)
	if err != nil {
		text, known := companyJoinErrorText(err)
		userError := userError{
			ChatID: m.Chat.ID,
			Err:    err,
			Text:   text,
			Known:  known,
		}
		return s.replyError(ctx, userError)
	}

	u, err := s.users.User(ctx, m.From.ID) // the role is member now
	if err != nil {
		return err
	}
	menu := s.menuFor(ctx, u)
	outMessage := outMessage{
		ChatID:   m.Chat.ID,
		Text:     companyJoinedText(c),
		Keyboard: menu.Keyboard,
	}
	return s.send.Send(ctx, outMessage)
}

// myCompany is «🏢 Моя компания»: the leader's card.
func (s *router) myCompany(ctx context.Context, cq *tgbot.CallbackQuery) error {
	info, err := s.companies.MyCompany(ctx, cq.SenderID())
	if err != nil {
		failure := errorReport{
			ChatID: cq.ChatID(),
			Err:    err,
		}
		return s.companyError(ctx, failure)
	}
	companyCard := companyCard{
		ChatID: cq.ChatID(),
		Info:   info,
		Admin:  false,
	}
	return s.sendCompanyCard(ctx, companyCard)
}

// companyRemoveAsk asks the leader (or an admin) to confirm removing an
// employee.
func (s *router) companyRemoveAsk(ctx context.Context, cq *tgbot.CallbackQuery) error {
	arg := strings.TrimPrefix(cq.Data, cbCompanyRemoveAsk)
	ref, err := parseMemberRef(arg)
	if err != nil {
		return err
	}
	companyAccess := companyAccess{
		ActorID:   cq.SenderID(),
		CompanyID: ref.CompanyID,
	}
	info, _, err := s.companyFor(ctx, companyAccess)
	if err != nil {
		failure := errorReport{
			ChatID: cq.ChatID(),
			Err:    err,
		}
		return s.companyError(ctx, failure)
	}

	removeAsk := removeAsk{
		Info:   info,
		UserID: ref.UserID,
	}
	text := removeMemberAskText(removeAsk)
	keyboard := confirmKeyboard(cbCompanyRemove + ref.String())
	outMessage := outMessage{
		ChatID:   cq.ChatID(),
		Text:     text,
		Keyboard: keyboard,
	}
	return s.send.Send(ctx, outMessage)
}

// companyRemove removes the employee and shows the card again.
func (s *router) companyRemove(ctx context.Context, cq *tgbot.CallbackQuery) error {
	arg := strings.TrimPrefix(cq.Data, cbCompanyRemove)
	ref, err := parseMemberRef(arg)
	if err != nil {
		return err
	}
	removeMemberInput := service.RemoveMemberInput{
		ActorID:   cq.SenderID(),
		CompanyID: ref.CompanyID,
		UserID:    ref.UserID,
	}
	err = s.companies.RemoveMember(ctx, removeMemberInput)
	if err != nil {
		failure := errorReport{
			ChatID: cq.ChatID(),
			Err:    err,
		}
		return s.companyError(ctx, failure)
	}

	companyAccess := companyAccess{
		ActorID:   cq.SenderID(),
		CompanyID: ref.CompanyID,
	}
	info, admin, err := s.companyFor(ctx, companyAccess)
	if err != nil {
		failure := errorReport{
			ChatID: cq.ChatID(),
			Err:    err,
		}
		return s.companyError(ctx, failure)
	}
	companyCard := companyCard{
		ChatID: cq.ChatID(),
		Info:   info,
		Admin:  admin,
	}
	return s.sendCompanyCard(ctx, companyCard)
}

// companyFor loads a company for someone who may see it: an admin (admin
// true), or its leader; anyone else gets service.ErrNotLeader.
func (s *router) companyFor(ctx context.Context, in companyAccess) (*service.CompanyInfo, bool, error) {
	u, err := s.users.User(ctx, in.ActorID)
	if err != nil {
		return nil, false, err
	}
	if u.Role == service.RoleAdmin {
		info, err := s.companies.Company(ctx, in.CompanyID)
		return info, true, err
	}
	info, err := s.companies.MyCompany(ctx, in.ActorID)
	if err != nil {
		return nil, false, err
	}
	if info.Company.ID != in.CompanyID {
		return nil, false, service.ErrNotLeader
	}
	return info, false, nil
}

// isLeader: the user leads a company (the menu shows «🏢 Моя компания»).
// A failed lookup only hides the button.
func (s *router) isLeader(ctx context.Context, u *service.User) bool {
	if u.Role != service.RoleMember {
		return false
	}
	_, err := s.companies.MyCompany(ctx, u.ID)
	if err != nil && !errors.Is(err, service.ErrNotLeader) {
		log.Printf("bot: company of %d: %v", u.ID, err)
	}
	return err == nil
}

// companyError explains a failed leader action; an unexpected error is also
// returned, for the log.
func (s *router) companyError(ctx context.Context, f errorReport) error {
	text, known := companyErrorText(f.Err)
	userError := userError{
		ChatID: f.ChatID,
		Err:    f.Err,
		Text:   text,
		Known:  known,
	}
	return s.replyError(ctx, userError)
}

// sendCompanyCard sends a company card as a new message.
func (s *router) sendCompanyCard(ctx context.Context, c companyCard) error {
	v := s.companyView(c)
	outMessage := outMessage{
		ChatID:   c.ChatID,
		Text:     companyText(v),
		Keyboard: companyKeyboard(v),
	}
	return s.send.Send(ctx, outMessage)
}

// companyView is what a company card shows, with its invite link.
func (s *router) companyView(c companyCard) companyView {
	link := s.inviteLink(c.Info.Company.Invite)
	v := companyView{
		Info:  c.Info,
		Link:  link,
		Admin: c.Admin,
	}
	return v
}

// inviteLink is t.me/<bot>?start=c_<invite>, or the /start command when
// the bot's username is unknown.
func (s *router) inviteLink(invite string) string {
	if s.botUsername == "" {
		return "/start " + companyStartPrefix + invite
	}
	return "https://t.me/" + s.botUsername + "?start=" + companyStartPrefix + invite
}

// companyRotate replaces the invite link (the leader or an admin) and
// shows the card with the new one.
func (s *router) companyRotate(ctx context.Context, cq *tgbot.CallbackQuery) error {
	companyID := strings.TrimPrefix(cq.Data, cbCompanyRotate)
	rotateInviteInput := service.RotateInviteInput{
		ActorID:   cq.SenderID(),
		CompanyID: companyID,
	}
	_, err := s.companies.RotateInvite(ctx, rotateInviteInput)
	if err != nil {
		failure := errorReport{
			ChatID: cq.ChatID(),
			Err:    err,
		}
		return s.companyError(ctx, failure)
	}

	companyAccess := companyAccess{
		ActorID:   cq.SenderID(),
		CompanyID: companyID,
	}
	info, admin, err := s.companyFor(ctx, companyAccess)
	if err != nil {
		failure := errorReport{
			ChatID: cq.ChatID(),
			Err:    err,
		}
		return s.companyError(ctx, failure)
	}
	companyCard := companyCard{
		ChatID: cq.ChatID(),
		Info:   info,
		Admin:  admin,
	}
	return s.sendCompanyCard(ctx, companyCard)
}

// adminCompany handles the admin's company buttons.
func (s *router) adminCompany(ctx context.Context, a adminAction) error {
	switch a.Name {
	case actCompanies:
		return s.adminCompanies(ctx, a)
	case actCompany:
		return s.adminCompanyCard(ctx, a)
	case actCompanyNew:
		return s.adminCompanyTerm(ctx, a)
	case actCompanyTerm:
		return s.adminCompanyAskName(ctx, a)
	case actCompanyExt:
		return s.adminCompanyExtend(ctx, a)
	}
	return nil
}

// adminCompanies is the list of companies, one button each.
func (s *router) adminCompanies(ctx context.Context, a adminAction) error {
	list, err := s.companies.Companies(ctx)
	if err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	editMessage := editMessage{
		ChatID:    a.ChatID,
		MessageID: a.MessageID,
		Text:      companiesText(list),
		Keyboard:  companiesKeyboard(list),
	}
	return s.send.Edit(ctx, editMessage)
}

// adminCompanyCard shows a company card in place of the pressed message.
func (s *router) adminCompanyCard(ctx context.Context, a adminAction) error {
	info, err := s.companies.Company(ctx, a.Arg)
	if err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	companyCard := companyCard{
		ChatID: a.ChatID,
		Info:   info,
		Admin:  true,
	}
	v := s.companyView(companyCard)
	editMessage := editMessage{
		ChatID:    a.ChatID,
		MessageID: a.MessageID,
		Text:      companyText(v),
		Keyboard:  companyKeyboard(v),
	}
	return s.send.Edit(ctx, editMessage)
}

// adminCompanyTerm: a new company led by the user of the card; which term
// is paid.
func (s *router) adminCompanyTerm(ctx context.Context, a adminAction) error {
	id, err := strconv.ParseInt(a.Arg, 10, 64)
	if err != nil {
		return fmt.Errorf("bot: bad user id in button: %w", err)
	}
	u, err := s.users.User(ctx, id)
	if err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	editMessage := editMessage{
		ChatID:    a.ChatID,
		MessageID: a.MessageID,
		Text:      companyTermText(u),
		Keyboard:  companyTermKeyboard(id),
	}
	return s.send.Edit(ctx, editMessage)
}

// adminCompanyAskName waits for the new company's name; the leader and the
// term wait in the prompt.
func (s *router) adminCompanyAskName(ctx context.Context, a adminAction) error {
	pendingInput := pendingInput{
		ChatID: a.ChatID,
		Kind:   pendingCompanyName,
		Text:   a.Arg,
	}
	s.dialogs.set(pendingInput)
	outMessage := outMessage{
		ChatID:   a.ChatID,
		Text:     askCompanyNameText,
		Keyboard: cancelKeyboard(),
	}
	return s.send.Send(ctx, outMessage)
}

// adminCompanyNamed creates the company once its name came, and sends its
// card with the invite link.
func (s *router) adminCompanyNamed(ctx context.Context, in companyNaming) error {
	s.dialogs.drop(in.ChatID) // before creating: a second text is not a second company
	leader, months, err := parseIDAndNumber(in.Arg)
	if err != nil {
		return fmt.Errorf("bot: bad company prompt %q: %w", in.Arg, err)
	}
	if !slices.Contains(companyTerms, months) {
		errorReport := errorReport{
			ChatID: in.ChatID,
			Err:    service.ErrBadCompany,
		}
		return s.reportError(ctx, errorReport)
	}
	createCompanyInput := service.CreateCompanyInput{
		LeaderID: leader,
		Name:     in.Name,
		Months:   months,
	}
	c, err := s.companies.CreateCompany(ctx, createCompanyInput)
	if err != nil {
		errorReport := errorReport{
			ChatID: in.ChatID,
			Err:    err,
		}
		return s.reportError(ctx, errorReport)
	}

	info, err := s.companies.Company(ctx, c.ID)
	if err != nil {
		errorReport := errorReport{
			ChatID: in.ChatID,
			Err:    err,
		}
		return s.reportError(ctx, errorReport)
	}
	companyCard := companyCard{
		ChatID: in.ChatID,
		Info:   info,
		Admin:  true,
	}
	return s.sendCompanyCard(ctx, companyCard)
}

// adminCompanyExtend adds paid months and shows the card again.
func (s *router) adminCompanyExtend(ctx context.Context, a adminAction) error {
	id, monthsText, _ := strings.Cut(a.Arg, ":")
	months, err := strconv.Atoi(monthsText)
	if err != nil || !slices.Contains(extendMonths, months) {
		return fmt.Errorf("bot: bad months in button %q", a.Arg)
	}
	extendCompanyInput := service.ExtendCompanyInput{
		CompanyID: id,
		Months:    months,
	}
	_, err = s.companies.ExtendCompany(ctx, extendCompanyInput)
	if err != nil {
		return s.reportError(ctx, a.failed(err))
	}

	card := a
	card.Arg = id
	return s.adminCompanyCard(ctx, card)
}

// parseMemberRef reads "<company id>:<user id>" of a remove button.
func parseMemberRef(arg string) (memberRef, error) {
	companyID, userText, _ := strings.Cut(arg, ":")
	userID, err := strconv.ParseInt(userText, 10, 64)
	if err != nil || companyID == "" {
		return memberRef{}, fmt.Errorf("bot: bad member button %q", arg)
	}
	ref := memberRef{
		CompanyID: companyID,
		UserID:    userID,
	}
	return ref, nil
}

// parseIDAndNumber reads "<user id>:<n>".
func parseIDAndNumber(arg string) (int64, int, error) {
	idText, nText, _ := strings.Cut(arg, ":")
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil {
		return 0, 0, err
	}
	n, err := strconv.Atoi(nText)
	if err != nil {
		return 0, 0, err
	}
	return id, n, nil
}

// inviteCodeOf reads the company code of "/start c_<code>".
func inviteCodeOf(text string) (string, bool) {
	f := strings.Fields(text)
	if len(f) < 2 {
		return "", false
	}
	code, ok := strings.CutPrefix(f[1], companyStartPrefix)
	return code, ok && code != ""
}
