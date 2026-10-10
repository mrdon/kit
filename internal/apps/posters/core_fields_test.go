package posters

import (
	"strings"
	"testing"

	"github.com/mrdon/kit/internal/posterrender"
)

const inlinedPoster = `import React from "react";
function Template() { return null; }

// ---- Poster content. The template above draws it; edit either. ----
const content = {
  "eyebrow": "Thu Oct 15",
  "title": "Vinyl night",
  "details": []
};
const photos = [{"id":"p1","focusX":0.5,"focusY":0.5,"zoom":1}];
const ground = "paper";
const accent = "amber";

export default function Poster({ format }) {
  return <Template content={content} format={format} photos={photos} ground={ground} accent={accent} />;
}
`

func TestInlinedRoundTrip(t *testing.T) {
	_, photos, ground, ok := readInlined(inlinedPoster)
	if !ok || ground != "paper" || len(photos) != 1 || photos[0].ID != "p1" {
		t.Fatalf("readInlined = %v %q %v", photos, ground, ok)
	}
	photos[0].Zoom = 1.6
	out := writeInlined(inlinedPoster, photos, "ink")
	if !strings.Contains(out, `const ground = "ink";`) || !strings.Contains(out, `"zoom":1.6`) {
		t.Fatalf("writeInlined did not rewrite:\n%s", out)
	}
	if _, _, _, ok := readInlined("export default function Poster() { return null; }"); ok {
		t.Error("a poster without the block must not read as inlined")
	}
}

func TestApplyPhotoFields(t *testing.T) {
	zoom := 2.0
	photos, changes, err := applyPhotoFields(fieldsArg{Photo: "p2", Zoom: &zoom}, []posterrender.PhotoUse{{ID: "p1", Zoom: 1}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if photos[0].ID != "p2" || photos[0].Zoom != 2 || len(changes) != 2 {
		t.Errorf("photos = %+v changes = %v", photos, changes)
	}
	if _, _, err := applyPhotoFields(fieldsArg{Zoom: &zoom}, nil, nil); err == nil {
		t.Error("zoom with no photo must be refused")
	}
}

func TestBuiltins(t *testing.T) {
	ts, err := Builtins()
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 7 || ts[0].Key != "split" || ts[6].Key != "type" {
		keys := make([]string, 0, len(ts))
		for _, b := range ts {
			keys = append(keys, b.Key)
		}
		t.Fatalf("builtins = %v", keys)
	}
	strip := ts[5]
	if strip.Meta.Photos.Min != 3 || strip.Meta.Name != "Strip" || len(strip.Meta.Needs) != 2 {
		t.Errorf("strip meta = %+v", strip.Meta)
	}
}

func TestExtractJSON(t *testing.T) {
	got := extractJSON("Sure! ```json\n{\"a\": \"b}c\", \"n\": {\"x\": 1}}\n```")
	if got != `{"a": "b}c", "n": {"x": 1}}` {
		t.Errorf("extractJSON = %q", got)
	}
	if extractJSON("no json here") != "" {
		t.Error("expected empty")
	}
}
