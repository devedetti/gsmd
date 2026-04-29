package httpx

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/time/rate"
)

func newAuthHandler(tokens map[string]string) http.HandlerFunc {
	inner := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "caller="+CallerFromContext(r.Context()))
	}
	return Auth(tokens, inner)
}

func TestAuth_NoHeader(t *testing.T) {
	h := newAuthHandler(map[string]string{"ha": "abc"})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d want 401", rec.Code)
	}
}

func TestAuth_NotBearer(t *testing.T) {
	h := newAuthHandler(map[string]string{"ha": "abc"})
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	h(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d want 401", rec.Code)
	}
}

func TestAuth_BearerCaseInsensitive(t *testing.T) {
	h := newAuthHandler(map[string]string{"ha": "abc"})
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set("Authorization", "bearer abc")
	h(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "caller=ha") {
		t.Fatalf("expected caller=ha in body, got %q", rec.Body.String())
	}
}

func TestAuth_WrongToken(t *testing.T) {
	h := newAuthHandler(map[string]string{"ha": "abc"})
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set("Authorization", "Bearer wrong")
	h(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d want 401", rec.Code)
	}
}

func TestAuth_GoodToken_PicksCorrectCaller(t *testing.T) {
	h := newAuthHandler(map[string]string{"ha": "abc", "grafana": "def"})
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set("Authorization", "Bearer def")
	h(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "caller=grafana") {
		t.Fatalf("expected caller=grafana, got %q", rec.Body.String())
	}
}

func TestAuth_MalformedHeader(t *testing.T) {
	h := newAuthHandler(map[string]string{"ha": "abc"})
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set("Authorization", "JustOneWord")
	h(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d want 401", rec.Code)
	}
}

func TestRateLimit(t *testing.T) {
	// Burst 2, no refill: first 2 pass, then all 429.
	lim := rate.NewLimiter(rate.Limit(0), 2)
	allowed, denied := 0, 0
	inner := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	h := RateLimit(lim, inner)
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodPost, "/", nil))
		switch rec.Code {
		case http.StatusOK:
			allowed++
		case http.StatusTooManyRequests:
			denied++
		default:
			t.Fatalf("unexpected code %d", rec.Code)
		}
	}
	if allowed != 2 || denied != 3 {
		t.Errorf("got allowed=%d denied=%d, want 2/3", allowed, denied)
	}
}

func TestMaxBody_UnderCap(t *testing.T) {
	inner := func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
	h := MaxBody(10, inner)
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte("hello"))))
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d want 200", rec.Code)
	}
}

func TestMaxBody_OverCap(t *testing.T) {
	inner := func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err != nil {
			if IsMaxBytesError(err) {
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				return
			}
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
	h := MaxBody(10, inner)
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(bytes.Repeat([]byte("a"), 100))))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("got %d want 413", rec.Code)
	}
}

func TestCallerFromContext_EmptyAndWrongType(t *testing.T) {
	if got := CallerFromContext(context.Background()); got != "" {
		t.Errorf("empty ctx: got %q want \"\"", got)
	}
	ctx := context.WithValue(context.Background(), ctxKeyCaller, 123)
	if got := CallerFromContext(ctx); got != "" {
		t.Errorf("non-string value: got %q want \"\"", got)
	}
}
