package handler

import (
	"docktun/internal/service"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

type Setup struct {
	svc  *service.SetupService
	auth *Auth
}

func NewSetup(svc *service.SetupService, auth *Auth) *Setup { return &Setup{svc: svc, auth: auth} }

// Status is intentionally minimal, including when force=true is supplied.
func (h *Setup) Status(w http.ResponseWriter, r *http.Request) {
	locked, err := h.svc.Locked()
	if err != nil {
		setupError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"completed": locked, "has_admin_user": locked})
}
func (h *Setup) Details(w http.ResponseWriter, r *http.Request) {
	id, _ := r.Context().Value("user_id").(int)
	if id <= 0 {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	status, err := h.svc.GetStatus(false)
	if err != nil {
		setupError(w, err)
		return
	}
	status.AdminUsername, _ = r.Context().Value("username").(string)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}
func setupError(w http.ResponseWriter, err error) {
	code, message := http.StatusInternalServerError, "setup failed; sign in if the account was already created"
	switch {
	case errors.Is(err, service.ErrSetupToken):
		code, message = http.StatusUnauthorized, err.Error()
	case errors.Is(err, service.ErrSetupLocked):
		code, message = http.StatusConflict, err.Error()
	case errors.Is(err, service.ErrSetupInput), errors.Is(err, service.ErrSetupIdentity):
		code, message = http.StatusBadRequest, err.Error()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
func (h *Setup) Complete(w http.ResponseWriter, r *http.Request)    { h.save(w, r, false) }
func (h *Setup) Reconfigure(w http.ResponseWriter, r *http.Request) { h.save(w, r, true) }
func (h *Setup) save(w http.ResponseWriter, r *http.Request, reconfigure bool) {
	var req service.SetupCompleteRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request format"}`, http.StatusBadRequest)
		return
	}
	var result service.SetupCompleteResult
	var err error
	if reconfigure {
		id, _ := r.Context().Value("user_id").(int)
		result, err = h.svc.Reconfigure(req, id, r.RemoteAddr)
	} else {
		result, err = h.svc.Complete(req, r.RemoteAddr, func(id int, username string) (string, string, error) {
			access, err := h.auth.GenerateToken(id, username, 15*time.Minute)
			if err != nil {
				return "", "", err
			}
			refresh, err := h.auth.GenerateToken(id, username, 7*24*time.Hour)
			return access, refresh, err
		})
	}
	if err != nil {
		setupError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
