package orchestrator

import (
	"context"
	"fmt"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/s1n7ax/remote-agent/internal/domain"
)

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// NextRun returns the next time a cron expression fires after from.
func NextRun(expr string, from time.Time) (time.Time, error) {
	sched, err := cronParser.Parse(expr)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid cron %q: %w", expr, err)
	}
	return sched.Next(from).UTC(), nil
}

// fireSchedules creates a job ticket for every due schedule.
func (o *Orchestrator) fireSchedules(ctx context.Context) error {
	scs, err := o.st.Schedules(ctx, 0)
	if err != nil {
		return err
	}
	now := o.now()
	for _, sc := range scs {
		if !sc.Enabled {
			continue
		}
		if sc.NextRunAt == nil {
			next, err := NextRun(sc.Cron, now)
			if err != nil {
				o.log.Warn("schedule has a bad cron", "schedule", sc.ID, "err", err)
				continue
			}
			sc.NextRunAt = &next
			if err := o.st.UpdateSchedule(ctx, sc); err != nil {
				return err
			}
			continue
		}
		if sc.NextRunAt.After(now) {
			continue
		}
		if _, err := o.st.CreateTicket(ctx, domain.Ticket{
			ProjectID: sc.ProjectID, Kind: domain.KindJob, Title: sc.Name + " — " + now.Local().Format("Jan 2 15:04"),
			Idea: sc.Prompt, JobPrompt: sc.Prompt, Status: domain.StatusReady, Stage: domain.StageJob,
			Agent: sc.Agent, Model: sc.Model, Effort: sc.Effort, SpendCapUSD: o.DefaultTicketCap(ctx),
		}); err != nil {
			return err
		}
		next, err := NextRun(sc.Cron, now)
		if err != nil {
			return err
		}
		sc.LastRunAt = &now
		sc.NextRunAt = &next
		if err := o.st.UpdateSchedule(ctx, sc); err != nil {
			return err
		}
		o.bus.Publish(Message{Type: "board", ProjectID: sc.ProjectID})
	}
	return nil
}
