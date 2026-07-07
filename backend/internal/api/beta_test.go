package api_test

// Beta-signup handler unit tests: POST /api/beta-signup, against a fake
// registrar over real net/http (mirrors push_test.go's shape).

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/api"
	"github.com/crabtree-michael/kiln/backend/internal/wire"
)

var errFakeBetaFailed = errors.New("fakeBetaRegistrar: synthetic failure")

// fakeBetaRegistrar records the emails it is asked to store; err forces a
// failure path.
type fakeBetaRegistrar struct {
	mu     sync.Mutex
	emails []string
	err    error
}

func (f *fakeBetaRegistrar) Register(_ context.Context, email string) error {
	if f.err != nil {
		return f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.emails = append(f.emails, email)
	return nil
}

func newBetaServer(t *testing.T, reg api.BetaRegistrar) *httptest.Server {
	t.Helper()
	srv := newBareServer()
	srv.EnableBeta(reg)
	return httptest.NewServer(srv.Handler())
}

func TestHandleBetaSignup(t *testing.T) {
	body := func(email string) []byte {
		return mustJSON(t, wire.BetaSignupRequest{Email: email})
	}

	t.Run("records the email and returns 202", func(t *testing.T) {
		reg := &fakeBetaRegistrar{}
		ts := newBetaServer(t, reg)
		defer ts.Close()
		resp := doPost(t, ts.URL+"/api/beta-signup", body("  user@example.com "))
		closeBody(t, resp)
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("status = %d, want 202", resp.StatusCode)
		}
		reg.mu.Lock()
		defer reg.mu.Unlock()
		if len(reg.emails) != 1 || reg.emails[0] != "user@example.com" {
			t.Fatalf("stored %v, want [user@example.com] (trimmed)", reg.emails)
		}
	})

	t.Run("rejects a malformed email with 400", func(t *testing.T) {
		reg := &fakeBetaRegistrar{}
		ts := newBetaServer(t, reg)
		defer ts.Close()
		resp := doPost(t, ts.URL+"/api/beta-signup", body("not-an-email"))
		closeBody(t, resp)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
		reg.mu.Lock()
		defer reg.mu.Unlock()
		if len(reg.emails) != 0 {
			t.Errorf("stored %d emails on bad request, want 0", len(reg.emails))
		}
	})

	t.Run("rejects an empty email with 400", func(t *testing.T) {
		ts := newBetaServer(t, &fakeBetaRegistrar{})
		defer ts.Close()
		resp := doPost(t, ts.URL+"/api/beta-signup", body(""))
		closeBody(t, resp)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("rejects invalid JSON with 400", func(t *testing.T) {
		ts := newBetaServer(t, &fakeBetaRegistrar{})
		defer ts.Close()
		resp := doPost(t, ts.URL+"/api/beta-signup", []byte("{not json"))
		closeBody(t, resp)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("registrar error returns 500", func(t *testing.T) {
		reg := &fakeBetaRegistrar{err: errFakeBetaFailed}
		ts := newBetaServer(t, reg)
		defer ts.Close()
		resp := doPost(t, ts.URL+"/api/beta-signup", body("user@example.com"))
		closeBody(t, resp)
		if resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", resp.StatusCode)
		}
	})
}
