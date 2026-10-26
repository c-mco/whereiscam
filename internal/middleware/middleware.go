package middleware

import (
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"whereis.cam/internal/storage"
)

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// Logging logs request method, path, remote address, status code, and duration.
func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(wrapped, r)

		duration := time.Since(start)
		log.Printf("%s %s %d %s %s", r.Method, r.URL.Path, wrapped.statusCode, duration, r.RemoteAddr)
	})
}

// SecurityHeaders applies baseline HTTP security headers.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

// Recoverer catches panics and returns a 500 error instead of terminating the server.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic recovered: %v", rec)
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

const (
	hitQueueSize  = 512
	hitRetention  = 90 * 24 * time.Hour
	hitPruneEvery = 6 * time.Hour
)

type hitRecord struct {
	path, ip, ua, ref string
}

// HitTracker records successful non-static, non-API page requests into SQLite.
// Hits are handed to a single background writer through a bounded queue and
// dropped when it is full, so request bursts never pile up on SQLite's writer.
// Hits older than hitRetention are pruned periodically.
func HitTracker(db *storage.DB) func(http.Handler) http.Handler {
	var queue chan hitRecord
	if db != nil {
		queue = make(chan hitRecord, hitQueueSize)
		go runHitWriter(db, queue)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			if queue == nil || !trackablePath(path) {
				next.ServeHTTP(w, r)
				return
			}

			wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
			next.ServeHTTP(wrapped, r)

			// Skip 404s from scanners and other failed requests.
			if wrapped.statusCode >= 400 {
				return
			}
			select {
			case queue <- hitRecord{path: path, ip: extractClientIP(r), ua: r.UserAgent(), ref: r.Referer()}:
			default:
				// Queue full; drop the hit rather than block or spawn more work.
			}
		})
	}
}

func trackablePath(path string) bool {
	return !strings.HasPrefix(path, "/static/") &&
		!strings.HasPrefix(path, "/favicon") &&
		!strings.HasPrefix(path, "/api/") &&
		path != "/sw.js" &&
		path != "/manifest.json" &&
		path != "/health"
}

func runHitWriter(db *storage.DB, queue <-chan hitRecord) {
	prune := func() {
		if n, err := db.PruneHits(time.Now().Add(-hitRetention)); err != nil {
			log.Printf("page hit pruning failed: %v", err)
		} else if n > 0 {
			log.Printf("pruned %d page hits older than %s", n, hitRetention)
		}
	}
	prune()

	ticker := time.NewTicker(hitPruneEvery)
	defer ticker.Stop()
	for {
		select {
		case h := <-queue:
			if err := db.RecordHit(h.path, h.ip, h.ua, h.ref); err != nil {
				log.Printf("failed to record page hit: %v", err)
			}
		case <-ticker.C:
			prune()
		}
	}
}

func extractClientIP(r *http.Request) string {
	if ip := r.Header.Get("CF-Connecting-IP"); ip != "" {
		return strings.TrimSpace(ip)
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}
