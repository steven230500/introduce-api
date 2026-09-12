package auth

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/steven230500/introduce-api/internal/httpx"
)

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// PublicRouter holds the routes that must work without a token.
func (h *Handler) PublicRouter() chi.Router {
	r := chi.NewRouter()
	r.Post("/register", h.register)
	r.Post("/login", h.login)
	r.Post("/refresh", h.refresh)
	return r
}

// PrivateRouter holds the routes that need one.
func (h *Handler) PrivateRouter() chi.Router {
	r := chi.NewRouter()
	r.Get("/me", h.me)
	r.Post("/logout", h.logout)
	return r
}

type credentials struct {
	Email       string  `json:"email"`
	Password    string  `json:"password"`
	DisplayName *string `json:"display_name,omitempty"`
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var body credentials
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	session, err := h.svc.Register(r.Context(), body.Email, body.Password, body.DisplayName)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, session)
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var body credentials
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	session, err := h.svc.Login(r.Context(), body.Email, body.Password)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, session)
}

func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	session, err := h.svc.Refresh(r.Context(), body.RefreshToken)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, session)
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	userID, _ := UserID(r.Context())
	user, orgID, err := h.svc.Me(r.Context(), userID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"user": user, "org_id": orgID})
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	userID, _ := UserID(r.Context())
	if err := h.svc.Logout(r.Context(), userID); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.NoContent(w)
}
