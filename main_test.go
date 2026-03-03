package main

import (
	"testing"

	"deploy-cli/internal/cli"
)

func TestNewRootCommand(t *testing.T) {
	cmd := cli.NewRootCommand()
	if cmd == nil {
		t.Fatal("expected non-nil root command")
	}
	if cmd.Use != "deploy-cli" {
		t.Fatalf("unexpected command name: %s", cmd.Use)
	}
}
