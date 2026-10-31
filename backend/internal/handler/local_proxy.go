package handler

import (
	"encoding/json"
	"net/http"

	"docktun/internal/service"
)

type LocalProxy struct{ proxy *service.LocalProxy }

func NewLocalProxy(proxy *service.LocalProxy) *LocalProxy { return &LocalProxy{proxy: proxy} }

func (h *LocalProxy) Get(w http.ResponseWriter, r *http.Request) {
	status, err := h.proxy.Status(r.Context())
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(status)
}

func (h *LocalProxy) Put(w http.ResponseWriter, r *http.Request) {
	var input service.LocalProxyConfig
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		jsonError(w, "invalid local proxy configuration", http.StatusBadRequest)
		return
	}
	status, err := h.proxy.Apply(r.Context(), input)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	json.NewEncoder(w).Encode(status)
}
