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
	config.Role = dashboard.RoleFlagship
	s, err := dashboard.New(zerolog.Nop(), config, sections...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStatusKeepsSectionOrder(t *testing.T) {
	handler := newTestServer(t, dashboard.Config{},
		dashboard.NewSection("b", func() int { return 2 }),
		dashboard.NewSection("a", func() []string { return []string{"x"} }),
	)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var got struct {
		Role     string `json:"role"`
		Sections []struct {
			Key  string          `json:"key"`
			Data json.RawMessage `json:"data"`
		} `json:"sections"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Role != dashboard.RoleFlagship || len(got.Sections) != 2 {
		t.Fatalf("unexpected status %+v", got)
	}
	if got.Sections[0].Key != "b" || string(got.Sections[0].Data) != "2" || got.Sections[1].Key != "a" || string(got.Sections[1].Data) != `["x"]` {
		t.Errorf("unexpected sections %+v", got.Sections)
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

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("DASHBOARD_PORT", "")
	config, err := dashboard.ConfigFromEnv(dashboard.RoleEscort)
	if err != nil || config.Port != 80 {
		t.Errorf("default config = %+v, %v", config, err)
	}

	t.Setenv("DASHBOARD_PORT", "8080")
	config, err = dashboard.ConfigFromEnv(dashboard.RoleEscort)
	if err != nil || config.Port != 8080 {
		t.Errorf("config with port = %+v, %v", config, err)
	}

	t.Setenv("DASHBOARD_PORT", "eighty")
	if _, err = dashboard.ConfigFromEnv(dashboard.RoleEscort); err == nil {
		t.Error("expected error for invalid DASHBOARD_PORT")
	}
}
