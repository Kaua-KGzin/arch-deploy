package hub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Kaua-KGzin/arch-deploy/internal/config"
)

func TestHeartbeatHitsTheArchLineEndpoint(t *testing.T) {
	var path, token string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, token = r.Method+" "+r.URL.Path, r.Header.Get("X-Hub-Token")
	}))
	defer srv.Close()

	t.Setenv("ARCHHUB_URL", srv.URL+"/")
	t.Setenv("ARCHHUB_TOKEN", "t0k")
	s, ok := Resolve(&config.Config{Project: "system-pvd"})
	if !ok {
		t.Fatal("deveria usar ARCHHUB_URL")
	}
	if err := Heartbeat(context.Background(), srv.Client(), s); err != nil {
		t.Fatal(err)
	}
	if path != "POST /api/v1/systems/system-pvd/heartbeat" || token != "t0k" {
		t.Fatalf("path=%q token=%q", path, token)
	}
}

func TestHeartbeatExplainsUnregisteredSystem(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	err := Heartbeat(context.Background(), srv.Client(), Settings{URL: srv.URL, System: "ghost"})
	if err == nil || !strings.Contains(err.Error(), "nao esta registrado") {
		t.Fatalf("err = %v", err)
	}
}

func TestDisabledWithoutURL(t *testing.T) {
	t.Setenv("ARCHHUB_URL", "")
	if _, ok := Resolve(&config.Config{Project: "x"}); ok {
		t.Fatal("sem URL o Hub deveria ficar desligado")
	}
}
