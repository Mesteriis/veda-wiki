package notify

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// Event kinds the owner can switch on or off; all are on by default.
const (
	EventRequests = "requests"
	EventSync     = "sync"
	EventKeys     = "keys"
)

// Events lists the event kinds in display order.
var Events = []string{EventRequests, EventSync, EventKeys}

// ConnectionState is the part of a GitHub connection the watcher compares.
type ConnectionState struct {
	ID, Repository, State, LastError string
}

// KeyState is an access key's identity and expiry.
type KeyState struct {
	ID      string
	Expires time.Time
}

// Snapshot is what the watcher reads from VEDA's state on each pass.
type Snapshot struct {
	Sync []ConnectionState
	Keys []KeyState
}

// Service sends notifications to the administrators who set a Telegram ID.
type Service struct {
	Store *Store
	Bot   *Bot
	// Admins returns the IDs of enabled administrators.
	Admins func() []string
	// State returns the current connections and keys.
	State func() Snapshot
	// Limiter bounds the public request form.
	Limiter Limiter

	once  sync.Once
	queue chan string
	wg    sync.WaitGroup
}

func (s *Service) start() { s.once.Do(func() { s.queue = make(chan string, 64) }) }

// Enabled reports whether an event kind is switched on.
func (s *Service) Enabled(ctx context.Context, kind string) bool {
	v, err := s.Store.Setting(ctx, "event."+kind)
	return err == nil && v != "off"
}

// SetEnabled switches an event kind on or off.
func (s *Service) SetEnabled(ctx context.Context, kind string, on bool) error {
	for _, known := range Events {
		if known == kind {
			value := "on"
			if !on {
				value = "off"
			}
			return s.Store.SetSetting(ctx, "event."+kind, value)
		}
	}
	return errors.New("unknown event")
}

// Enqueue queues text for every recipient without waiting; a full queue
// drops the message rather than blocking a request.
func (s *Service) Enqueue(text string) {
	s.start()
	select {
	case s.queue <- text:
	default:
		log.Print("notification queue full; message dropped")
	}
}

// RequestCreated notifies about a new access request.
func (s *Service) RequestCreated(ctx context.Context, r Request, titles []string) {
	if !s.Enabled(ctx, EventRequests) {
		return
	}
	text := fmt.Sprintf("VEDA: запрос доступа к API/MCP\nTelegram: %s\nРазделы: %s", r.Telegram, strings.Join(titles, ", "))
	if r.Comment != "" {
		// Every comment line is quoted so it cannot pass for a line of ours.
		text += "\nКомментарий посетителя:\n│ " + strings.ReplaceAll(r.Comment, "\n", "\n│ ")
	}
	s.Enqueue(text)
}

// FormLimited tells the owner, at most once a day, that the public request
// form is refusing visitors because a limit is reached.
func (s *Service) FormLimited(ctx context.Context, now time.Time) {
	if !s.Enabled(ctx, EventRequests) {
		return
	}
	if fresh, err := s.Store.Mark(ctx, "form-limit", now.UTC().Format("2006-01-02"), now); err == nil && fresh {
		s.Enqueue("VEDA: форма запросов доступа упёрлась в лимит и отклоняет посетителей. Разберите ожидающие запросы в «Настройках».")
	}
}

// Run sends queued messages and checks the state every interval until ctx
// ends. Call it once.
func (s *Service) Run(ctx context.Context, interval time.Duration) {
	s.start()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case text := <-s.queue:
				s.deliver(ctx, text)
			}
		}
	}()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		s.Check(ctx, time.Now())
		select {
		case <-ctx.Done():
			s.wg.Wait()
			return
		case <-ticker.C:
		}
	}
}

// Check compares the state with what was already reported and queues the
// differences: a connection entering or leaving an error, a key expiring
// within a day.
func (s *Service) Check(ctx context.Context, now time.Time) {
	if s.State == nil {
		return
	}
	snapshot := s.State()
	// Marks follow the state even while the event is off, so switching it
	// on later neither repeats an old error nor misses the next one.
	syncOn := s.Enabled(ctx, EventSync)
	for _, c := range snapshot.Sync {
		if c.State == "error" {
			if fresh, err := s.Store.Mark(ctx, EventSync, c.ID, now); err == nil && fresh && syncOn {
				s.Enqueue(fmt.Sprintf("VEDA: ошибка синхронизации\nИсточник: %s (%s)\nКод: %s", c.ID, c.Repository, c.LastError))
			}
		} else if recovered, err := s.Store.Unmark(ctx, EventSync, c.ID); err == nil && recovered && syncOn {
			s.Enqueue(fmt.Sprintf("VEDA: синхронизация восстановлена\nИсточник: %s (%s)", c.ID, c.Repository))
		}
	}
	if s.Enabled(ctx, EventKeys) {
		for _, k := range snapshot.Keys {
			if k.Expires.After(now) && k.Expires.Sub(now) <= 24*time.Hour {
				subject := k.ID + "@" + k.Expires.UTC().Format(time.RFC3339)
				if fresh, err := s.Store.Mark(ctx, EventKeys, subject, now); err == nil && fresh {
					s.Enqueue(fmt.Sprintf("VEDA: ключ доступа %s истекает %s UTC", k.ID, k.Expires.UTC().Format("02.01.2006 15:04")))
				}
			}
		}
	}
}

// Test sends a test message now and returns the first failure.
func (s *Service) Test(ctx context.Context) (int, error) {
	return s.send(ctx, "VEDA: пробное уведомление. Если вы его видите, уведомления настроены.")
}

func (s *Service) deliver(ctx context.Context, text string) {
	for attempt := 0; attempt < 3; attempt++ {
		if _, err := s.send(ctx, text); err == nil || errors.Is(err, ErrNoBot) || errors.Is(err, errNoRecipients) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(attempt+1) * 5 * time.Second):
		}
	}
	log.Print("notification not delivered after retries")
}

var errNoRecipients = errors.New("no administrator has a Telegram ID")

// send delivers text to every administrator with a Telegram ID; it returns
// how many received it and the last failure.
func (s *Service) send(ctx context.Context, text string) (int, error) {
	ids, err := s.Store.TelegramIDs(ctx)
	if err != nil {
		return 0, err
	}
	var chats []int64
	if s.Admins != nil {
		for _, admin := range s.Admins() {
			if chat := ids[admin]; chat != 0 {
				chats = append(chats, chat)
			}
		}
	}
	if len(chats) == 0 {
		return 0, errNoRecipients
	}
	delivered := 0
	var last error
	for _, chat := range chats {
		if err := s.Bot.Send(ctx, chat, text); err != nil {
			last = err
			continue
		}
		delivered++
	}
	if delivered == 0 {
		return 0, last
	}
	return delivered, nil
}
