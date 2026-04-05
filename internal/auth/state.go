package auth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// State encoding: we pack the MCP client's OAuth params into Slack's state parameter
// so we can recover them after Slack redirects back to us.

type oauthState struct {
	ClientID      string `json:"c"`
	RedirectURI   string `json:"r"`
	State         string `json:"s,omitempty"`
	CodeChallenge string `json:"p,omitempty"`
}

func encodeState(clientID, redirectURI, state, codeChallenge string) string {
	s := oauthState{
		ClientID:      clientID,
		RedirectURI:   redirectURI,
		State:         state,
		CodeChallenge: codeChallenge,
	}
	b, _ := json.Marshal(s)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeState(encoded string) (clientID, redirectURI, state, codeChallenge string, err error) {
	b, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", "", "", "", fmt.Errorf("decoding base64: %w", err)
	}
	var s oauthState
	if err := json.Unmarshal(b, &s); err != nil {
		return "", "", "", "", fmt.Errorf("unmarshaling state: %w", err)
	}
	return s.ClientID, s.RedirectURI, s.State, s.CodeChallenge, nil
}
