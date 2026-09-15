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
	r.Post("/reset-password", h.resetPassword)
	return r
}

// PrivateRouter holds the routes that need one.
func (h *Handler) PrivateRouter() chi.Router {
	r := chi.NewRouter()
	r.Get("/me", h.me)
	r.Post("/password", h.changePassword)
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

// resetPassword takes the code an administrator of the church handed out.
func (h *Handler) resetPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email       string `json:"email"`
		Code        string `json:"code"`
		NewPassword string `json:"new_password"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.svc.ResetPassword(r.Context(), body.Email, body.Code, body.NewPassword); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.NoContent(w)
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

func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	userID, _ := UserID(r.Context())
	if err := h.svc.ChangePassword(
		r.Context(), userID, body.CurrentPassword, body.NewPassword); err != nil {
		httpx.WriteError(w, err)
		return
	}
	// Every session is gone, including this one: the client must sign in again.
	httpx.NoContent(w)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	userID, _ := UserID(r.Context())
	if err := h.svc.Logout(r.Context(), userID); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.NoContent(w)
}
