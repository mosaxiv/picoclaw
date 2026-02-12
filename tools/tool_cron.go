package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mosaxiv/clawlet/cron"
)

func (r *Registry) cronTool(ctx context.Context, tctx Context, action, message string, everySeconds int, cronExpr, jobID string) (string, error) {
	if r.Cron == nil {
		return "", errors.New("cron service not configured")
	}
	action = strings.TrimSpace(action)
	switch action {
	case "add":
		message = strings.TrimSpace(message)
		if message == "" {
			return "", errors.New("message is required")
		}
		if tctx.Channel == "" || tctx.ChatID == "" {
			return "", errors.New("no session context (channel/chat_id)")
		}
		var sched cron.Schedule
		if everySeconds > 0 {
			sched = cron.Schedule{Kind: "every", EveryMS: int64(everySeconds) * 1000}
		} else if strings.TrimSpace(cronExpr) != "" {
			sched = cron.Schedule{Kind: "cron", Expr: strings.TrimSpace(cronExpr)}
		} else {
			return "", errors.New("either every_seconds or cron_expr is required")
		}
		payload := cron.Payload{
			Kind:    "agent_turn",
			Message: message,
			Deliver: true,
			Channel: tctx.Channel,
			To:      tctx.ChatID,
		}
		j, err := r.Cron.Add(shortName(message), sched, payload)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Created job '%s' (id: %s)", j.Name, j.ID), nil
	case "list":
		jobs := r.Cron.List(false)
		if len(jobs) == 0 {
			return "No scheduled jobs.", nil
		}
		var b strings.Builder
		b.WriteString("Scheduled jobs:\n")
		for _, j := range jobs {
			b.WriteString(fmt.Sprintf("- %s (id: %s, %s)\n", j.Name, j.ID, j.Schedule.Kind))
		}
		return strings.TrimRight(b.String(), "\n"), nil
	case "remove":
		if strings.TrimSpace(jobID) == "" {
			return "", errors.New("job_id is required")
		}
		if r.Cron.Remove(strings.TrimSpace(jobID)) {
			return "Removed job " + strings.TrimSpace(jobID), nil
		}
		return "Job not found: " + strings.TrimSpace(jobID), nil
	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

func shortName(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 30 {
		return s
	}
	return s[:30]
}
