package email

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSendArgsUnmarshalTolerantRecipients(t *testing.T) {
	cases := []struct {
		name string
		json string
		want SendArgs
	}{
		{
			name: "to as array",
			json: `{"to":["a@example.com","b@example.com"],"subject":"s","body":"b"}`,
			want: SendArgs{
				To:      stringList{"a@example.com", "b@example.com"},
				Subject: "s",
				Body:    "b",
			},
		},
		{
			name: "to as bare string",
			json: `{"to":"a@example.com","subject":"s","body":"b"}`,
			want: SendArgs{
				To:      stringList{"a@example.com"},
				Subject: "s",
				Body:    "b",
			},
		},
		{
			name: "cc and bcc as bare strings",
			json: `{"to":["a@example.com"],"cc":"c@example.com","bcc":"d@example.com","subject":"s","body":"b"}`,
			want: SendArgs{
				To:      stringList{"a@example.com"},
				Cc:      stringList{"c@example.com"},
				Bcc:     stringList{"d@example.com"},
				Subject: "s",
				Body:    "b",
			},
		},
		{
			name: "references as bare string",
			json: `{"to":["a@example.com"],"subject":"s","body":"b","references":"<x@y>"}`,
			want: SendArgs{
				To:         stringList{"a@example.com"},
				Subject:    "s",
				Body:       "b",
				References: stringList{"<x@y>"},
			},
		},
		{
			name: "null to",
			json: `{"to":null,"subject":"s","body":"b"}`,
			want: SendArgs{Subject: "s", Body: "b"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got SendArgs
			if err := json.Unmarshal([]byte(tc.json), &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestSendArgsUnmarshalRejectsNonString(t *testing.T) {
	var got SendArgs
	err := json.Unmarshal([]byte(`{"to":123,"subject":"s","body":"b"}`), &got)
	if err == nil {
		t.Fatalf("expected error for numeric to, got nil")
	}
}
