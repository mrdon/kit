// Package integrations configures external service connections (API keys,
// OAuth tokens, etc.) without ever exposing secrets to the LLM. An LLM
// tool mints a short-lived signed URL and the user submits the secret via
// a Kit-hosted web form.
package integrations

import (
	"errors"
	"fmt"
	"sync"
)

// Scope tells the app who owns a configured integration.
type Scope string

const (
	// ScopeUser means each user configures their own (e.g. personal email).
	ScopeUser Scope = "user"
	// ScopeTenant means one shared connection per workspace (e.g. a shared
	// Stripe account). Admin-only.
	ScopeTenant Scope = "tenant"
)

// Field targets for FieldSpec — each value routes to a specific column
// (or into the non-secret config JSONB).
const (
	TargetConfig         = "config"
	TargetUsername       = "username"
	TargetPrimaryToken   = "primary_token"
	TargetSecondaryToken = "secondary_token"
)

// FieldSpec describes one form field in a TypeSpec.
type FieldSpec struct {
	Name      string // form key; also key in config JSONB when Target == TargetConfig
	Label     string // shown on the form
	InputType string // "text" | "password" | "url"
	Target    string // TargetConfig (default when empty) | TargetUsername | TargetPrimary/SecondaryToken
	Required  bool
	Help      string // optional hint text shown under the input
}

// TypeSpec declares a registrable (provider, auth_type) pair.
type TypeSpec struct {
	Provider    string // "github", "email", "stripe"
	AuthType    string // "api_key", "oauth2", "app_password", "imap_smtp"
	DisplayName string // "GitHub (Personal Access Token)"
	Description string
	Scope       Scope
	Fields      []FieldSpec
}

// effectiveTarget resolves the FieldSpec.Target with a safe default.
func (f FieldSpec) effectiveTarget() string {
	if f.Target == "" {
		return TargetConfig
	}
	return f.Target
}

// IsSecret reports whether the field's value should be encrypted on
// submit (i.e. routes to a token column).
func (f FieldSpec) IsSecret() bool {
	t := f.effectiveTarget()
	return t == TargetPrimaryToken || t == TargetSecondaryToken
}

func typeKey(provider, authType string) string {
	return provider + ":" + authType
}

// Key returns the canonical string form of a TypeSpec's identity.
func (s TypeSpec) Key() string { return typeKey(s.Provider, s.AuthType) }

var (
	typeRegistryMu sync.RWMutex
	typeRegistry   = map[string]TypeSpec{}
)

// RegisterTypeSpec adds a TypeSpec to the registry. Panics if the spec is
// malformed — misconfiguration should fail fast at startup. Duplicate keys
// also panic (second registration of the same (provider, auth_type) is
// always a bug, never a legitimate re-init).
func RegisterTypeSpec(spec TypeSpec) {
	if err := validateTypeSpec(spec); err != nil {
		panic(fmt.Sprintf("integrations: invalid TypeSpec %s: %v", spec.Key(), err))
	}
	typeRegistryMu.Lock()
	defer typeRegistryMu.Unlock()
	key := spec.Key()
	if _, exists := typeRegistry[key]; exists {
		panic(fmt.Sprintf("integrations: TypeSpec %s already registered", key))
	}
	typeRegistry[key] = spec
}

// LookupTypeSpec returns the registered spec for a (provider, auth_type),
// or (TypeSpec{}, false) if no spec is registered.
func LookupTypeSpec(provider, authType string) (TypeSpec, bool) {
	typeRegistryMu.RLock()
	defer typeRegistryMu.RUnlock()
	spec, ok := typeRegistry[typeKey(provider, authType)]
	return spec, ok
}

// allTypeSpecs returns a snapshot of the registry, sorted by key. Used by
// list_integration_types and tests.
func allTypeSpecs() []TypeSpec {
	typeRegistryMu.RLock()
	defer typeRegistryMu.RUnlock()
	out := make([]TypeSpec, 0, len(typeRegistry))
	for _, spec := range typeRegistry {
		out = append(out, spec)
	}
	return out
}

// resetRegistryForTest clears the registry. Test-only; the production
// code path never unregisters.
func resetRegistryForTest() {
	typeRegistryMu.Lock()
	defer typeRegistryMu.Unlock()
	typeRegistry = map[string]TypeSpec{}
}

func validateTypeSpec(spec TypeSpec) error {
	if spec.Provider == "" {
		return errors.New("provider is required")
	}
	if spec.AuthType == "" {
		return errors.New("auth_type is required")
	}
	if spec.DisplayName == "" {
		return errors.New("display_name is required")
	}
	if spec.Scope != ScopeUser && spec.Scope != ScopeTenant {
		return fmt.Errorf("scope must be %q or %q, got %q", ScopeUser, ScopeTenant, spec.Scope)
	}
	seenTargets := map[string]bool{}
	seenNames := map[string]bool{}
	for _, f := range spec.Fields {
		if f.Name == "" {
			return errors.New("field name is required")
		}
		if seenNames[f.Name] {
			return fmt.Errorf("duplicate field name %q", f.Name)
		}
		seenNames[f.Name] = true

		target := f.effectiveTarget()
		switch target {
		case TargetConfig:
			// Multiple config fields are allowed.
		case TargetUsername, TargetPrimaryToken, TargetSecondaryToken:
			if seenTargets[target] {
				return fmt.Errorf("duplicate field target %q", target)
			}
			seenTargets[target] = true
		default:
			return fmt.Errorf("field %q has invalid target %q", f.Name, target)
		}
	}
	return nil
}
