package ui

import (
	"fmt"

	"github.com/kloudkit/portico/internal/control"
)

// State renders a service's state, with Docker's health when it has one.
func State(svc control.Service) string {
	var s string
	switch svc.State {
	case "running", "up":
		s = Good.Render(svc.State)
	case "starting":
		s = Warn.Render(svc.State)
	case "exited":
		s = Bad.Render(fmt.Sprintf("exited (%d)", *svc.ExitCode))
	default:
		s = Bad.Render(svc.State)
	}

	switch svc.Health {
	case "healthy":
		s += Faint.Render(" · ") + Good.Render("healthy")
	case "unhealthy":
		s += Faint.Render(" · ") + Bad.Render("unhealthy")
	case "starting":
		s += Faint.Render(" · ") + Warn.Render("health starting")
	}

	return s
}
