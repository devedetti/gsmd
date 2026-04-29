// Package httpx contains HTTP middleware used by gsmd: bearer auth, rate
// limit, and a body-size cap.
package httpx

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"golang.org/x/time/rate"
)

type ctxKey int

const ctxKeyCaller ctxKey = iota

// CallerFromContext returns the caller name attached by Auth, or "" if the
// request didn't go through Auth.
func CallerFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyCaller).(string)
	return v
}

// Auth wraps next with bearer-token authentication. The tokens map is
// name → token. On success, the caller name is stored in the request
// context. Any failure (missing header, wrong scheme, unknown token) returns
// 401 with a generic message — the kind of failure is not leaked.
func Auth(tokens map[string]string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller, ok := matchToken(tokens, r.Header.Get("Authorization"))
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		ctx := context.WithValue(r.Context(), ctxKeyCaller, caller)
		next(w, r.WithContext(ctx))
	}
}

func matchToken(tokens map[string]string, header string) (string, bool) {
	if header == "" {
		return "", false
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	provided := []byte(parts[1])
	for name, tok := range tokens {
		if subtle.ConstantTimeCompare(provided, []byte(tok)) == 1 {
			return name, true
		}
	}
	return "", false
}

// RateLimit gates next on a token-bucket limiter. Over-limit requests get 429.
func RateLimit(lim *rate.Limiter, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !lim.Allow() {
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next(w, r)
	}
}

// MaxBody caps the request body to n bytes. Handlers that read the body
// should check IsMaxBytesError on read errors to map to 413.
func MaxBody(n int64, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, n)
		next(w, r)
	}
}

// IsMaxBytesError reports whether err comes from the body cap installed by
// MaxBody. Use it in handlers to choose between 400 (bad input) and 413.
func IsMaxBytesError(err error) bool {
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
