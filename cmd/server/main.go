package main

import (
	"log"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"
	"github.com/steven230500/introduce-api/internal/auth"
	"github.com/steven230500/introduce-api/internal/health"
	"github.com/steven230500/introduce-api/internal/media"
	"github.com/steven230500/introduce-api/internal/storage"
)

func main() {
	_ = godotenv.Load()

	store, err := storage.NewDisk(
		env("UPLOADS_DIR", "./uploads"),
		env("FILES_BASE_URL", "http://localhost:8080/files"),
	)
	if err != nil {
		log.Fatalf("storage init: %v", err)
	}

	verifier := auth.NewSupabaseVerifier(env("SUPABASE_JWT_SECRET", ""))

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)
	r.Use(corsMiddleware)

	r.Get("/health", health.Handler)

	// Serve uploaded files as static
	uploadsDir := env("UPLOADS_DIR", "./uploads")
	r.Handle("/files/*", http.StripPrefix("/files/", http.FileServer(http.Dir(uploadsDir))))

	r.Group(func(r chi.Router) {
		r.Use(verifier.Middleware)
		r.Mount("/media", media.NewHandler(store).Router())
	})

	port := env("PORT", "8080")
	log.Printf("introduce-api listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, r))
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
