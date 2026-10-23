package account

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// The JSON API's plumbing: errors with a status and a sentence for the
// player, the POST wrapper with its same-origin, JSON and size checks, and
// the JSON helpers.

// apiError is an answer other than 2xx: its status and a sentence the
// player can read.
type apiError struct {
	status int
	msg    string
}

func (e *apiError) Error() string { return e.msg }

func fail(status int, msg string) error { return &apiError{status, msg} }

var (
	errSignIn     = fail(http.StatusUnauthorized, "Sign in first.")
	errBadRequest = fail(http.StatusBadRequest, "That request was not understood.")
	errSlowDown   = fail(http.StatusTooManyRequests, "Too many tries: wait a minute, then try again.")
	errServer     = fail(http.StatusServiceUnavailable, "The server is busy. Try again in a moment.")
)

// post wraps a JSON POST handler: same origin only, a JSON body of at most
// maxBodyBytes, and a JSON answer.
func (s *Service) post(h func(w http.ResponseWriter, r *http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "Requests must come from this site."})
			return
		}
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Send JSON."})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
		defer cancel()
		v, err := h(w, r.WithContext(ctx))
		var api *apiError
		switch {
		case err == nil:
			writeJSON(w, http.StatusOK, v)
		case errors.As(err, &api):
			writeJSON(w, api.status, map[string]string{"error": api.msg})
		case errors.Is(err, errBusy):
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": errServer.Error()})
		default:
			s.cfg.Log.Error("account request failed", "path", r.URL.Path, "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Something went wrong on the server."})
		}
	}
}

// sameOrigin refuses requests a browser sent from another site. The
// session cookie is SameSite=Strict and the API wants JSON, which another
// site cannot send without asking first; this is the third lock.
func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // not a browser, or one too old to send it: the other locks still hold
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errBadRequest
	}
	return nil
}
