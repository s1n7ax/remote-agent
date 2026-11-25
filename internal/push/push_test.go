package push

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// browser is a fake user agent's push subscription keys.
type browser struct {
	priv *ecdh.PrivateKey
	auth []byte
}

func newBrowser(t *testing.T) browser {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	return browser{priv: priv, auth: auth}
}

func (b browser) subscription(endpoint string) []byte {
	enc := base64.RawURLEncoding.EncodeToString
	raw, _ := json.Marshal(map[string]any{
		"endpoint": endpoint,
		"keys":     map[string]string{"p256dh": enc(b.priv.PublicKey().Bytes()), "auth": enc(b.auth)},
	})
	return raw
}

// decrypt undoes RFC 8291 aes128gcm encryption as a browser would.
func (b browser) decrypt(t *testing.T, body []byte) []byte {
	t.Helper()
	salt, idlen := body[:16], int(body[20])
	asPub, ct := body[21:21+idlen], body[21+idlen:]
	pub, err := ecdh.P256().NewPublicKey(asPub)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := b.priv.ECDH(pub)
	if err != nil {
		t.Fatal(err)
	}
	info := append(append([]byte("WebPush: info\x00"), b.priv.PublicKey().Bytes()...), asPub...)
	ikm, _ := hkdf.Key(sha256.New, secret, b.auth, string(info), 32)
	cek, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	plain = bytes.TrimRight(plain, "\x00")
	if len(plain) == 0 || plain[len(plain)-1] != 0x02 {
		t.Fatalf("bad padding delimiter")
	}
	return plain[:len(plain)-1]
}

func TestGenerateKeys(t *testing.T) {
	k, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	pub, err := base64.RawURLEncoding.DecodeString(k.Public)
	if err != nil || len(pub) != 65 || pub[0] != 4 {
		t.Fatalf("public key %q is not a raw uncompressed P-256 point (%v)", k.Public, err)
	}
	if k.Private == "" || k.Private == k.Public {
		t.Fatal("bad private key")
	}
	k2, _ := GenerateKeys()
	if k2 == k {
		t.Fatal("keys repeat")
	}
}

func TestSend(t *testing.T) {
	keys, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	b := newBrowser(t)
	var got Payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/push/secret-id" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		if ce := r.Header.Get("Content-Encoding"); ce != "aes128gcm" {
			t.Errorf("Content-Encoding = %q", ce)
		}
		if ttl := r.Header.Get("TTL"); ttl != "86400" {
			t.Errorf("TTL = %q", ttl)
		}
		if topic := r.Header.Get("Topic"); topic != "ticket-7" {
			t.Errorf("Topic = %q", topic)
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "vapid t=") || !strings.HasSuffix(auth, ", k="+keys.Public) {
			t.Errorf("Authorization = %q", auth)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b.decrypt(t, body), &got); err != nil {
			t.Errorf("payload: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	s := &Sender{Keys: keys, Subject: "mailto:me@example.com", HTTP: srv.Client()}
	want := Payload{Title: "Questions waiting", Body: "Ticket 7", URL: "/tickets/7", Tag: "ticket-7"}
	if err := s.Send(context.Background(), b.subscription(srv.URL+"/push/secret-id"), want); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("payload = %+v, want %+v", got, want)
	}
}

func TestSendErrors(t *testing.T) {
	keys, _ := GenerateKeys()
	b := newBrowser(t)
	for _, tt := range []struct {
		status int
		gone   bool
	}{{http.StatusGone, true}, {http.StatusNotFound, true}, {http.StatusTooManyRequests, false}, {http.StatusInternalServerError, false}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tt.status)
		}))
		s := &Sender{Keys: keys, Subject: "https://example.com", HTTP: srv.Client()}
		err := s.Send(context.Background(), b.subscription(srv.URL+"/p/secret"), Payload{Title: "x"})
		srv.Close()
		if err == nil {
			t.Errorf("status %d: nil error", tt.status)
			continue
		}
		if errors.Is(err, ErrGone) != tt.gone {
			t.Errorf("status %d: errors.Is(ErrGone) = %v, want %v", tt.status, !tt.gone, tt.gone)
		}
		if strings.Contains(err.Error(), "secret") {
			t.Errorf("error leaks endpoint path: %v", err)
		}
	}

	s := &Sender{Keys: keys, Subject: "mailto:x@y"}
	for _, sub := range []string{"not json", `{"endpoint":"https://x"}`, `{"keys":{"auth":"a","p256dh":"b"}}`} {
		if err := s.Send(context.Background(), []byte(sub), Payload{Title: "x"}); err == nil {
			t.Errorf("subscription %s accepted", sub)
		}
	}
}

func TestTopic(t *testing.T) {
	for tag, want := range map[string]string{
		"":                      "",
		"ticket-7":              "ticket-7",
		"has space":             "",
		"a/b":                   "",
		strings.Repeat("a", 33): "",
		strings.Repeat("a", 32): strings.Repeat("a", 32),
	} {
		if got := topic(tag); got != want {
			t.Errorf("topic(%q) = %q, want %q", tag, got, want)
		}
	}
}
