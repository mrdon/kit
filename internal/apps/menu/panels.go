package menu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Scoped panel writes.
//
// set_menu_board replaces the whole document, which is the right shape for a
// person setting a menu up and the wrong one for anything that runs
// unattended. The tap list can survive a bad write -- it has an upstream, and
// the next refresh restores it -- but the wordmark, the footer rules and the
// panels do not, so a job that rewrote the document nightly would keep all of
// them in its blast radius forever in order to edit one paragraph.
//
// This path can only ever touch `panels`. The swap is done in SQL rather than
// by reading the document, editing it and writing it back, so a tap sync
// landing between the read and the write cannot be clobbered by a stale copy
// of the taps we never meant to write in the first place.

// ParsePanels decodes and validates a panels array on its own.
//
// Unknown fields are rejected here for the same reason ParseBoard rejects
// them: a typo'd key should fail the call rather than quietly render a panel
// missing half its content on a wall nobody is watching.
//
// Panel validation is the same pass the whole-document path runs, so a panel
// that would be refused inside a board is refused here too. What is NOT
// checked is anything about the taps -- those are not being written, and
// requiring a caller to hold a valid tap list in order to edit a panel is the
// coupling this function exists to remove.
func ParsePanels(raw []byte) ([]Panel, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var panels []Panel
	if err := dec.Decode(&panels); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPayloadInvalid, err)
	}
	for i := range panels {
		if err := panels[i].validate(i); err != nil {
			return nil, err
		}
	}
	return panels, nil
}

// SavePanels replaces the board's panels, leaving every other key in the
// document untouched.
//
// updated_at moves, which is deliberate: it is half of the version stamp the
// wall polls, so a panel change that left it alone would be correct in the
// database and invisible on the screen until the next time a beer changed.
func SavePanels(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, panels []byte) error {
	const q = `UPDATE app_menu_boards
	           SET payload = jsonb_set(payload, '{panels}', $2::jsonb, true),
	               updated_at = NOW()
	           WHERE tenant_id = $1`
	tag, err := pool.Exec(ctx, q, tenantID, panels)
	if err != nil {
		return fmt.Errorf("saving menu panels: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// setPanelsArgs is the shared input shape for set_menu_panels.
//
// RawMessage rather than string because the argument arrives both ways: the
// schema asks for a JSON string, which is what an agent sends, while several
// MCP clients send the array itself. Normalizing is panelsJSON's job.
type setPanelsArgs struct {
	Panels json.RawMessage `json:"panels"`
}

// panelsJSON normalizes the argument into the JSON array bytes to validate.
func panelsJSON(raw json.RawMessage) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, errors.New("panels is required")
	}
	// A JSON string carrying the array, rather than the array itself.
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nil, fmt.Errorf("parsing panels: %w", err)
		}
		return []byte(s), nil
	}
	return trimmed, nil
}

// savePanels is the handler both surfaces call.
func savePanels(ctx context.Context, pool *pgxpool.Pool, a *App, tenantID uuid.UUID, args setPanelsArgs) (string, error) {
	raw, err := panelsJSON(args.Panels)
	if err != nil {
		return "", err
	}
	panels, err := ParsePanels(raw)
	if err != nil {
		return "", err
	}
	// Store the round-tripped form rather than the caller's bytes, so what
	// lands in the document is exactly what the type models -- absent
	// optional keys stay absent instead of being written as empty strings.
	clean, err := json.Marshal(panels)
	if err != nil {
		return "", fmt.Errorf("encoding panels: %w", err)
	}
	if err := SavePanels(ctx, pool, tenantID, clean); err != nil {
		return "", err
	}
	url, err := a.publicURL(ctx, pool, tenantID)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Panels set — %s. The tap list and venue chrome were not touched.\n\nShowing at: %s",
		panelSummary(panels), url), nil
}

// panelSummary describes what was written, by kind, so a caller reading the
// result can tell a panel silently dropped from one it meant to remove.
func panelSummary(panels []Panel) string {
	if len(panels) == 0 {
		return "no panels, so the rail is empty"
	}
	counts := map[string]int{}
	var order []string
	for _, p := range panels {
		if counts[p.Kind] == 0 {
			order = append(order, p.Kind)
		}
		counts[p.Kind]++
	}
	parts := make([]string, 0, len(order))
	for _, k := range order {
		if counts[k] == 1 {
			parts = append(parts, "1 "+k)
			continue
		}
		parts = append(parts, fmt.Sprintf("%d %s", counts[k], k))
	}
	noun := "panels"
	if len(panels) == 1 {
		noun = "panel"
	}
	return fmt.Sprintf("%d %s (%s)", len(panels), noun, joinWords(parts))
}

func joinWords(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

// describePanels renders the rail as the JSON set_menu_panels accepts.
//
// The exact document, not a summary. Anything editing one panel has to send
// the whole array back, so the only useful answer to "what is on the rail" is
// one that can be modified and returned verbatim -- a prose description would
// leave a caller reconstructing the panels it means to keep, which is how a
// nightly job quietly drops the one panel nobody asked it to touch.
func describePanels(panels []Panel) string {
	if len(panels) == 0 {
		return "  the rail is empty — no panels\n"
	}
	pretty, err := json.MarshalIndent(panels, "  ", "  ")
	if err != nil {
		// The panels came out of a document that already parsed, so this is
		// unreachable short of a broken encoder; say so rather than pretend
		// the rail is empty.
		return fmt.Sprintf("  (could not render panels: %v)\n", err)
	}
	return "\n  Panels, as set_menu_panels takes them:\n  " + string(pretty) + "\n"
}
