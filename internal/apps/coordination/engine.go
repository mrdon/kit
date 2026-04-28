package coordination

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/services/messenger"
)

// Engine runs the cron-driven sweep that drives outbound asks,
// nudges, re-engagements, deadline checks, and convergence detection.
type Engine struct {
	pool *pgxpool.Pool
	app  *CoordinationApp

	// Test hooks: set to override timing.
	now func() time.Time
}

func newEngine(pool *pgxpool.Pool, app *CoordinationApp) *Engine {
	return &Engine{pool: pool, app: app, now: time.Now}
}

// Tick is the cron entry point. Performs three scans:
//  1. Deadline expirations on active coordinations.
//  2. Nudge/re-engagement sends for participants whose next_nudge_at has
//     elapsed.
//  3. Convergence detection on coordinations where all responses are in.
func (e *Engine) Tick(ctx context.Context) error {
	now := e.now()

	if err := e.sweepDeadlines(ctx, now); err != nil {
		slog.Error("deadline sweep", "error", err)
	}
	if err := e.sweepReadyParticipants(ctx, now); err != nil {
		slog.Error("ready-participants sweep", "error", err)
	}
	if err := e.sweepConvergence(ctx); err != nil {
		slog.Error("convergence sweep", "error", err)
	}
	return nil
}

// sweepDeadlines surfaces a deadline decision card for any coordination
// whose deadline_at has passed and is still active.
func (e *Engine) sweepDeadlines(ctx context.Context, now time.Time) error {
	rows, err := e.pool.Query(ctx, `
		SELECT id, tenant_id FROM app_coordinations
		WHERE status = 'active' AND deadline_at IS NOT NULL AND deadline_at <= $1
	`, now)
	if err != nil {
		return err
	}
	defer rows.Close()
	type todo struct{ id, tenantID uuid.UUID }
	var coords []todo
	for rows.Next() {
		var t todo
		if err := rows.Scan(&t.id, &t.tenantID); err != nil {
			return err
		}
		coords = append(coords, t)
	}
	for _, t := range coords {
		coord, err := GetCoordination(ctx, e.pool, t.tenantID, t.id)
		if err != nil {
			slog.Error("loading coord for deadline", "error", err, "id", t.id)
			continue
		}
		if coord == nil {
			continue
		}
		// Phase 1: surface as abandonment (the deadline_handling card variant
		// distinction lives in the spec but for v1 we abandon when the
		// deadline lapses without convergence).
		if err := e.handleAbandonment(ctx, coord, "deadline reached"); err != nil {
			slog.Error("abandoning on deadline", "error", err, "id", coord.ID)
		}
	}
	return nil
}

// sweepReadyParticipants picks participants whose next_nudge_at has
// elapsed and processes them. Groups by coordination so an initial wave
// of N participants is batched into a single approval card.
func (e *Engine) sweepReadyParticipants(ctx context.Context, now time.Time) error {
	parts, err := ListReadyParticipants(ctx, e.pool, now)
	if err != nil {
		return fmt.Errorf("listing ready participants: %w", err)
	}
	if len(parts) == 0 {
		return nil
	}

	// Group by (tenant, coordination) so we can batch initial waves.
	type key struct {
		tenantID uuid.UUID
		coordID  uuid.UUID
	}
	groups := map[key][]Participant{}
	order := []key{}
	for _, p := range parts {
		k := key{p.TenantID, p.CoordinationID}
		if _, seen := groups[k]; !seen {
			order = append(order, k)
		}
		groups[k] = append(groups[k], p)
	}

	for _, k := range order {
		coord, err := GetCoordination(ctx, e.pool, k.tenantID, k.coordID)
		if err != nil || coord == nil {
			slog.Error("loading coord for sweep", "error", err, "id", k.coordID)
			continue
		}
		if coord.Status != StatusActive {
			continue
		}
		if err := e.processReadyGroup(ctx, coord, groups[k]); err != nil {
			slog.Error("processing ready group", "error", err, "coord", coord.ID)
		}
	}
	return nil
}

// processReadyGroup drafts messages for each ready participant in this
// coordination, then either sends them directly (if auto_approve) or
// surfaces a batched approval card.
func (e *Engine) processReadyGroup(ctx context.Context, coord *Coordination, ready []Participant) error {
	type draft struct {
		participant Participant
		body        string
		reason      string
	}
	drafts := make([]draft, 0, len(ready))

	for _, p := range ready {
		reason := pickReason(coord, &p)
		body, err := e.app.draftMessage(ctx, coord, &p, reason)
		if err != nil {
			slog.Error("drafting message", "error", err, "participant", p.ID)
			continue
		}
		drafts = append(drafts, draft{participant: p, body: body, reason: reason})
	}
	if len(drafts) == 0 {
		return nil
	}

	if !coord.Config.AutoApprove {
		// One approval card per draft (one per participant). Each card's
		// tool-args round-trip the drafted body so resolution sends the
		// exact text without re-drafting. Park each participant's
		// next_nudge_at while we wait on the organizer.
		for _, d := range drafts {
			parked := d.participant
			parked.NextNudgeAt = nil
			if err := UpdateParticipant(ctx, e.pool, &parked); err != nil {
				slog.Error("parking participant for approval", "error", err)
			}
			draft := approvalDraft{
				ParticipantID: d.participant.ID.String(),
				Body:          d.body,
			}
			if err := e.app.surfaceApprovalCard(ctx, coord, &d.participant, draft); err != nil {
				slog.Error("surfacing approval card", "error", err, "coord", coord.ID, "participant", d.participant.ID)
			}
		}
		return nil
	}

	for _, d := range drafts {
		if err := e.sendOne(ctx, coord, d.participant, d.body); err != nil {
			slog.Error("sending message", "error", err, "participant", d.participant.ID)
			continue
		}
	}
	return nil
}

// SendApprovedBatch is called from the approval-card resolve handler.
// It sends each pre-drafted body via Messenger and updates the
// participant rows (status/round/nudge schedule) the same way the
// auto-approve path does.
func (e *Engine) SendApprovedBatch(ctx context.Context, coord *Coordination, drafts []approvalDraft) error {
	for _, d := range drafts {
		pid, err := uuid.Parse(d.ParticipantID)
		if err != nil {
			slog.Error("invalid participant id in draft", "error", err, "id", d.ParticipantID)
			continue
		}
		p, err := GetParticipant(ctx, e.pool, coord.TenantID, pid)
		if err != nil {
			slog.Error("loading participant for approved send", "error", err, "id", pid)
			continue
		}
		if p == nil {
			continue
		}
		if err := e.sendOne(ctx, coord, *p, d.Body); err != nil {
			slog.Error("approved send", "error", err, "participant", pid)
		}
	}
	return nil
}

// sendOne posts via Messenger and updates the participant's state.
func (e *Engine) sendOne(ctx context.Context, coord *Coordination, p Participant, body string) error {
	if e.app.msg == nil {
		return errors.New("messenger not configured")
	}
	sent, err := e.app.msg.Send(ctx, messenger.SendRequest{
		TenantID:   coord.TenantID,
		Channel:    "slack",
		Recipient:  messenger.Recipient{SlackUserID: p.Identifier},
		Body:       body,
		Origin:     "coordination",
		OriginRef:  p.ID.String(),
		AwaitReply: true,
	})
	if err != nil {
		return fmt.Errorf("messenger.Send: %w", err)
	}

	// Update participant: bump round, set next_nudge_at, link session.
	round := Round{
		Round:      len(p.Rounds) + 1,
		AskedAt:    e.now(),
		AskedSlots: coord.Config.CandidateSlots,
	}
	p.Rounds = append(p.Rounds, round)
	if p.SessionID == nil {
		sid := sent.SessionID
		p.SessionID = &sid
	}
	p.Status = ParticipantContacted
	p.NudgeCount++
	next := e.now().Add(nudgeInterval(p.NudgeCount))
	p.NextNudgeAt = &next

	if p.NudgeCount > slackNudgeThreshold {
		p.Status = ParticipantTimedOut
		p.NextNudgeAt = nil
	}

	if err := UpdateParticipant(ctx, e.pool, &p); err != nil {
		return fmt.Errorf("updating participant: %w", err)
	}
	return nil
}

// sweepConvergence walks active coordinations looking for ones where
// all participants have responded with non-empty constraints and a
// candidate slot still works for everyone. When found, surfaces a
// convergence decision card to the organizer.
//
// Phase 1 stub: detects convergence and logs; decision card creation
// is wired in a subsequent commit.
func (e *Engine) sweepConvergence(ctx context.Context) error {
	rows, err := e.pool.Query(ctx, `
		SELECT DISTINCT tenant_id, id FROM app_coordinations
		WHERE status = 'active'
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type cell struct{ tenant, id uuid.UUID }
	var cells []cell
	for rows.Next() {
		var c cell
		if err := rows.Scan(&c.tenant, &c.id); err != nil {
			return err
		}
		cells = append(cells, c)
	}
	for _, c := range cells {
		coord, err := GetCoordination(ctx, e.pool, c.tenant, c.id)
		if err != nil || coord == nil {
			continue
		}
		parts, err := ListParticipants(ctx, e.pool, c.tenant, coord.ID)
		if err != nil {
			continue
		}
		done, slot := meetingIsComplete(coord, parts)
		if !done {
			continue
		}
		slog.Info("coordination converged",
			"coord", coord.ID, "slot", slot.Key())
		if err := UpdateCoordinationStatus(ctx, e.pool, c.tenant, coord.ID, StatusConverged, &CoordinationResult{ChosenSlot: &slot}); err != nil {
			slog.Error("converged status update", "error", err)
			continue
		}
		if err := e.app.surfaceConvergenceCard(ctx, coord, slot); err != nil {
			slog.Error("surfacing convergence card", "error", err, "coord", coord.ID)
		}
	}
	return nil
}

// NotifyCancel sends a brief cancellation note to each
// contacted/responded participant. Called from Service.Cancel.
func (e *Engine) NotifyCancel(ctx context.Context, coord *Coordination) error {
	parts, err := ListParticipants(ctx, e.pool, coord.TenantID, coord.ID)
	if err != nil {
		return err
	}
	for _, p := range parts {
		if p.Status != ParticipantContacted && p.Status != ParticipantResponded {
			continue
		}
		body := "Sorry — this scheduling has been cancelled. No further action needed from you."
		_, err := e.app.msg.Send(ctx, messenger.SendRequest{
			TenantID:   coord.TenantID,
			Channel:    "slack",
			Recipient:  messenger.Recipient{SlackUserID: p.Identifier},
			Body:       body,
			Origin:     "coordination",
			OriginRef:  p.ID.String(),
			AwaitReply: false,
		})
		if err != nil {
			slog.Error("cancel notify", "error", err, "participant", p.ID)
		}
	}
	return nil
}

// handleAbandonment surfaces an abandonment decision card to the
// organizer. The status only flips to abandoned once they tap "abandon"
// on the card — until then the coordination stays active so they can
// extend or change participants.
func (e *Engine) handleAbandonment(ctx context.Context, coord *Coordination, reason string) error {
	slog.Info("surfacing abandonment card", "coord", coord.ID, "reason", reason)
	if err := e.app.surfaceAbandonmentCard(ctx, coord, reason); err != nil {
		// If we can't surface the card, fall back to direct status flip.
		slog.Error("surfacing abandonment card", "error", err)
		return UpdateCoordinationStatus(ctx, e.pool, coord.TenantID, coord.ID, StatusAbandoned, nil)
	}
	return nil
}

// nudgeInterval returns the wait between nudges for the given count.
// 1st nudge: 24h; 2nd: 24h.
func nudgeInterval(nudgeCount int) time.Duration {
	switch nudgeCount {
	case 1:
		return 24 * time.Hour
	case 2:
		return 24 * time.Hour
	default:
		return 48 * time.Hour
	}
}

const slackNudgeThreshold = 2

// pickReason chooses a draftMessage reason based on participant state.
// "initial" for first contact, "nudge" for follow-ups while still
// contacted, "reengage_invalidated" for responded participants whose
// answer was invalidated by recompute.
func pickReason(coord *Coordination, p *Participant) string {
	switch p.Status {
	case ParticipantPending:
		return "initial"
	case ParticipantResponded:
		return "reengage_invalidated"
	default:
		return "nudge"
	}
}

// recomputeMeeting takes participants' current constraint sets and
// returns the surviving candidate slots plus the ids of participants
// whose stated availability no longer fits any surviving slot (and
// therefore need re-engagement).
func recomputeMeeting(coord *Coordination, parts []Participant) (candidates []Slot, invalidated []uuid.UUID) {
	candidates = make([]Slot, 0, len(coord.Config.CandidateSlots))
	for _, s := range coord.Config.CandidateSlots {
		ok := true
		for _, p := range parts {
			if p.Status == ParticipantDeclined || p.Status == ParticipantTimedOut {
				continue
			}
			if v, ok2 := p.Constraints.SlotVerdicts[s.Key()]; ok2 && v == VerdictReject {
				ok = false
				break
			}
		}
		if ok {
			candidates = append(candidates, s)
		}
	}
	invalidated = []uuid.UUID{}
	for _, p := range parts {
		if p.Status != ParticipantResponded {
			continue
		}
		// A responded participant is invalidated if NONE of the surviving
		// candidates is something they accepted.
		anyAccepted := false
		for _, s := range candidates {
			if v, ok2 := p.Constraints.SlotVerdicts[s.Key()]; ok2 && v == VerdictAccept {
				anyAccepted = true
				break
			}
		}
		if !anyAccepted && len(candidates) > 0 {
			invalidated = append(invalidated, p.ID)
		}
	}
	return candidates, invalidated
}

// meetingIsComplete returns true if no pending/contacted participants
// remain, all responded participants have an accept on at least one
// surviving slot, and at least one slot survives. Returns the
// preferred slot (first surviving slot the organizer would pick — for
// Phase 1 simply the first in candidate order).
func meetingIsComplete(coord *Coordination, parts []Participant) (bool, Slot) {
	if len(parts) == 0 {
		return false, Slot{}
	}
	hasResponses := false
	for _, p := range parts {
		switch p.Status {
		case ParticipantPending, ParticipantContacted:
			return false, Slot{}
		case ParticipantResponded:
			hasResponses = true
		}
	}
	if !hasResponses {
		return false, Slot{}
	}
	candidates, _ := recomputeMeeting(coord, parts)
	if len(candidates) == 0 {
		return false, Slot{}
	}
	// Need at least one slot accepted by every responded participant.
	for _, s := range candidates {
		ok := true
		for _, p := range parts {
			if p.Status != ParticipantResponded {
				continue
			}
			v, present := p.Constraints.SlotVerdicts[s.Key()]
			if !present || v != VerdictAccept {
				ok = false
				break
			}
		}
		if ok {
			return true, s
		}
	}
	return false, Slot{}
}
