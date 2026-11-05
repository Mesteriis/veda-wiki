package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// TokenFile is the bot token's file name in the data directory. It is never
// returned by any API.
const TokenFile = "telegram-bot.token"

var tokenPattern = regexp.MustCompile(`^[0-9]{5,16}:[A-Za-z0-9_-]{30,64}$`)

// ErrNoBot means no bot token is configured.
var ErrNoBot = errors.New("telegram bot not configured")

// Bot is an outbound-only Telegram Bot API client: it never receives updates.
type Bot struct {
	base string
	dir  string
	http *http.Client
}

// NewBot returns a client for api.telegram.org reading the token from dir.
func NewBot(dir string) *Bot {
	return &Bot{base: "https://api.telegram.org", dir: dir, http: &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// NewBotForTest returns a client for a local test server.
func NewBotForTest(base, dir string, hc *http.Client) *Bot {
	return &Bot{base: base, dir: dir, http: hc}
}

func (b *Bot) token() (string, error) {
	p := filepath.Join(b.dir, TokenFile)
	info, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNoBot
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return "", errors.New("telegram token must be a private regular file")
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(raw))
	if !tokenPattern.MatchString(token) {
		return "", errors.New("invalid telegram token file")
	}
	return token, nil
}

// SetToken checks token with getMe and stores it atomically; it returns the
// bot's username. An invalid token leaves the stored one unchanged.
func (b *Bot) SetToken(ctx context.Context, token string) (string, error) {
	token = strings.TrimSpace(token)
	if !tokenPattern.MatchString(token) {
		return "", errors.New("invalid token")
	}
	name, err := b.getMe(ctx, token)
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(b.dir, ".telegram-token-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if err = tmp.Chmod(0600); err == nil {
		if _, err = tmp.WriteString(token + "\n"); err == nil {
			err = tmp.Sync()
		}
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	return name, os.Rename(tmp.Name(), filepath.Join(b.dir, TokenFile))
}

// RemoveToken deletes the stored token.
func (b *Bot) RemoveToken() error {
	err := os.Remove(filepath.Join(b.dir, TokenFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Name returns the configured bot's username, or ErrNoBot.
func (b *Bot) Name(ctx context.Context) (string, error) {
	token, err := b.token()
	if err != nil {
		return "", err
	}
	return b.getMe(ctx, token)
}

func (b *Bot) getMe(ctx context.Context, token string) (string, error) {
	var me struct {
		Username string `json:"username"`
	}
	if err := b.call(ctx, token, "getMe", nil, &me); err != nil {
		return "", err
	}
	return me.Username, nil
}

// Send sends plain text (no parse mode, link previews off) to chat.
func (b *Bot) Send(ctx context.Context, chat int64, text string) error {
	token, err := b.token()
	if err != nil {
		return err
	}
	body := map[string]any{"chat_id": chat, "text": text, "link_preview_options": map[string]bool{"is_disabled": true}}
	return b.call(ctx, token, "sendMessage", body, nil)
}

// call never puts the token in an error: Telegram's URL carries it.
func (b *Bot) call(ctx context.Context, token, method string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", b.base+"/bot"+token+"/"+method, reader)
	if err != nil {
		return errors.New("telegram request failed")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.http.Do(req)
	if err != nil {
		return errors.New("telegram unavailable")
	}
	defer resp.Body.Close()
	var answer struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&answer) != nil || !answer.OK {
		return errors.New("telegram rejected the request")
	}
	if out != nil {
		return json.Unmarshal(answer.Result, out)
	}
	return nil
}
