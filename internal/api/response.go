package api

import (
	"encoding/json"
	"net/http"
)

type Response struct {
	Status string `json:"status"`
	Data   any    `json:"data,omitempty"`
	Error  string `json:"error,omitempty"`
}

func WriteSuccess(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, Response{
		Status: "success",
		Data:   data,
	})
}

func WriteError(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusOK, Response{
		Status: "error",
		Error:  msg,
	})
}

func writeJSON(w http.ResponseWriter, code int, resp Response) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(resp)
}

