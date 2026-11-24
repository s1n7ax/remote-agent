// Package push delivers Web Push notifications signed with VAPID keys. Key
// and subscription storage is the caller's job; this package only turns
// strings in and out.
package push

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// ErrGone means the push service no longer knows the subscription (404 or
// 410); the caller should delete it.
var ErrGone = errors.New("push subscription gone")

// Keys is a VAPID key pair, base64url encoded as browsers expect.
type Keys struct {
	Public  string `json:"publicKey"`
	Private string `json:"privateKey"`
}

// GenerateKeys makes a new VAPID key pair.
func GenerateKeys() (Keys, error) {
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return Keys{}, fmt.Errorf("generate vapid keys: %w", err)
	}
	return Keys{Public: pub, Private: priv}, nil
}

// Payload is what the service worker shows.
type Payload struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	URL   string `json:"url,omitempty"` // opened on click
	Tag   string `json:"tag,omitempty"` // replaces an earlier notification with the same tag
}

// Sender sends notifications with one VAPID identity.
type Sender struct {
	Keys Keys
	// Subject is the VAPID contact, a mailto: or https: URL.
	Subject string
	// TTL is how long the push service keeps an undelivered message; zero
	// means one day.
	TTL time.Duration
	// HTTP is the client used; nil means a client with a 30s timeout.
	HTTP *http.Client
}

// Send delivers payload to the browser subscription given as the JSON a
// browser's PushSubscription.toJSON() produces. It returns an error wrapping
// ErrGone when the subscription expired or was revoked.
func (s *Sender) Send(ctx context.Context, subscription []byte, p Payload) error {
	var sub webpush.Subscription
	if err := json.Unmarshal(subscription, &sub); err != nil {
		return fmt.Errorf("push: decode subscription: %w", err)
	}
	if sub.Endpoint == "" || sub.Keys.Auth == "" || sub.Keys.P256dh == "" {
		return errors.New("push: subscription needs endpoint, keys.auth and keys.p256dh")
	}
	msg, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("push: encode payload: %w", err)
	}
	ttl := s.TTL
	if ttl == 0 {
		ttl = 24 * time.Hour
	}
	hc := s.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := webpush.SendNotificationWithContext(ctx, msg, &sub, &webpush.Options{
		HTTPClient:      hc,
		Subscriber:      strings.TrimPrefix(s.Subject, "mailto:"),
		TTL:             int(ttl / time.Second),
		Topic:           topic(p.Tag),
		Urgency:         webpush.UrgencyNormal,
		VAPIDPublicKey:  s.Keys.Public,
		VAPIDPrivateKey: s.Keys.Private,
	})
	if err != nil {
		return fmt.Errorf("push: send to %s: %w", host(sub.Endpoint), err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return fmt.Errorf("push: %s answered %d: %w", host(sub.Endpoint), resp.StatusCode, ErrGone)
	case resp.StatusCode/100 != 2:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("push: %s answered %d: %s", host(sub.Endpoint), resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// topic turns a tag into a valid Topic header (≤32 URL-safe base64 chars),
// or "" when the tag cannot be one.
func topic(tag string) string {
	if tag == "" || len(tag) > 32 {
		return ""
	}
	for _, r := range tag {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_'
		if !ok {
			return ""
		}
	}
	return tag
}

// host names a push endpoint in errors without its secret path.
func host(endpoint string) string {
	rest, ok := strings.CutPrefix(endpoint, "https://")
	if !ok {
		rest = strings.TrimPrefix(endpoint, "http://")
	}
	h, _, _ := strings.Cut(rest, "/")
	return h
}
