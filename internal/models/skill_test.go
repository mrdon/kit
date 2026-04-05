package models

import "testing"

func TestSlugifyName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Tap Room Policies", "tap-room-policies"},
		{"employee-handbook", "employee-handbook"},
		{"WiFi & Network Info", "wifi-network-info"},
		{"  Spaces  Everywhere  ", "spaces-everywhere"},
		{"ALLCAPS", "allcaps"},
		{"already-valid", "already-valid"},
		{"has---multiple---dashes", "has-multiple-dashes"},
		{"trailing-", "trailing"},
		{"-leading", "leading"},
		{"special!@#chars$%^here", "special-chars-here"},
		{"a", "a"},
		{"", ""},
		{"This Is A Really Long Name That Exceeds The Sixty Four Character Limit For Skill Names", "this-is-a-really-long-name-that-exceeds-the-sixty-four-character"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := SlugifyName(tt.input)
			if got != tt.want {
				t.Errorf("SlugifyName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidateSkillName(t *testing.T) {
	valid := []string{
		"tap-room-policies",
		"employee-handbook",
		"a",
		"abc123",
		"my-skill-2",
	}
	for _, name := range valid {
		if err := ValidateSkillName(name); err != nil {
			t.Errorf("ValidateSkillName(%q) unexpected error: %v", name, err)
		}
	}

	invalid := []string{
		"",
		"Has Spaces",
		"HAS-CAPS",
		"trailing-",
		"-leading",
		"double--hyphen",
		"special!char",
		// 65 chars
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	for _, name := range invalid {
		if err := ValidateSkillName(name); err == nil {
			t.Errorf("ValidateSkillName(%q) expected error, got nil", name)
		}
	}
}

func TestParseSKILLMD(t *testing.T) {
	t.Run("full frontmatter", func(t *testing.T) {
		raw := "---\nname: tap-room-policies\ndescription: Policies for the tap room\n---\n\n# Policies\n\nNo tabs over $100."
		name, desc, content, err := ParseSKILLMD(raw)
		if err != nil {
			t.Fatal(err)
		}
		if name != "tap-room-policies" {
			t.Errorf("name = %q, want %q", name, "tap-room-policies")
		}
		if desc != "Policies for the tap room" {
			t.Errorf("description = %q, want %q", desc, "Policies for the tap room")
		}
		if content != "# Policies\n\nNo tabs over $100." {
			t.Errorf("content = %q", content)
		}
	})

	t.Run("no frontmatter", func(t *testing.T) {
		raw := "# Just Content\n\nNo frontmatter here."
		name, desc, content, err := ParseSKILLMD(raw)
		if err != nil {
			t.Fatal(err)
		}
		if name != "" {
			t.Errorf("name = %q, want empty", name)
		}
		if desc != "" {
			t.Errorf("description = %q, want empty", desc)
		}
		if content != raw {
			t.Errorf("content should be raw input")
		}
	})

	t.Run("empty", func(t *testing.T) {
		_, _, content, err := ParseSKILLMD("")
		if err != nil {
			t.Fatal(err)
		}
		if content != "" {
			t.Errorf("content = %q, want empty", content)
		}
	})
}

func TestToSKILLMD(t *testing.T) {
	s := &Skill{
		Name:        "tap-room-policies",
		Description: "Policies for the tap room",
		Content:     "# Policies\n\nNo tabs over $100.",
	}

	got := s.ToSKILLMD()
	want := "---\nname: tap-room-policies\ndescription: Policies for the tap room\n---\n\n# Policies\n\nNo tabs over $100."
	if got != want {
		t.Errorf("ToSKILLMD() =\n%s\n\nwant:\n%s", got, want)
	}

	// Round-trip
	name, desc, content, err := ParseSKILLMD(got)
	if err != nil {
		t.Fatal(err)
	}
	if name != s.Name || desc != s.Description || content != s.Content {
		t.Errorf("round-trip failed: name=%q desc=%q content=%q", name, desc, content)
	}
}
