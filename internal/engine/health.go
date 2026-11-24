package engine

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Kaua-KGzin/arch-deploy/internal/config"
)

// WaitHealthy polls url until it answers the expected status (200 by
// default) or the timeout (60s by default) runs out.
func WaitHealthy(ctx context.Context, client *http.Client, url string, hc *config.Healthcheck, out io.Writer) error {
	expect := hc.Expect
	if expect == 0 {
		expect = http.StatusOK
	}
	timeout := hc.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	interval := hc.Interval
	if interval <= 0 {
		interval = 3 * time.Second
	}

	fmt.Fprintf(out, "> health check: GET %s (espera %d, ate %s)\n", url, expect, timeout)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	last := "sem resposta"
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", "arch-deploy")
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == expect {
				fmt.Fprintf(out, "health check ok (%d)\n", resp.StatusCode)
				return nil
			}
			last = fmt.Sprintf("respondeu %d", resp.StatusCode)
		} else if ctx.Err() == nil {
			last = err.Error()
		}
		if err := sleep(ctx, interval); err != nil {
			return fmt.Errorf("%w apos %s: %s", ErrUnhealthy, timeout, last)
		}
	}
}
