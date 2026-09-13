// Command server runs the Introduce API: identity, the content library, and the
// websocket hub that keeps projector windows in step with the operator.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"

	"github.com/steven230500/introduce-api/internal/auth"
	"github.com/steven230500/introduce-api/internal/collections"
	"github.com/steven230500/introduce-api/internal/config"
	"github.com/steven230500/introduce-api/internal/db"
	"github.com/steven230500/introduce-api/internal/health"
	"github.com/steven230500/introduce-api/internal/media"
	"github.com/steven230500/introduce-api/internal/org"
	"github.com/steven230500/introduce-api/internal/presentation"
	"github.com/steven230500/introduce-api/internal/songs"
	"github.com/steven230500/introduce-api/internal/storage"
	"github.com/steven230500/introduce-api/internal/templates"
)

func main() {
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	log.Println("schema up to date")

	store, err := newStore(cfg.UploadsDir, cfg.FilesBaseURL)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}
	log.Printf("uploads go to %s", store.Kind())

	authRepo := auth.NewRepo(pool)
	tokens := auth.NewTokenIssuer(cfg.JWTSecret, cfg.AccessTTL, cfg.RefreshTTL)
	authSvc := auth.NewService(authRepo, tokens)
	hub := presentation.NewHub()

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware(cfg.AllowedOrigin))

	r.Get("/health", health.Handler)
	r.Handle("/files/*", http.StripPrefix("/files/",
		http.FileServer(http.Dir(cfg.UploadsDir))))

	r.Mount("/auth", auth.NewHandler(authSvc).PublicRouter())

	// Signed in, but not necessarily in an organization yet: this is where a
	// new account creates one or asks to join.
	r.Group(func(r chi.Router) {
		r.Use(authSvc.Middleware)
		r.Mount("/account", auth.NewHandler(authSvc).PrivateRouter())
		r.Mount("/org", org.NewHandler(org.NewRepo(pool), authRepo).Router())
		r.Mount("/presentation", presentation.NewHandler(presentation.NewRepo(pool), hub).Router())
	})

	// Everything that belongs to an organization.
	r.Group(func(r chi.Router) {
		r.Use(authSvc.Middleware)
		r.Use(auth.RequireOrg)
		r.Mount("/songs", songs.NewHandler(songs.NewRepo(pool)).Router())
		r.Mount("/templates", templates.NewHandler(templates.NewRepo(pool)).Router())
		r.Mount("/collections", collections.NewHandler(collections.NewRepo(pool)).Router())
		r.Mount("/media", media.NewHandler(store, media.NewRepo(pool)).Router())
	})

	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		// No write timeout: websocket connections are meant to stay open for
		// the length of a service.
	}

	go func() {
		log.Printf("introduce-api listening on :%s", cfg.Port)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

func corsMiddleware(origin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods",
				"GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// newStore picks where uploads land.
//
// A bucket if one is configured, the machine's own disk otherwise. Failing
// hard on a half-configured bucket is deliberate: silently falling back to
// disk would fill the droplet over weeks and only show up when it is full.
func newStore(uploadsDir, filesBaseURL string) (storage.Store, error) {
	if cfg := storage.S3FromEnv(); cfg.Configured() {
		return storage.NewS3(cfg)
	}
	return storage.NewDisk(uploadsDir, filesBaseURL)
}
