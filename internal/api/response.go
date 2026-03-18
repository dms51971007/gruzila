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

// WriteSuccess записывает унифицированный успешный API-ответ.
// HTTP-код остаётся 200, а признак успеха передаётся в поле status.
func WriteSuccess(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, Response{
		Status: "success",
		Data:   data,
	})
}

// WriteError записывает унифицированный API-ответ с ошибкой.
// По текущему контракту API ошибки также возвращаются c HTTP 200,
// а фактический результат клиент определяет по полю status.
func WriteError(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusOK, Response{
		Status: "error",
		Error:  msg,
	})
}

// writeJSON — низкоуровневый helper для сериализации ответа.
// Вызывается только из WriteSuccess/WriteError.
func writeJSON(w http.ResponseWriter, code int, resp Response) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(resp)
}
