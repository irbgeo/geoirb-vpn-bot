package bot

import (
	"context"
	"time"

	"strconv"

	tgbot "github.com/irbgeo/go-tgbot"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// OutMessage is one text message to send.
type OutMessage struct {
	ChatID   int64
	Text     string
	Keyboard *tgbot.InlineKeyboardMarkup // nil = no buttons
}

// EditMessage replaces the text and buttons of a sent message.
type EditMessage struct {
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
func (r paymentRef) String() string {
	return strconv.FormatInt(r.UserID, 10) + ":" + r.Ref
}

// failed turns an error of this action into a report for its chat.
func (a adminAction) failed(err error) errorReport {
	return errorReport{
		ChatID: a.ChatID,
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

// OutInvoice is a Telegram Stars (XTR) invoice.
type OutInvoice struct {
	ChatID      int64
	Title       string // up to 32 chars
	Description string // up to 255 chars
	Payload     string // comes back with the payment
	Label       string // price line, e.g. "1 месяц"
	Stars       int
}

// PreCheckoutAnswer answers a pre-checkout query (within 10 seconds).
type PreCheckoutAnswer struct {
	ID    string
	OK    bool
	Error string // shown to the user when OK is false
}

// RefundInput returns the Stars of one payment.
type RefundInput struct {
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

// pendingKind is what an admin's next text message will be.
type pendingKind int

const (
	pendingBroadcast pendingKind = iota + 1 // text of a broadcast
	readyBroadcast                          // text given, waiting for "send"
)

// pendingTTL: a prompt older than this is dropped, so a later text is not
// taken for an answer the admin forgot about.
const pendingTTL = 10 * time.Minute

// pendingInput is what the bot waits for from one admin.
type pendingInput struct {
	ChatID int64 // the admin's chat
	Kind   pendingKind
	Text   string    // readyBroadcast: the text to send
	At     time.Time // when the prompt was sent (pendingTTL)
}

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

// broadcastResult counts delivered and failed broadcast messages.
type broadcastResult struct {
	Sent   int
	Failed int
}

// OutFile is one file (document or photo) to send.
type OutFile struct {
	ChatID  int64
	Name    string
	Data    []byte
	Caption string
}

// Deps is everything New needs.
type Deps struct {
	Service        Service
	Sender         Sender
	Bypass         Bypass
	SupportContact string     // e.g. "@geoirb"
	BackupStamp    string     // file touched by each good backup; "" = no check
	Load           ServerLoad // nil = no server load alerts
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
