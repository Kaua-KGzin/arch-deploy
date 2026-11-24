// Package hub tells the Arch Hub that a system was just deployed, using the
// heartbeat endpoint of the Arch line contract:
//
//	POST {ARCHHUB_URL}/api/v1/systems/{id}/heartbeat   (X-Hub-Token optional)
package hub

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Kaua-KGzin/arch-deploy/internal/config"
)

type Settings struct {
	URL    string
	System string
	Token  string
}

// Resolve fills the hub settings from the config, then ARCHHUB_URL /
// ARCHHUB_TOKEN, with the project name as system id. Enabled is false when
// no URL is known.
func Resolve(cfg *config.Config) (Settings, bool) {
	s := Settings{URL: cfg.Hub.URL, System: cfg.Hub.System, Token: cfg.Hub.Token}
	if s.URL == "" {
		s.URL = os.Getenv("ARCHHUB_URL")
	}
	if s.Token == "" {
		s.Token = os.Getenv("ARCHHUB_TOKEN")
	}
	if s.System == "" {
		s.System = cfg.Project
	}
	s.URL = strings.TrimRight(s.URL, "/")
	return s, s.URL != ""
}

// Heartbeat sends one heartbeat. A 404 means the system never registered.
func Heartbeat(ctx context.Context, client *http.Client, s Settings) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	endpoint := fmt.Sprintf("%s/api/v1/systems/%s/heartbeat", s.URL, url.PathEscape(s.System))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "arch-deploy")
	if s.Token != "" {
		req.Header.Set("X-Hub-Token", s.Token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("o sistema %q nao esta registrado no Hub", s.System)
	case resp.StatusCode >= 300:
		return fmt.Errorf("Hub respondeu %d", resp.StatusCode)
	}
	return nil
}
