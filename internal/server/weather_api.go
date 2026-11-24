package server

import (
	"net/http"
	"strings"
)

// Pocket Weather, to pick a clock's place: your places, or a search.

func (s *Server) handleWeatherPlaces(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	conn, err := s.clients(ctx, actorID(ctx)).conn(ctx, appWeather, scopeWeatherRead)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"connected": false, "error": err.Error(), "places": []any{}})
		return
	}
	ps, err := conn.client.Places(ctx)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"connected": true, "error": err.Error(), "places": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": true, "places": ps})
}

func (s *Server) handleWeatherSearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(q)) < 2 {
		writeJSON(w, http.StatusOK, map[string]any{"connected": true, "places": []any{}})
		return
	}
	conn, err := s.clients(ctx, actorID(ctx)).conn(ctx, appWeather, scopeWeatherRead)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"connected": false, "error": err.Error(), "places": []any{}})
		return
	}
	ps, err := conn.client.SearchPlaces(ctx, q)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"connected": true, "error": err.Error(), "places": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": true, "places": ps})
}
