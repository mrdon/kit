package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Policy is a scheduled job's capability manifest: what the agent running
// inside it is allowed to do. It is persisted under the "policy" key of
// Job.Config (JSONB). See the creating-jobs builtin skill for design
// guidance.
//
// A nil *Policy means "no restrictions" — interactive Slack callers and
// existing jobs without a policy keep today's behaviour.
type Policy struct {
	// AllowedTools narrows the registry to this set (plus the always-allowed
	// infrastructure tools in InfrastructureTools). Uses a pointer to a
	// slice so JSON round-trips preserve the three distinct states:
	//   nil                    → no restriction
	//   pointer to empty slice → allow nothing except infrastructure
	//   pointer to non-empty   → these + infrastructure
	AllowedTools *[]string `json:"allowed_tools,omitempty"`

	// ForceGate names tools that always route through an approval card,
	// even if the agent did not set require_approval: true. Wins over
	// Def.DenyCallerGate — that flag suppresses the agent's own opt-in,
	// not a job-level contract.
	ForceGate []string `json:"force_gate,omitempty"`

	// PinnedArgs pins per-tool argument keys to fixed values. The merge
	// happens before the gate check and before the tool handler runs,
	// so the approval card preview shows the pinned (true) values and
	// the handler receives them. Keys not in the map pass through.
	PinnedArgs map[string]map[string]any `json:"pinned_args,omitempty"`
}

// InfrastructureTools are agent-infrastructure tools that the allow-list
// never filters out. Excluding them would deadlock job runs (e.g. the
// agent can't load a skill it needs to answer correctly). Keep this set
// tight — it grants implicit capability to every policy-constrained run.
var InfrastructureTools = map[string]bool{
	"load_skill":             true,
	"load_skill_file":        true,
	"resolve_decision":       true,
	"revise_decision_option": true,
	"send_slack_message":     true,
}

// IsAllowed reports whether the policy permits a tool call by name. A nil
// policy is fully permissive. Infrastructure tools are always allowed.
func (p *Policy) IsAllowed(toolName string) bool {
	if p == nil || p.AllowedTools == nil {
		return true
	}
	if InfrastructureTools[toolName] {
		return true
	}
	return slices.Contains(*p.AllowedTools, toolName)
}

// ForcesGate reports whether the policy demands an approval card for a
// tool call by name, independent of Def.DefaultPolicy and DenyCallerGate.
func (p *Policy) ForcesGate(toolName string) bool {
	if p == nil {
		return false
	}
	return slices.Contains(p.ForceGate, toolName)
}

// PinnedFor returns the pinned argument map for a tool, or nil if none.
// The returned map must not be mutated by callers.
func (p *Policy) PinnedFor(toolName string) map[string]any {
	if p == nil || p.PinnedArgs == nil {
		return nil
	}
	return p.PinnedArgs[toolName]
}

// ParseConfigPolicy extracts the Policy (if any) from a job's Config
// JSONB payload. Returns (nil, nil) for empty config or missing policy
// key so callers can treat "no policy" uniformly.
func ParseConfigPolicy(cfg []byte) (*Policy, error) {
	if len(cfg) == 0 {
		return nil, nil //nolint:nilnil // absence of policy is not an error
	}
	var wrapper struct {
		Policy *Policy `json:"policy"`
	}
	if err := json.Unmarshal(cfg, &wrapper); err != nil {
		return nil, fmt.Errorf("parsing job config: %w", err)
	}
	return wrapper.Policy, nil
}

// SetConfigPolicy returns a new Config JSONB payload with the policy key
// replaced (or deleted, if p is nil). Other keys in the existing config
// (e.g. builder_script's script_id/fn_name) are preserved.
func SetConfigPolicy(cfg []byte, p *Policy) ([]byte, error) {
	m := map[string]json.RawMessage{}
	if len(cfg) > 0 {
		if err := json.Unmarshal(cfg, &m); err != nil {
			return nil, fmt.Errorf("parsing existing job config: %w", err)
		}
	}
	if p == nil {
		delete(m, "policy")
	} else {
		b, err := json.Marshal(p)
		if err != nil {
			return nil, fmt.Errorf("marshalling policy: %w", err)
		}
		m["policy"] = b
	}
	if len(m) == 0 {
		return nil, nil
	}
	return json.Marshal(m)
}

// UpdateJobPolicy writes a job's policy in-place by replacing the
// "policy" key in its Config JSONB. Other keys (e.g. builder_script
// fields) are preserved. Builtin jobs cannot be updated — matches
// the constraint on UpdateJobDescription.
func UpdateJobPolicy(ctx context.Context, pool *pgxpool.Pool, tenantID, jobID uuid.UUID, p *Policy) error {
	t, err := GetJob(ctx, pool, tenantID, jobID)
	if err != nil {
		return err
	}
	if t == nil {
		return errors.New("job not found")
	}
	if t.JobType == JobTypeBuiltin {
		return errors.New("builtin jobs cannot be updated")
	}
	newConfig, err := SetConfigPolicy(t.Config, p)
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `
		UPDATE jobs SET config = $3 WHERE tenant_id = $1 AND id = $2
	`, tenantID, jobID, newConfig)
	if err != nil {
		return fmt.Errorf("updating job policy: %w", err)
	}
	return nil
}

// MergePinnedArgs merges pinned values over an input JSON object,
// producing a new json.RawMessage. Never mutates input. Keys in pinned
// overwrite matching top-level keys in input. Returns (input, false) if
// pinned is empty. Returns (nil, error) if input is not a JSON object.
func MergePinnedArgs(input json.RawMessage, pinned map[string]any) (json.RawMessage, bool, error) {
	if len(pinned) == 0 {
		return input, false, nil
	}
	obj := map[string]json.RawMessage{}
	if len(input) > 0 {
		if err := json.Unmarshal(input, &obj); err != nil {
			return nil, false, fmt.Errorf("merging pinned args: input is not a JSON object: %w", err)
		}
	}
	changed := false
	for k, v := range pinned {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, false, fmt.Errorf("marshalling pinned value for %q: %w", k, err)
		}
		existing, hadKey := obj[k]
		if !hadKey || string(existing) != string(b) {
			changed = true
		}
		obj[k] = b
	}
	if !changed {
		return input, false, nil
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return nil, false, fmt.Errorf("remarshalling merged args: %w", err)
	}
	return out, true, nil
}
