package posterrender

import (
	"context"
	"fmt"
)

// Fake is a Renderer for tests: it records requests and answers from
// simple hooks. Every method has a default that succeeds with a tiny PNG,
// so a test overrides only the call it is about.
type Fake struct {
	RenderCalls  []RenderRequest
	OptionsCalls []OptionsRequest
	InspectCalls []ImageRef

	RenderFn        func(req RenderRequest) (*RenderResponse, error)
	OptionsFn       func(req OptionsRequest) (*OptionsResponse, error)
	InspectFn       func(image ImageRef) (*InspectResponse, error)
	TemplateCheckFn func(source string) (*TemplateCheckResponse, error)
	CopyCheckFn     func(content Content) (*CopyCheck, error)
	Unhealthy       bool
}

// PNG1x1 is a valid one-pixel PNG, enough for anything that stores or
// serves a render without decoding it.
var PNG1x1 = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x60, 0x60, 0x60, 0x60,
	0x00, 0x00, 0x00, 0x05, 0x00, 0x01, 0x5e, 0xf3, 0x2a, 0x3a, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45,
	0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

// Render records the request and answers a clean one-pixel render.
func (f *Fake) Render(_ context.Context, req RenderRequest) (*RenderResponse, error) {
	f.RenderCalls = append(f.RenderCalls, req)
	if f.RenderFn != nil {
		return f.RenderFn(req)
	}
	return &RenderResponse{PNG: PNG1x1, Width: 1, Height: 1, Problems: []string{}, Warnings: []string{}}, nil
}

// Options answers one option per template on the first ground, up to Count.
func (f *Fake) Options(_ context.Context, req OptionsRequest) (*OptionsResponse, error) {
	f.OptionsCalls = append(f.OptionsCalls, req)
	if f.OptionsFn != nil {
		return f.OptionsFn(req)
	}
	ground := "paper"
	for g := range req.Brand.Grounds {
		ground = g
		break
	}
	out := &OptionsResponse{Options: []Option{}, Skipped: []Skipped{}}
	for i, t := range req.Templates {
		if i >= req.Count {
			break
		}
		var photos []PhotoUse
		if req.Hero != nil && t.Meta.Photos.Max > 0 {
			photos = []PhotoUse{*req.Hero}
		}
		out.Options = append(out.Options, Option{
			TemplateID: t.ID,
			Ground:     ground,
			Accent:     "amber",
			Photos:     photos,
			Source:     fmt.Sprintf("// poster from %s\n%s", t.ID, t.Source),
			PNG:        PNG1x1,
			Problems:   []string{},
			Warnings:   []string{},
		})
	}
	return out, nil
}

// Inspect answers a landscape camera photo.
func (f *Fake) Inspect(_ context.Context, image ImageRef) (*InspectResponse, error) {
	f.InspectCalls = append(f.InspectCalls, image)
	if f.InspectFn != nil {
		return f.InspectFn(image)
	}
	return &InspectResponse{Width: 1600, Height: 1200, Orientation: "landscape", C2PA: "none", Thumbnail: PNG1x1}, nil
}

// Photo answers the one-pixel image.
func (f *Fake) Photo(context.Context, ImageRef, int) ([]byte, error) { return PNG1x1, nil }

// Sheet answers the one-pixel image.
func (f *Fake) Sheet(context.Context, []SheetItem, Brand) ([]byte, error) { return PNG1x1, nil }

// TemplateCheck answers a clean check with a generic meta.
func (f *Fake) TemplateCheck(_ context.Context, source string, _ Brand, _ []ImageRef) (*TemplateCheckResponse, error) {
	if f.TemplateCheckFn != nil {
		return f.TemplateCheckFn(source)
	}
	meta := &TemplateMeta{Name: "Fake", Description: "a fake", Needs: []string{"eyebrow", "title"}}
	meta.Photos.Min, meta.Photos.Max = 0, 1
	return &TemplateCheckResponse{Meta: meta, Problems: map[string][]string{}, Renders: map[string][]byte{"portrait": PNG1x1}}, nil
}

// CopyCheck answers clean unless overridden.
func (f *Fake) CopyCheck(_ context.Context, content Content, _ *Brand) (*CopyCheck, error) {
	if f.CopyCheckFn != nil {
		return f.CopyCheckFn(content)
	}
	return &CopyCheck{Problems: []string{}, Warnings: []string{}}, nil
}

// BrandCheck answers no problems.
func (f *Fake) BrandCheck(context.Context, Brand) ([]string, error) { return []string{}, nil }

// Healthy reports the configured health.
func (f *Fake) Healthy(context.Context) bool { return !f.Unhealthy }
