// gsmd: HTTP daemon that wraps a ZTE router to send SMS.
//
// Configuration: a JSON file at /etc/gsmd.conf (override with -config).
// See gsmd.conf.example for the schema.
//
// Endpoints:
//
//	POST /sms       (auth required) body {"number":"...","message":"..."}
//	GET  /healthz   (no auth)
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/time/rate"

	"gsmd/internal/config"
	"gsmd/internal/httpx"
	"gsmd/internal/zte"
)

const maxBodyBytes = 64 << 10

func main() {
	configPath := flag.String("config", "/etc/gsmd.conf", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("config load failed", "path", *configPath, "err", err)
		os.Exit(1)
	}

	client, err := zte.New(cfg.CPEHost, cfg.CPEUser, cfg.CPEPass)
	if err != nil {
		slog.Error("zte.New failed", "err", err)
		os.Exit(1)
	}

	limiter := rate.NewLimiter(rate.Limit(float64(cfg.RateLimitPerMin)/60.0), cfg.RateLimitBurst)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /sms",
		httpx.Auth(cfg.Tokens,
			httpx.RateLimit(limiter,
				httpx.MaxBody(maxBodyBytes,
					sendHandler(client)))))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		slog.Info("listening",
			"addr", cfg.Listen,
			"router", cfg.CPEHost,
			"callers", len(cfg.Tokens),
			"rate_per_min", cfg.RateLimitPerMin,
			"rate_burst", cfg.RateLimitBurst)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server failed", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	slog.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	_ = client.Logout()
}

type sendRequest struct {
	Number  string `json:"number"`
	Message string `json:"message"`
}

func sendHandler(c *zte.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		caller := httpx.CallerFromContext(r.Context())

		var req sendRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			if httpx.IsMaxBytesError(err) {
				writeErr(w, http.StatusRequestEntityTooLarge, "request body too large")
				return
			}
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		req.Number = strings.TrimSpace(req.Number)
		if req.Number == "" {
			writeErr(w, http.StatusBadRequest, "number is required")
			return
		}
		if req.Message == "" {
			writeErr(w, http.StatusBadRequest, "message is required")
			return
		}

		if err := c.SendSMS(req.Number, req.Message); err != nil {
			slog.Warn("SendSMS failed",
				"caller", caller,
				"to", redact(req.Number),
				"len", len(req.Message),
				"err", err)
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		slog.Info("SMS sent",
			"caller", caller,
			"to", redact(req.Number),
			"len", len(req.Message))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":"success"}` + "\n"))
	}
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// redact keeps the first 3 and last 2 digits of the number, masking the rest.
func redact(s string) string {
	if len(s) <= 5 {
		return strings.Repeat("•", len(s))
	}
	return s[:3] + strings.Repeat("•", len(s)-5) + s[len(s)-2:]
}
