//go:build !integration

package acmeflow

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type testJWSEnvelope struct {
	Protected string `json:"protected"`
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

func TestProfileOrderIncludesAdvertisedProfileAndValidJWS(t *testing.T) {
	accountKey := mustTestRSAKey(t)
	const accountURI = "https://ca.invalid/acct/42"
	var orderRequests atomic.Int32

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/directory":
			w.Header().Set("Replay-Nonce", "directory-nonce")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"newNonce": server.URL + "/nonce",
				"newOrder": server.URL + "/new-order",
				"meta": map[string]any{"profiles": map[string]string{
					"shortlived": "short-lived certificate",
				}},
			})
		case "/new-order":
			orderRequests.Add(1)
			if got := r.Header.Get("Content-Type"); got != "application/jose+json" {
				t.Errorf("Content-Type = %q, want application/jose+json", got)
			}
			var envelope testJWSEnvelope
			if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
				t.Errorf("decode JWS envelope: %v", err)
				return
			}
			protectedJSON := decodeBase64URL(t, envelope.Protected)
			var protected map[string]any
			if err := json.Unmarshal(protectedJSON, &protected); err != nil {
				t.Errorf("decode protected header: %v", err)
				return
			}
			if protected["alg"] != "RS256" || protected["kid"] != accountURI || protected["nonce"] != "directory-nonce" || protected["url"] != server.URL+"/new-order" {
				t.Errorf("protected header = %#v", protected)
			}
			verifyRSAJWS(t, &accountKey.PublicKey, envelope)

			payloadJSON := decodeBase64URL(t, envelope.Payload)
			var payload struct {
				Identifiers []map[string]string `json:"identifiers"`
				Profile     string              `json:"profile"`
			}
			if err := json.Unmarshal(payloadJSON, &payload); err != nil {
				t.Errorf("decode payload: %v", err)
				return
			}
			if payload.Profile != "shortlived" {
				t.Errorf("profile = %q, want shortlived", payload.Profile)
			}
			if len(payload.Identifiers) != 1 || payload.Identifiers[0]["type"] != "ip" || payload.Identifiers[0]["value"] != "192.0.2.10" {
				t.Errorf("identifiers = %#v", payload.Identifiers)
			}

			w.Header().Set("Location", server.URL+"/order/1")
			w.Header().Set("Replay-Nonce", "next-nonce")
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"status":"pending","profile":"shortlived","identifiers":[{"type":"ip","value":"192.0.2.10"}],"authorizations":[%q],"finalize":%q}`,
				server.URL+"/authz/1", server.URL+"/order/1/finalize")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	starter := ProfileOrderStarter{}
	order, err := starter.StartOrder(context.Background(), OrderStartRequest{
		DirectoryURL: server.URL + "/directory",
		AccountURI:   accountURI,
		AccountKey:   accountKey,
		HTTPClient:   server.Client(),
		Identifiers:  []Identifier{{Type: IdentifierIP, Value: "192.0.2.10"}},
		Profile:      "shortlived",
	})
	if err != nil {
		t.Fatalf("StartOrder() error = %v", err)
	}
	if order.URI != server.URL+"/order/1" || order.FinalizeURL != server.URL+"/order/1/finalize" {
		t.Fatalf("order = %#v", order)
	}
	if orderRequests.Load() != 1 {
		t.Fatalf("new-order requests = %d, want 1", orderRequests.Load())
	}
}

func TestProfileOrderRejectsUnadvertisedProfileWithoutFallback(t *testing.T) {
	key := mustTestRSAKey(t)
	var orderRequests atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/directory" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"newNonce": server.URL + "/nonce",
				"newOrder": server.URL + "/new-order",
				"meta":     map[string]any{"profiles": map[string]string{"default": "default"}},
			})
			return
		}
		orderRequests.Add(1)
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()

	_, err := (ProfileOrderStarter{}).StartOrder(context.Background(), OrderStartRequest{
		DirectoryURL: server.URL + "/directory",
		AccountURI:   server.URL + "/account/1",
		AccountKey:   key,
		HTTPClient:   server.Client(),
		Identifiers:  []Identifier{{Type: IdentifierIP, Value: "192.0.2.11"}},
		Profile:      "shortlived",
	})
	if got := ErrorCategoryOf(err); got != CategoryProfile {
		t.Fatalf("error category = %q, want %q (err=%v)", got, CategoryProfile, err)
	}
	if orderRequests.Load() != 0 {
		t.Fatalf("unadvertised profile posted %d orders, want 0", orderRequests.Load())
	}
}

func TestProfileOrderRetriesBadNonce(t *testing.T) {
	key := mustTestRSAKey(t)
	var orderRequests atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/directory":
			w.Header().Set("Replay-Nonce", "first-nonce")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"newNonce": server.URL + "/nonce",
				"newOrder": server.URL + "/new-order",
				"meta":     map[string]any{"profiles": map[string]string{"shortlived": "short"}},
			})
		case "/new-order":
			n := orderRequests.Add(1)
			var envelope testJWSEnvelope
			_ = json.NewDecoder(r.Body).Decode(&envelope)
			var protected map[string]any
			_ = json.Unmarshal(decodeBase64URL(t, envelope.Protected), &protected)
			if n == 1 {
				if protected["nonce"] != "first-nonce" {
					t.Errorf("first nonce = %v", protected["nonce"])
				}
				w.Header().Set("Replay-Nonce", "replacement-nonce")
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"type":"urn:ietf:params:acme:error:badNonce","detail":"token-canary"}`))
				return
			}
			if protected["nonce"] != "replacement-nonce" {
				t.Errorf("retry nonce = %v, want replacement-nonce", protected["nonce"])
			}
			w.Header().Set("Location", server.URL+"/order/1")
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"status":"pending","profile":"shortlived","identifiers":[{"type":"ip","value":"192.0.2.12"}],"authorizations":[%q],"finalize":%q}`,
				server.URL+"/authz/1", server.URL+"/order/1/finalize")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	_, err := (ProfileOrderStarter{MaxBadNonceRetries: 2}).StartOrder(context.Background(), OrderStartRequest{
		DirectoryURL: server.URL + "/directory",
		AccountURI:   server.URL + "/account/1",
		AccountKey:   key,
		HTTPClient:   server.Client(),
		Identifiers:  []Identifier{{Type: IdentifierIP, Value: "192.0.2.12"}},
		Profile:      "shortlived",
	})
	if err != nil {
		t.Fatalf("StartOrder() error = %v", err)
	}
	if orderRequests.Load() != 2 {
		t.Fatalf("new-order requests = %d, want 2", orderRequests.Load())
	}
}

func TestProfileOrderBadNonceExhaustionIsSafe(t *testing.T) {
	key := mustTestRSAKey(t)
	const canary = "authorization-bearer-token-canary"
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/directory":
			w.Header().Set("Replay-Nonce", "nonce-1")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"newNonce": server.URL + "/nonce",
				"newOrder": server.URL + "/new-order",
				"meta":     map[string]any{"profiles": map[string]string{"shortlived": "short"}},
			})
		case "/new-order":
			w.Header().Set("Replay-Nonce", "nonce-next")
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, `{"type":"urn:ietf:params:acme:error:badNonce","detail":%q}`, canary)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	_, err := (ProfileOrderStarter{MaxBadNonceRetries: 1}).StartOrder(context.Background(), OrderStartRequest{
		DirectoryURL: server.URL + "/directory",
		AccountURI:   server.URL + "/account/1",
		AccountKey:   key,
		HTTPClient:   server.Client(),
		Identifiers:  []Identifier{{Type: IdentifierIP, Value: "192.0.2.13"}},
		Profile:      "shortlived",
	})
	if got := ErrorCategoryOf(err); got != CategoryBadNonce {
		t.Fatalf("error category = %q, want %q (err=%v)", got, CategoryBadNonce, err)
	}
	if strings.Contains(err.Error(), canary) {
		t.Fatalf("error leaked problem detail: %q", err)
	}
}

func TestProfileOrderPreservesRetryAfterFromHTTP429(t *testing.T) {
	key := mustTestRSAKey(t)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/directory":
			w.Header().Set("Replay-Nonce", "nonce")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"newNonce": server.URL + "/nonce",
				"newOrder": server.URL + "/new-order",
				"meta":     map[string]any{"profiles": map[string]string{"shortlived": "short"}},
			})
		case "/new-order":
			w.Header().Set("Retry-After", "90")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"detail":"Authorization: Bearer token-canary"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	_, err := (ProfileOrderStarter{MaxBadNonceRetries: -1}).StartOrder(context.Background(), OrderStartRequest{
		DirectoryURL: server.URL + "/directory",
		AccountURI:   server.URL + "/account/1",
		AccountKey:   key,
		HTTPClient:   server.Client(),
		Identifiers:  []Identifier{{Type: IdentifierIP, Value: "192.0.2.14"}},
		Profile:      "shortlived",
	})
	var safe *SafeError
	if !errors.As(err, &safe) {
		t.Fatalf("error type = %T, want *SafeError", err)
	}
	if safe.Category != CategoryRateLimited || safe.RetryAfter != 90*time.Second {
		t.Fatalf("safe error = %#v", safe)
	}
	if strings.Contains(err.Error(), "token-canary") {
		t.Fatalf("error leaked response body: %q", err)
	}
}

func TestProfileOrderECDSAJWSUsesRawSignature(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := encodeProfileJWS(key, "https://ca.invalid/acct/1", "nonce", "https://ca.invalid/new-order", profileOrderPayload{
		Identifiers: []profileIdentifier{{Type: "dns", Value: "example.com"}},
		Profile:     "shortlived",
	})
	if err != nil {
		t.Fatalf("encodeProfileJWS() error = %v", err)
	}
	var protected map[string]any
	if err := json.Unmarshal(decodeBase64URL(t, envelope.Protected), &protected); err != nil {
		t.Fatal(err)
	}
	if protected["alg"] != "ES256" {
		t.Fatalf("alg = %v, want ES256", protected["alg"])
	}
	signature := decodeBase64URL(t, envelope.Signature)
	if len(signature) != 64 {
		t.Fatalf("signature length = %d, want 64-byte JOSE R||S", len(signature))
	}
	digest := sha256.Sum256([]byte(envelope.Protected + "." + envelope.Payload))
	r := new(big.Int).SetBytes(signature[:32])
	s := new(big.Int).SetBytes(signature[32:])
	if !ecdsa.Verify(&key.PublicKey, digest[:], r, s) {
		t.Fatal("ECDSA JWS signature verification failed")
	}
}

func decodeBase64URL(t *testing.T, encoded string) []byte {
	t.Helper()
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode base64url: %v", err)
	}
	return decoded
}

func verifyRSAJWS(t *testing.T, publicKey *rsa.PublicKey, envelope testJWSEnvelope) {
	t.Helper()
	signature := decodeBase64URL(t, envelope.Signature)
	digest := sha256.Sum256([]byte(envelope.Protected + "." + envelope.Payload))
	if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], signature); err != nil {
		t.Fatalf("verify JWS signature: %v", err)
	}
}
