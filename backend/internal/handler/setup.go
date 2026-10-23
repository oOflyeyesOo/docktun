package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"docktun/internal/service"
)

type Setup struct {
	svc  *service.SetupService
	auth *Auth
}

func NewSetup(svc *service.SetupService, auth *Auth) *Setup {
	return &Setup{svc: svc, auth: auth}
}

func (h *Setup) Status(w http.ResponseWriter, r *http.Request) {
	force := r.URL.Query().Get("force") == "true"
	status, err := h.svc.GetStatus(force)
	if err != nil {
		http.Error(w, `{"error":"failed to fetch setup status"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (h *Setup) Complete(w http.ResponseWriter, r *http.Request) {
	var req service.SetupCompleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request format"}`, http.StatusBadRequest)
		return
	}

	tokenGen := func(adminID int, username string) (string, string, error) {
		access, err := h.auth.GenerateToken(adminID, username, 15*time.Minute)
		if err != nil {
			return "", "", err
		}
		refresh, err := h.auth.GenerateToken(adminID, username, 7*24*time.Hour)
		if err != nil {
			return "", "", err
		}
		return access, refresh, nil
	}

	result, err := h.svc.Complete(req, r.RemoteAddr, tokenGen)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
