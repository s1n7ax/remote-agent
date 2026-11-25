package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/s1n7ax/remote-agent/internal/domain"
	"github.com/s1n7ax/remote-agent/internal/push"
	"github.com/s1n7ax/remote-agent/internal/store"
)

// Notifier sends Web Push notifications to every subscribed browser, for the
// event kinds the user enabled. Keys, prefs and subscriptions live in the
// database.
type Notifier struct {
	st     *store.Store
	sender push.Sender
	log    *slog.Logger
}

// NewNotifier loads the VAPID keys, generating them on first start. subject
// is the VAPID contact URL; it never carries the user's email.
func NewNotifier(ctx context.Context, st *store.Store, subject string, log *slog.Logger) (*Notifier, error) {
	var keys push.Keys
	raw, ok, err := st.Setting(ctx, "vapid_keys")
	if err != nil {
		return nil, err
	}
	if ok {
		err = json.Unmarshal([]byte(raw), &keys)
	} else {
		keys, err = push.GenerateKeys()
		if err == nil {
			b, _ := json.Marshal(keys)
			err = st.SetSetting(ctx, "vapid_keys", string(b))
		}
	}
	if err != nil {
		return nil, fmt.Errorf("vapid keys: %w", err)
	}
	return &Notifier{st: st, sender: push.Sender{Keys: keys, Subject: subject}, log: log}, nil
}

// PublicKey implements api.Push.
func (n *Notifier) PublicKey() string { return n.sender.Keys.Public }

// Subscribe implements api.Push.
func (n *Notifier) Subscribe(ctx context.Context, sub json.RawMessage) error {
	var s struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.Unmarshal(sub, &s); err != nil || s.Endpoint == "" {
		return errors.New("subscription needs an endpoint")
	}
	return n.st.AddPushSubscription(ctx, s.Endpoint, string(sub))
}

// Prefs implements api.Push.
func (n *Notifier) Prefs(ctx context.Context) (map[string]bool, error) {
	out := map[string]bool{}
	for ev, def := range domain.NotifyEvents {
		on, err := n.enabled(ctx, ev, def)
		if err != nil {
			return nil, err
		}
		out[string(ev)] = on
	}
	return out, nil
}

func (n *Notifier) enabled(ctx context.Context, ev domain.NotifyEvent, def bool) (bool, error) {
	v, ok, err := n.st.Setting(ctx, "notify_"+string(ev))
	if err != nil || !ok {
		return def, err
	}
	return v == "true", nil
}

// SetPrefs implements api.Push; unknown kinds are ignored.
func (n *Notifier) SetPrefs(ctx context.Context, prefs map[string]bool) error {
	for k, on := range prefs {
		if _, known := domain.NotifyEvents[domain.NotifyEvent(k)]; !known {
			continue
		}
		if err := n.st.SetSetting(ctx, "notify_"+k, fmt.Sprint(on)); err != nil {
			return err
		}
	}
	return nil
}

// Test implements api.Push.
func (n *Notifier) Test(ctx context.Context) error {
	sent, err := n.send(ctx, push.Payload{Title: "remote-agent", Body: "Notifications work.", URL: "/", Tag: "test"})
	if err == nil && sent == 0 {
		err = errors.New("no browser is subscribed")
	}
	return err
}

// Notify implements orchestrator.Notifier.
func (n *Notifier) Notify(ctx context.Context, ev domain.NotifyEvent, title, body, url string) {
	if on, err := n.enabled(ctx, ev, domain.NotifyEvents[ev]); err != nil || !on {
		return
	}
	if _, err := n.send(ctx, push.Payload{Title: title, Body: body, URL: url, Tag: string(ev) + url}); err != nil {
		n.log.Warn("push failed", "event", ev, "err", err)
	}
}

// send delivers p to every subscription, dropping the ones that are gone.
func (n *Notifier) send(ctx context.Context, p push.Payload) (int, error) {
	subs, err := n.st.PushSubscriptions(ctx)
	if err != nil {
		return 0, err
	}
	var errs []error
	sent := 0
	for endpoint, sub := range subs {
		err := n.sender.Send(ctx, []byte(sub), p)
		switch {
		case errors.Is(err, push.ErrGone):
			_ = n.st.DeletePushSubscription(ctx, endpoint)
		case err != nil:
			errs = append(errs, err)
		default:
			sent++
		}
	}
	return sent, errors.Join(errs...)
}
