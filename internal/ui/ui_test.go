package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
)

func TestNestedDisplayOwnership(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	ctx, root, finishRoot := Start(context.Background(), "share")
	_, nested, finishNested := Start(ctx, "install")
	if nested != root {
		t.Fatal("nested phase created a separate display")
	}
	// A nested phase must not send a finish message or wait on the root renderer.
	root.done = make(chan struct{})
	finished := make(chan struct{})
	go func() {
		finishNested(nil)
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("nested phase tried to finish the root display")
	}
	finishRoot(nil)
}

func TestViewUpdatesCurrentStatusWithoutBorders(t *testing.T) {
	m := model{spinner: spinner.New(), title: "share", started: time.Now(), status: "Preparing"}
	next, _ := m.Update(event{"Installing"})
	next, _ = next.Update(event{"tunnel up: https://example.com → localhost:3000"})
	view := next.View()
	if strings.Contains(view, "Preparing") || strings.Contains(view, "Installing") {
		t.Fatalf("completed phases remain in display: %q", view)
	}
	if strings.ContainsAny(view, "╭╮╰╯│─") {
		t.Fatalf("display has a bounding box: %q", view)
	}
	if !strings.Contains(view, "tunnel up:") || strings.Count(view, "SPRITE TUNNEL") != 1 {
		t.Fatalf("missing or duplicated active display: %q", view)
	}
}
