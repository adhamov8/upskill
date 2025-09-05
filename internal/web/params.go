package web

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

func ParamInt64(r *http.Request, name string) (int64, error) {
	return strconv.ParseInt(chi.URLParam(r, name), 10, 64)
}

func QueryInt(r *http.Request, name string, def int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return i
}

func QueryString(r *http.Request, name, def string) string {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	return v
}
