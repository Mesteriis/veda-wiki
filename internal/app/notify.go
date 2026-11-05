package app

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/notify"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

// notifyInterval is how often connection errors and key expiry are checked.
const notifyInterval = time.Minute

// startNotify opens <data>/notify.sqlite and starts the notification worker.
// Notifications read VEDA's state only; nothing here writes to it.
func startNotify(ctx context.Context, dir string, st *store.Store, workers *sync.WaitGroup) (*notify.Service, error) {
	db, err := notify.Open(dir)
	if err != nil {
		return nil, err
	}
	service := &notify.Service{
		Store:  db,
		Bot:    notify.NewBot(dir),
		Admins: func() []string { return admins(st) },
		State:  func() notify.Snapshot { return snapshot(st) },
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		defer db.Close()
		service.Run(ctx, notifyInterval)
	}()
	return service, nil
}

func admins(st *store.Store) []string {
	var out []string
	_ = st.View(func(db *domain.Database) error {
		for id, user := range db.Users {
			if user.Admin && !user.Disabled {
				out = append(out, id)
			}
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func snapshot(st *store.Store) notify.Snapshot {
	var s notify.Snapshot
	_ = st.View(func(db *domain.Database) error {
		for id, c := range db.Connections {
			s.Sync = append(s.Sync, notify.ConnectionState{ID: id, Repository: c.Repository, State: c.State, LastError: c.LastError})
		}
		for id, k := range db.AccessKeys {
			if expires, err := time.Parse(time.RFC3339Nano, k.Expires); err == nil {
				s.Keys = append(s.Keys, notify.KeyState{ID: id, Expires: expires})
			}
		}
		return nil
	})
	return s
}
