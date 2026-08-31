package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"ssh-manager-tui/internal/config"
	"ssh-manager-tui/internal/tui"
)

func main() {
	store, err := config.NewStore()
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuration error:", err)
		os.Exit(1)
	}

	model, err := tui.New(store)
	if err != nil {
		fmt.Fprintln(os.Stderr, "startup error:", err)
		os.Exit(1)
	}

	program := tea.NewProgram(model)
	if _, err := program.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "ssh-manager:", err)
		os.Exit(1)
	}
}
