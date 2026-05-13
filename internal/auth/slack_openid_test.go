package auth

import (
	"net/url"
	"strings"
	"testing"
)

func TestSlackAuthorizeURL_WorkspaceDomain(t *testing.T) {
	cfg := SlackOpenIDConfig{ClientID: "client-123"}
	cases := []struct {
		name       string
		teamID     string
		teamDomain string
		wantHost   string
		wantTeam   bool
	}{
		{
			name:       "with domain pins workspace via subdomain",
			teamID:     "T01ABC",
			teamDomain: "monarchbands",
			wantHost:   "monarchbands.slack.com",
			wantTeam:   true,
		},
		{
			name:       "no domain falls back to slack.com",
			teamID:     "T01ABC",
			teamDomain: "",
			wantHost:   "slack.com",
			wantTeam:   true,
		},
		{
			name:       "no team and no domain still produces a valid URL",
			teamID:     "",
			teamDomain: "",
			wantHost:   "slack.com",
			wantTeam:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SlackAuthorizeURL(cfg, "https://kit.example/oauth/callback", "state-xyz", tc.teamID, tc.teamDomain)
			u, err := url.Parse(got)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if u.Host != tc.wantHost {
				t.Errorf("host = %q, want %q (full url: %s)", u.Host, tc.wantHost, got)
			}
			if u.Path != "/openid/connect/authorize" {
				t.Errorf("path = %q, want /openid/connect/authorize", u.Path)
			}
			hasTeam := strings.Contains(got, "&team=")
			if hasTeam != tc.wantTeam {
				t.Errorf("team param present = %v, want %v (url: %s)", hasTeam, tc.wantTeam, got)
			}
		})
	}
}
