package httpx

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pinger interface {
	Ping(context.Context) error
}

func NewRouter(pool *pgxpool.Pool, allowedOrigin string, registerRoutes ...func(chi.Router)) http.Handler {
	return NewRouterWithPinger(pool, allowedOrigin, registerRoutes...)
}

func NewRouterWithPinger(database pinger, allowedOrigin string, registerRoutes ...func(chi.Router)) http.Handler {
	router := chi.NewRouter()
	router.Use(RequestID)
	router.Use(RequestLogger)
	router.Use(Recoverer)
	router.Use(CORS(allowedOrigin))
	router.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	router.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := database.Ping(ctx); err != nil {
			WriteError(w, http.StatusServiceUnavailable, "database_unavailable", "Database is not ready", nil)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	for _, register := range registerRoutes {
		register(router)
	}
	return router
}
