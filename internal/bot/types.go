package bot

import (
	"context"
	"strconv"
	"time"

	tgbot "github.com/irbgeo/go-tgbot"

	"github.com/irbgeo/geoirb-vpn-bot/internal/config"
	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// outMessage is one text message to send.
type outMessage struct {
	ChatID   int64
	Text     string
	Keyboard *tgbot.InlineKeyboardMarkup // nil = no buttons
}

// editMessage replaces the text and buttons of a sent message.
type editMessage struct {
	ChatID    int64
	MessageID int64
	Text      string
	Keyboard  *tgbot.InlineKeyboardMarkup
}

// adminAction is one pressed admin button: "a:<Name>:<Arg>".
type adminAction struct {
	ChatID    int64
	MessageID int64
	Name      string // users, user, dis, en, ext, cfg, del, delok
	Arg       string // page number, user ID or public key
}

// usersView is one page of the admin user list.
type usersView struct {
	Users []*service.User
	Total int64
	Page  int64 // from 0
}

// cardView is what the user card keyboard needs.
type cardView struct {
	UserID   int64
	Keys     []service.KeyInfo
	Payments []*service.Payment
}

// paymentRef names one payment in a button: Telegram charge IDs are too
// long for the 64-byte callback data, so a short hash is used instead.
type paymentRef struct {
	UserID int64
	Ref    string // payRef(chargeID)
}

// String is the "<user ID>:<ref>" part of refund buttons.
func (s paymentRef) String() string {
	return strconv.FormatInt(s.UserID, 10) + ":" + s.Ref
}

// failed turns an error of this action into a report for its chat.
func (s adminAction) failed(err error) errorReport {
	return errorReport{
		ChatID: s.ChatID,
		Err:    err,
	}
}

// peersGroup is a titled list of keys in a text.
type peersGroup struct {
	Title string
	Peers []*service.Peer
}

// errorReport is a failed admin action to show in a chat.
type errorReport struct {
	ChatID int64
	Err    error
}

// outInvoice is a Telegram Stars (XTR) invoice.
type outInvoice struct {
	ChatID      int64
	Title       string // up to 32 chars
	Description string // up to 255 chars
	Payload     string // comes back with the payment
	Label       string // price line, e.g. "1 месяц"
	Stars       int
}

// preCheckoutAnswer answers a pre-checkout query (within 10 seconds).
type preCheckoutAnswer struct {
	ID    string
	OK    bool
	Error string // shown to the user when OK is false
}

// refundInput returns the Stars of one payment.
type refundInput struct {
	UserID   int64
	ChargeID string
}

// accessView is what the "My access" keyboard needs.
type accessView struct {
	Keys   []service.KeyInfo
	CanBuy bool // RoleUser: show "extend" buttons
}

// tariffsView is what the tariff keyboard needs.
type tariffsView struct {
	Tariffs   []service.Tariff
	PublicKey string // key to extend; empty = the user's key
}

// failedPayment is a successful payment the bot could not apply.
type failedPayment struct {
	Message *tgbot.Message
	Cause   error
}

// refundAlert is a payment the bot could not apply, for the admins.
type refundAlert struct {
	ChargeID  string
	UserID    int64
	Cause     error // why it was not applied
	RefundErr error // nil = the Stars went back
}

// paymentReviewInput is a payment that was not applied because its record
// was left unfinished earlier.
type paymentReviewInput struct {
	ChargeID string
	UserID   int64
	Stars    int
}

// paymentAlert is a new payment to tell the admins about.
type paymentAlert struct {
	Payer       *tgbot.User
	Stars       int
	Result      *service.PayResult
	DeliveryErr error // the key/message did not reach the user
}

// noticeGroup is one kind of key notice for DeliverMaintenance.
type noticeGroup struct {
	Peers   []*service.Peer
	Text    func(*service.Peer) string
	NoOffer bool
}

// userError is an error to explain to the user (Text); Known means it is
// expected and needs no log.
type userError struct {
	ChatID int64
	Err    error
	Text   string
	Known  bool
}

// keyNotice is a message about one key to its owner.
type keyNotice struct {
	Peer    *service.Peer
	Text    string
	NoOffer bool // no "extend" button (the key never expires)
}

// pendingKind is what a chat's next input will be.
type pendingKind int

const (
	pendingBroadcast pendingKind = iota + 1 // admin: text of a broadcast
	readyBroadcast                          // admin: text given, waiting for "send"
	pendingKeyName                          // user: the name of the key to create
	pendingFeedback                         // user: a review or suggestion
)

// dialogTake asks dialogs.take for a chat's entry of one kind.
type dialogTake struct {
	ChatID int64
	Kind   pendingKind
}

// massSend is one background send to every user with an enabled key:
// Before runs once the slot is taken (e.g. flips maintenance), Started is
// told to the admin, Deliver runs per user and Report words the result.
type massSend struct {
	AdminChat int64
	Started   string
	Before    func() error
	Deliver   func(ctx context.Context, userID int64) error
	Report    func(broadcastResult) string
}

// pendingTTL: a prompt older than this is dropped, so a later text is not
// taken for an answer the admin forgot about.
const pendingTTL = 10 * time.Minute

// pendingInput is what the bot waits for in one chat.
type pendingInput struct {
	ChatID int64
	UserID int64 // pendingKeyName: only this user answers
	Kind   pendingKind
	Text   string    // readyBroadcast: the text to send
	At     time.Time // when the prompt was sent (pendingTTL)
	Maint  maintChange
}

// maintChange: what sending a ready broadcast does to the maintenance state.
type maintChange int

const (
	maintKeep  maintChange = iota // a plain broadcast
	maintStart                    // "maintenance started" text
	maintEnd                      // "maintenance is over" text
)

// broadcastView is a broadcast preview.
type broadcastView struct {
	Recipients int
	Text       string
}

// broadcastJob is confirmed work to do for many users in the background
// (a broadcast, fresh configs): Deliver runs for each recipient, Report
// words the result for the admin.
type broadcastJob struct {
	AdminChat  int64
	Recipients []int64
	Deliver    func(ctx context.Context, userID int64) error
	Report     func(broadcastResult) string
}

// broadcastResult counts delivered and failed broadcast messages, and the
// recipients a shutdown left untried.
type broadcastResult struct {
	Sent    int
	Failed  int
	Skipped int
}

// outFile is one file (document or photo) to send.
type outFile struct {
	ChatID  int64
	Name    string
	Data    []byte
	Caption string
}

// outVideo is a video to send: by FileID when Telegram already has it,
// otherwise Data is uploaded as Name.
type outVideo struct {
	ChatID  int64
	FileID  string
	Name    string
	Data    []byte
	Caption string
}

// splitVideoName is the file name users see for the uploaded video.
const splitVideoName = "split-tunnel.mp4"

// Deps is everything New needs.
type Deps struct {
	Users    Users
	Keys     Keys
	Billing  Billing
	Ops      Ops
	Feedback Feedback
	Sender   Sender
	// SplitVideo: the video on app split tunneling (data.SplitTunnel); empty = no such step.
	SplitVideo []byte
	Notifier   *notifier
	// Config gives SupportContact (e.g. "@geoirb") and MaintenanceFlag (a
	// file that exists while maintenance is on, so the state survives a
	// restart; "" = kept in memory only).
	Config *config.Config
}

// feedbackView is one page of reviews and suggestions for admins.
type feedbackView struct {
	List  []*service.Feedback
	Total int64
	Page  int64 // from 0
}

// navView is where a list page is: its buttons' prefix, page and total.
type navView struct {
	Prefix string // e.g. cbAdminUsers; the page number is appended
	Page   int64
	Total  int64
}

// menuScreen is the main menu: greeting and buttons.
type menuScreen struct {
	Text     string
	Keyboard *tgbot.InlineKeyboardMarkup
}

// menuView is what the main menu keyboard needs.
type menuView struct {
	Role        service.Role
	Maintenance bool // admins see "end maintenance" instead of "maintenance"
	SplitVideo  bool // there is a video on app split tunneling to offer
}

// command is a /command a user sent (or a button standing in for one).
type command struct {
	Name   string // without the slash
	UserID int64
}

// supportView is what the support text needs.
type supportView struct {
	Contact string
	UserID  int64
}

// keyRequest is a user's own key to create in a chat; Name "" = the
// generated name ("tg:<user> #N").
type keyRequest struct {
	ChatID int64
	UserID int64
	Name   string
}

// keyDelivery is a new key to send to a chat.
type keyDelivery struct {
	ChatID int64
	Peer   *service.Peer
}

// configDelivery is a key config to send to a chat.
type configDelivery struct {
	ChatID int64
	Key    *service.KeyConfig
}

// onlineSample is how many clients were online at a maintenance run.
type onlineSample struct {
	At     time.Time
	Online int
}

// onlineDrop is a fall of clients online: Online now, Peak in the last
// onlineDropWindow before it.
type onlineDrop struct {
	Online int
	Peak   int
}

// ownKeyFailedInput is a failed reissue or delete of a user's own key.
type ownKeyFailedInput struct {
	Query *tgbot.CallbackQuery
	Err   error
}
