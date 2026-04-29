package zte

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeRouterState struct {
	calls      int
	nextResult string
}

func fakeRouter(t *testing.T) (*httptest.Server, *fakeRouterState) {
	t.Helper()
	state := &fakeRouterState{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":"` + state.nextResult + `"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, state
}

func newClient(t *testing.T, host string) *Client {
	t.Helper()
	c, err := New(host, "admin", "admin")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestLogin_SuccessResetsCounter(t *testing.T) {
	srv, state := fakeRouter(t)
	c := newClient(t, strings.TrimPrefix(srv.URL, "http://"))
	c.consecutiveAuthFails = 2

	state.nextResult = "0"
	if err := c.login(); err != nil {
		t.Fatalf("login should succeed, got %v", err)
	}
	if c.consecutiveAuthFails != 0 {
		t.Errorf("expected counter reset, got %d", c.consecutiveAuthFails)
	}
}

func TestLogin_AlreadyLoggedInResetsCounter(t *testing.T) {
	srv, state := fakeRouter(t)
	c := newClient(t, strings.TrimPrefix(srv.URL, "http://"))
	c.consecutiveAuthFails = 1

	state.nextResult = "4"
	if err := c.login(); err != nil {
		t.Fatalf("login should succeed with result=4, got %v", err)
	}
	if c.consecutiveAuthFails != 0 {
		t.Errorf("expected counter reset on result=4, got %d", c.consecutiveAuthFails)
	}
}

func TestLogin_AuthFailBumpsCounter_StopsAtThreshold(t *testing.T) {
	srv, state := fakeRouter(t)
	c := newClient(t, strings.TrimPrefix(srv.URL, "http://"))

	state.nextResult = "3"
	for i := 1; i <= lockoutThreshold; i++ {
		err := c.login()
		if err == nil {
			t.Fatalf("login should fail at attempt %d", i)
		}
		if c.consecutiveAuthFails != i {
			t.Errorf("attempt %d: counter=%d want %d", i, c.consecutiveAuthFails, i)
		}
	}

	callsBefore := state.calls
	err := c.login()
	if err == nil {
		t.Fatal("expected login to be disabled past threshold")
	}
	if !strings.Contains(err.Error(), "login disabled") {
		t.Errorf("expected disabled-error message, got %v", err)
	}
	if state.calls != callsBefore {
		t.Errorf("expected zero new network calls past threshold, got %d", state.calls-callsBefore)
	}
}

func TestLogin_TransportErrorDoesNotBump(t *testing.T) {
	// 127.0.0.1:1 should refuse — transport error, not auth failure.
	c := newClient(t, "127.0.0.1:1")
	if err := c.login(); err == nil {
		t.Fatal("expected transport error")
	}
	if c.consecutiveAuthFails != 0 {
		t.Errorf("transport error must not bump counter, got %d", c.consecutiveAuthFails)
	}
}

func TestLogin_RecoversAfterPartialFails(t *testing.T) {
	srv, state := fakeRouter(t)
	c := newClient(t, strings.TrimPrefix(srv.URL, "http://"))

	state.nextResult = "3"
	_ = c.login()
	_ = c.login()
	if c.consecutiveAuthFails != 2 {
		t.Fatalf("setup: counter=%d want 2", c.consecutiveAuthFails)
	}
	state.nextResult = "0"
	if err := c.login(); err != nil {
		t.Fatalf("recovery login should succeed, got %v", err)
	}
	if c.consecutiveAuthFails != 0 {
		t.Errorf("counter not reset after success: %d", c.consecutiveAuthFails)
	}
}
