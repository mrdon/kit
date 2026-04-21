package integrations

import (
	"strings"
	"testing"
)

func TestValidateTypeSpec(t *testing.T) {
	okFields := []FieldSpec{
		{Name: "api_key", Target: TargetPrimaryToken, Required: true},
		{Name: "account_id", Target: TargetUsername, Required: true},
		{Name: "workspace_url"}, // defaults to config
	}
	okSpec := TypeSpec{
		Provider:    "test",
		AuthType:    "ok",
		DisplayName: "Test OK",
		Scope:       ScopeUser,
		Fields:      okFields,
	}
	if err := validateTypeSpec(okSpec); err != nil {
		t.Errorf("valid spec rejected: %v", err)
	}

	cases := []struct {
		name string
		spec TypeSpec
		want string
	}{
		{
			"missing provider",
			TypeSpec{AuthType: "x", DisplayName: "d", Scope: ScopeUser},
			"provider",
		},
		{
			"missing auth_type",
			TypeSpec{Provider: "x", DisplayName: "d", Scope: ScopeUser},
			"auth_type",
		},
		{
			"missing display name",
			TypeSpec{Provider: "x", AuthType: "y", Scope: ScopeUser},
			"display_name",
		},
		{
			"invalid scope",
			TypeSpec{Provider: "x", AuthType: "y", DisplayName: "d", Scope: Scope("bogus")},
			"scope must be",
		},
		{
			"duplicate field name",
			TypeSpec{Provider: "x", AuthType: "y", DisplayName: "d", Scope: ScopeUser,
				Fields: []FieldSpec{{Name: "a"}, {Name: "a"}}},
			"duplicate field name",
		},
		{
			"duplicate username target",
			TypeSpec{Provider: "x", AuthType: "y", DisplayName: "d", Scope: ScopeUser,
				Fields: []FieldSpec{
					{Name: "u1", Target: TargetUsername},
					{Name: "u2", Target: TargetUsername},
				}},
			"duplicate field target",
		},
		{
			"invalid target",
			TypeSpec{Provider: "x", AuthType: "y", DisplayName: "d", Scope: ScopeUser,
				Fields: []FieldSpec{{Name: "a", Target: "nope"}}},
			"invalid target",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateTypeSpec(c.spec)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not contain %q", err.Error(), c.want)
			}
		})
	}
}

func TestRegisterLookupDuplicate(t *testing.T) {
	t.Cleanup(resetRegistryForTest)
	resetRegistryForTest()

	RegisterTypeSpec(TypeSpec{
		Provider: "demo", AuthType: "k", DisplayName: "Demo", Scope: ScopeUser,
		Fields: []FieldSpec{{Name: "api_key", Target: TargetPrimaryToken, Required: true}},
	})

	got, ok := LookupTypeSpec("demo", "k")
	if !ok {
		t.Fatal("lookup failed after register")
	}
	if got.DisplayName != "Demo" {
		t.Errorf("lookup returned wrong spec: %+v", got)
	}

	// Second register panics.
	defer func() {
		if r := recover(); r == nil {
			t.Error("duplicate register should panic")
		}
	}()
	RegisterTypeSpec(TypeSpec{
		Provider: "demo", AuthType: "k", DisplayName: "Dup", Scope: ScopeUser,
	})
}

func TestFieldSpecIsSecret(t *testing.T) {
	cases := []struct {
		f    FieldSpec
		want bool
	}{
		{FieldSpec{Target: TargetPrimaryToken}, true},
		{FieldSpec{Target: TargetSecondaryToken}, true},
		{FieldSpec{Target: TargetUsername}, false},
		{FieldSpec{Target: TargetConfig}, false},
		{FieldSpec{}, false}, // default = config
	}
	for _, c := range cases {
		if got := c.f.IsSecret(); got != c.want {
			t.Errorf("IsSecret() on %+v = %v, want %v", c.f, got, c.want)
		}
	}
}
