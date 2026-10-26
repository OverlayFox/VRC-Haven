package dashboard_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/OverlayFox/VRC-Haven/src/dashboard"
)

func newTestServer(t *testing.T, config dashboard.Config, sections ...dashboard.Section) http.Handler {
	t.Helper()
	config.IsFlagship = true
	s, err := dashboard.New(zerolog.Nop(), config, sections...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

type statusResponse struct {
	IsFlagship bool `json:"role"`
	Sections   []struct {
		Key  string          `json:"key"`
		Data json.RawMessage `json:"data"`
	} `json:"sections"`
}

func getStatus(t *testing.T, handler http.Handler) statusResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var got statusResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestStatusKeepsSectionOrder(t *testing.T) {
	handler := newTestServer(t, dashboard.Config{},
		dashboard.NewSection("b", func() int { return 2 }),
		dashboard.NewSection("a", func() []string { return []string{"x"} }),
	)

	got := getStatus(t, handler)
	if len(got.Sections) != 2 {
		t.Fatalf("unexpected status %+v", got)
	}
	if got.Sections[0].Key != "b" || string(got.Sections[0].Data) != "2" || got.Sections[1].Key != "a" || string(got.Sections[1].Data) != `["x"]` {
		t.Errorf("unexpected sections %+v", got.Sections)
	}
}

func TestStatusIsFlagship(t *testing.T) {
	for _, isFlagship := range []bool{true, false} {
		s, err := dashboard.New(zerolog.Nop(), dashboard.Config{IsFlagship: isFlagship})
		if err != nil {
			t.Fatal(err)
		}
		if got := getStatus(t, s); got.IsFlagship != isFlagship {
			t.Errorf("IsFlagship %v: status reported %v", isFlagship, got.IsFlagship)
		}
	}
}

func TestServesEmbeddedPage(t *testing.T) {
	handler := newTestServer(t, dashboard.Config{})
	for _, path := range []string{"/", "/app.js", "/app.css"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
			t.Errorf("GET %s = %d with %d bytes", path, rec.Code, rec.Body.Len())
		}
	}
}

func TestBasicAuth(t *testing.T) {
	handler := newTestServer(t, dashboard.Config{Username: "admin", Password: "secret"})

	tests := []struct {
		name       string
		user, pass string
		setAuth    bool
		want       int
	}{
		{name: "no credentials", want: http.StatusUnauthorized},
		{name: "wrong password", user: "admin", pass: "nope", setAuth: true, want: http.StatusUnauthorized},
		{name: "wrong user", user: "root", pass: "secret", setAuth: true, want: http.StatusUnauthorized},
		{name: "correct", user: "admin", pass: "secret", setAuth: true, want: http.StatusOK},
	}
	for _, tt := range tests {
		for _, path := range []string{"/", "/api/status"} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			if tt.setAuth {
				req.SetBasicAuth(tt.user, tt.pass)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Errorf("%s: GET %s = %d, want %d", tt.name, path, rec.Code, tt.want)
			}
			if tt.want == http.StatusUnauthorized && !strings.HasPrefix(rec.Header().Get("WWW-Authenticate"), "Basic") {
				t.Errorf("%s: missing WWW-Authenticate header", tt.name)
			}
		}
	}
}

func TestDefaultConfig(t *testing.T) {
	var config dashboard.Config
	config.SetDefaults()

	if config.Port != 8080 {
		t.Errorf("default port = %d, want 8080", config.Port)
	}
	if err := config.Validate(); err != nil {
		t.Errorf("default config is invalid: %v", err)
	}
}

func TestValidatePort(t *testing.T) {
	for _, port := range []int{-1, 65536} {
		if err := (dashboard.Config{Port: port}).Validate(); err == nil {
			t.Errorf("expected error for port %d", port)
		}
	}
	if _, err := dashboard.New(zerolog.Nop(), dashboard.Config{Port: 70000}); err == nil {
		t.Error("New accepted port 70000")
	}
}
