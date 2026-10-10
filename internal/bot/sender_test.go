package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	tgbot "github.com/irbgeo/go-tgbot"
	"github.com/stretchr/testify/require"

	"github.com/irbgeo/geoirb-vpn-bot/internal/config"
	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// apiCall is one request the fake Bot API got: Fields are the JSON body, or
// the form fields of an upload (a file is under its field name).
type apiCall struct {
	Method string
	Fields map[string]any
}

// telegramAPI is a fake Bot API. It records every call and answers "ok",
// or the whole response body that replies holds for the method.
type telegramAPI struct {
	mu      sync.Mutex
	calls   []apiCall
	replies map[string]string
}

func (s *telegramAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c := apiCall{
		Method: path.Base(r.URL.Path),
		Fields: map[string]any{},
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		_ = r.ParseMultipartForm(1 << 20)
		for k, v := range r.MultipartForm.Value {
			c.Fields[k] = v[0]
		}
		for k, v := range r.MultipartForm.File {
			f, _ := v[0].Open()
			data, _ := io.ReadAll(f)
			c.Fields[k] = v[0].Filename + ":" + string(data)
		}
	} else {
		_ = json.NewDecoder(r.Body).Decode(&c.Fields)
	}
	s.mu.Lock()
	s.calls = append(s.calls, c)
	reply, ok := s.replies[c.Method]
	s.mu.Unlock()
	if !ok {
		reply = `{"ok":true,"result":{"message_id":1}}`
		if !strings.HasPrefix(c.Method, "send") && !strings.HasPrefix(c.Method, "edit") {
			reply = `{"ok":true,"result":true}`
		}
	}
	_, _ = io.WriteString(w, reply)
}

// methods lists the called methods in order.
func (s *telegramAPI) methods() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.calls))
	for _, c := range s.calls {
		out = append(out, c.Method)
	}
	return out
}

// newTelegramSender is the real sender talking to a fake Bot API.
func newTelegramSender(t *testing.T) (*telegramSender, *telegramAPI) {
	api := &telegramAPI{
		replies: map[string]string{},
	}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	client, err := tgbot.NewClient("123:abc", tgbot.WithBaseURL(srv.URL))
	require.NoError(t, err)
	uploads, err := tgbot.NewClient("123:abc", tgbot.WithBaseURL(srv.URL))
	require.NoError(t, err)
	return NewTelegramSender(client, uploads), api
}

func TestNewTelegramClient(t *testing.T) {
	_, err := NewTelegramClient(&config.Config{})
	require.ErrorContains(t, err, "token is required")

	client, err := NewTelegramClient(
		&config.Config{
			BotToken:        "123:abc",
			TelegramTestEnv: true,
		},
	)
	require.NoError(t, err)
	require.NotNil(t, client)
}

// thirtyKeys is "My access" of a user with 30 keys: more than one Telegram
// message holds.
func thirtyKeys(t *testing.T) string {
	keys := make([]service.KeyInfo, 0, 30)
	for i := range 30 {
		keys = append(
			keys,
			service.KeyInfo{
				Peer: &service.Peer{
					Name: fmt.Sprintf("tg:administrator #%d", i+1),
					IP:   fmt.Sprintf("10.8.1.%d", i+1),
				},
			},
		)
	}
	text := accessText(keys)
	require.Greater(t, utf8.RuneCountInString(text), tgbot.MaxMessageLength)
	return text
}

func TestSendSplitsATextTooLongForOneMessage(t *testing.T) {
	s, api := newTelegramSender(t)
	text := thirtyKeys(t)

	err := s.Send(
		context.Background(),
		outMessage{
			ChatID:   42,
			Text:     text,
			Keyboard: menuKeyboard(),
		},
	)
	require.NoError(t, err)

	require.Equal(t, []string{"sendMessage", "sendMessage"}, api.methods())
	first, last := api.calls[0].Fields, api.calls[1].Fields
	require.LessOrEqual(t, utf8.RuneCountInString(first["text"].(string)), tgbot.MaxMessageLength)
	require.Equal(t, text, first["text"].(string)+last["text"].(string), "nothing is lost")
	require.NotContains(t, first, "reply_markup")
	require.Contains(t, last, "reply_markup", "the buttons go under the last part")
}

func TestSendShortTextIsOneMessage(t *testing.T) {
	s, api := newTelegramSender(t)

	err := s.Send(
		context.Background(),
		outMessage{
			ChatID: 42,
			Text:   "hi",
		},
	)
	require.NoError(t, err)
	require.Len(t, api.calls, 1)
	require.Equal(t, "hi", api.calls[0].Fields["text"])
	require.NotContains(t, api.calls[0].Fields, "reply_markup")
}

func TestEditPutsTheRestOfALongTextIntoNewMessages(t *testing.T) {
	s, api := newTelegramSender(t)
	text := thirtyKeys(t)

	err := s.Edit(
		context.Background(),
		editMessage{
			ChatID:    42,
			MessageID: 7,
			Text:      text,
			Keyboard:  menuKeyboard(),
		},
	)
	require.NoError(t, err)

	require.Equal(t, []string{"editMessageText", "sendMessage"}, api.methods())
	edit, rest := api.calls[0].Fields, api.calls[1].Fields
	require.InDelta(t, 7, edit["message_id"], 0)
	require.Equal(t, text, edit["text"].(string)+rest["text"].(string))
	require.Equal(
		t,
		map[string]any{
			"inline_keyboard": []any{},
		},
		edit["reply_markup"],
		"the old buttons go, the new ones are under the last part",
	)
	require.Contains(t, rest, "reply_markup")
}

func TestSendVideoUploadsThenSendsByFileID(t *testing.T) {
	s, api := newTelegramSender(t)
	api.replies["sendVideo"] = `{"ok":true,"result":{"message_id":1,"video":{"file_id":"VID9"}}}`
	ctx := context.Background()

	id, err := s.SendVideo(
		ctx,
		&outVideo{
			ChatID:  42,
			Name:    splitVideoName,
			Data:    []byte("mp4"),
			Caption: "how to",
		},
	)
	require.NoError(t, err)
	require.Equal(t, "VID9", id)
	require.Equal(t, splitVideoName+":mp4", api.calls[0].Fields["video"], "the file itself")
	require.Equal(t, "how to", api.calls[0].Fields["caption"])

	_, err = s.SendVideo(
		ctx,
		&outVideo{
			ChatID: 42,
			FileID: id,
		},
	)
	require.NoError(t, err)
	require.Equal(t, "VID9", api.calls[1].Fields["video"], "by ID: no second upload")
}

// A video without sound may come back as an animation: no "video" in the
// reply, the file is in "document".
func TestSendVideoFindsTheFileIDOfAnAnimation(t *testing.T) {
	s, api := newTelegramSender(t)
	api.replies["sendVideo"] = `{"ok":true,"result":{"message_id":1,"animation":{"file_id":"ANIM"},"document":{"file_id":"ANIM"}}}`

	id, err := s.SendVideo(
		context.Background(),
		&outVideo{
			ChatID: 42,
			Name:   splitVideoName,
			Data:   []byte("mp4"),
		},
	)
	require.NoError(t, err)
	require.Equal(t, "ANIM", id)
}

func TestNewUploadClient(t *testing.T) {
	_, err := NewUploadClient(&config.Config{})
	require.ErrorContains(t, err, "token is required")

	client, err := NewUploadClient(
		&config.Config{
			BotToken: "123:abc",
		},
	)
	require.NoError(t, err)
	require.NotNil(t, client)
}
