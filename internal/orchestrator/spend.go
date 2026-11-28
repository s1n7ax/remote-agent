package orchestrator

import (
	"context"
	"fmt"
	"strconv"

	"github.com/s1n7ax/remote-agent/internal/domain"
)

// Settings keys for OpenRouter spend caps (USD; 0 means no cap).
const (
	SettingDailyCap       = "openrouter_daily_cap_usd"
	SettingTicketCap      = "openrouter_ticket_cap_usd"
	settingDailyRaisePref = "openrouter_daily_raise_"
)

// capRaise is how much one "raise" answer adds to a cap.
const capRaise = 5.0

func (o *Orchestrator) floatSetting(ctx context.Context, key string) float64 {
	v, ok, err := o.st.Setting(ctx, key)
	if err != nil || !ok {
		return 0
	}
	f, _ := strconv.ParseFloat(v, 64)
	return f
}

func (o *Orchestrator) today() string { return o.now().Format("2006-01-02") }

// dailyCap is the configured daily cap plus raises granted today.
func (o *Orchestrator) dailyCap(ctx context.Context) float64 {
	base := o.floatSetting(ctx, SettingDailyCap)
	if base == 0 {
		return 0
	}
	return base + o.floatSetting(ctx, settingDailyRaisePref+o.today())
}

// DefaultTicketCap returns the per-ticket cap new tickets get.
func (o *Orchestrator) DefaultTicketCap(ctx context.Context) float64 {
	return o.floatSetting(ctx, SettingTicketCap)
}

// overCap returns why a ticket may not spend more, or "".
func (o *Orchestrator) overCap(ctx context.Context, t domain.Ticket) string {
	if t.SpendCapUSD > 0 && t.SpendUSD >= t.SpendCapUSD {
		return fmt.Sprintf("this ticket reached its $%.2f spend cap", t.SpendCapUSD)
	}
	if limit := o.dailyCap(ctx); limit > 0 {
		spent, err := o.st.Spend(ctx, o.today())
		if err == nil && spent >= limit {
			return fmt.Sprintf("today's OpenRouter spend reached the $%.2f daily cap", limit)
		}
	}
	return ""
}

// addCost books a pi run's incremental cost and stops the run at a cap.
func (o *Orchestrator) addCost(ctx context.Context, ar *activeRun, usd float64) {
	ar.run.CostUSD += usd
	if _, err := o.st.AddSpend(ctx, o.today(), usd); err != nil {
		o.log.Error("book spend", "err", err)
	}
	t, err := o.mutate(ctx, ar.ticket.ID, func(t *domain.Ticket) { t.SpendUSD += usd })
	if err != nil {
		o.log.Error("book ticket spend", "err", err)
		return
	}
	ar.ticket.SpendUSD = t.SpendUSD
	if reason := o.overCap(ctx, t); reason != "" {
		ar.mu.Lock()
		already := ar.capped != ""
		ar.capped = reason
		ar.mu.Unlock()
		if !already {
			o.event(ctx, ar, domain.EventSystem, "spend cap: "+reason)
			_ = ar.sess.Interrupt(ctx)
			_ = ar.sess.Stop()
		}
	}
}

// askSpendCap posts the spend-cap question and parks the ticket.
func (o *Orchestrator) askSpendCap(ctx context.Context, t domain.Ticket, reason string) error {
	if cur, err := o.st.Ticket(ctx, t.ID); err != nil || cur.WontDo() {
		return err // closed as won't do: no more questions
	}
	tree := domain.QuestionTree{
		Title: "Spend cap reached",
		Intro: "The run paused because " + reason + ".",
		Questions: []domain.Question{{
			ID: "cap", Kind: domain.QuestionSingle, Title: "How should this ticket continue?",
			Options: []domain.Option{
				{ID: "raise", Label: fmt.Sprintf("Raise the cap by $%.0f", capRaise), Recommended: true,
					Description: "Adds to whichever cap was hit (this ticket's or today's) and continues the run."},
				{ID: "stop", Label: "Stop here", Description: "The ticket goes back to the backlog."},
			},
			AllowOther: new(bool),
		}},
	}
	if _, err := o.st.AddRound(ctx, domain.Round{TicketID: t.ID, Purpose: domain.PurposeSpendCap, Stage: t.Stage, Tree: tree}); err != nil {
		return err
	}
	t, err := o.mutate(ctx, t.ID, func(c *domain.Ticket) {
		c.Status = domain.StatusNeedsInput
		c.StatusNote = reason
	})
	if err != nil {
		return err
	}
	o.notify(ctx, domain.NotifyPaused, t, "Spend cap reached: "+reason)
	return nil
}

// applySpendAnswer acts on the spend-cap question; it reports whether the
// ticket should continue.
func (o *Orchestrator) applySpendAnswer(ctx context.Context, t *domain.Ticket, a domain.Answer) (bool, error) {
	if len(a.OptionIDs) == 0 || a.OptionIDs[0] != "raise" {
		return false, nil
	}
	if t.SpendCapUSD > 0 && t.SpendUSD >= t.SpendCapUSD {
		t.SpendCapUSD += capRaise
	}
	if limit := o.dailyCap(ctx); limit > 0 {
		spent, _ := o.st.Spend(ctx, o.today())
		if spent >= limit {
			key := settingDailyRaisePref + o.today()
			raised := o.floatSetting(ctx, key) + capRaise
			if err := o.st.SetSetting(ctx, key, strconv.FormatFloat(raised, 'f', 2, 64)); err != nil {
				return false, err
			}
		}
	}
	return true, nil
}
