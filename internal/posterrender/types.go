// Package posterrender is Kit's client for the poster renderer: the Node
// service under renderer/ that compiles poster code in a V8 isolate,
// validates the element tree against the tenant's brand, and rasterises it
// with Satori. Kit talks to it over HTTP on localhost, usually as a child
// process it supervises (see process.go), or wherever POSTER_RENDERER_URL
// points.
//
// The renderer keeps no records: every request carries the brand, the
// source and the image references it needs. These types mirror
// renderer/src/types.ts field for field.
package posterrender

// Format is a canvas with its safe margins.
type Format struct {
	Width  int        `json:"width"`
	Height int        `json:"height"`
	Safe   SafeMargin `json:"safe"`
}

// SafeMargin is the area type and the logo must stay inside.
type SafeMargin struct {
	Top    int `json:"top"`
	Right  int `json:"right"`
	Bottom int `json:"bottom"`
	Left   int `json:"left"`
}

// Ground is a background token with the tokens that go on it.
type Ground struct {
	Bg    string `json:"bg"`
	Text  string `json:"text"`
	Label string `json:"label"`
	Rule  string `json:"rule"`
	Logo  string `json:"logo"`
}

// Accent is the one highlighted element's fill and the text that goes on it.
type Accent struct {
	Fill string `json:"fill"`
	Text string `json:"text"`
}

// AccentRules says how many accent elements a composition may carry and
// whether two accent tokens may appear together.
type AccentRules struct {
	MaxElements int  `json:"maxElements"`
	Together    bool `json:"together"`
}

// Pair is an approved foreground/background pairing.
type Pair struct {
	Fg        string `json:"fg"`
	Bg        string `json:"bg"`
	MinSizePx int    `json:"minSizePx,omitempty"`
}

// FontRole is one of the three type roles.
type FontRole struct {
	Family  string  `json:"family"`
	Weights []int   `json:"weights"`
	Advance float64 `json:"advance,omitempty"`
}

// Fonts holds the three roles every brand has.
type Fonts struct {
	Display FontRole `json:"display"`
	Text    FontRole `json:"text"`
	Mono    FontRole `json:"mono"`
}

// Brand is the tenant's visual system as the renderer consumes it.
type Brand struct {
	Hash           string              `json:"hash"`
	Tokens         map[string]string   `json:"tokens"`
	Grounds        map[string]Ground   `json:"grounds"`
	Accents        map[string]Accent   `json:"accents"`
	AccentRules    AccentRules         `json:"accentRules"`
	Pairs          []Pair              `json:"pairs"`
	Fonts          Fonts               `json:"fonts"`
	Formats        map[string]Format   `json:"formats"`
	PortraitFormat string              `json:"portraitFormat"`
	Logos          map[string]ImageRef `json:"logos"`
	BannedWords    []string            `json:"bannedWords,omitempty"`
}

// ImageSource says where the renderer fetches an image from.
type ImageSource string

// Image sources.
const (
	SourceDrive   ImageSource = "drive"
	SourcePixabay ImageSource = "pixabay"
	SourceURL     ImageSource = "url"
	SourceInline  ImageSource = "inline"
)

// ImageRef is an image the renderer may fetch. Poster code refers to it by
// id only. A Drive image carries the Drive file id separately from Kit's
// id, and never a URL: only the renderer builds Drive URLs, so the server's
// Drive key never leaves it.
type ImageRef struct {
	ID       string      `json:"id"`
	Source   ImageSource `json:"source"`
	FileID   string      `json:"fileId,omitempty"`
	URL      string      `json:"url,omitempty"`
	Modified string      `json:"modified,omitempty"`
	Data     string      `json:"data,omitempty"`
	Folder   string      `json:"folder,omitempty"`
}

// PhotoUse is how a layout crops a photo.
type PhotoUse struct {
	ID     string  `json:"id"`
	FocusX float64 `json:"focusX"`
	FocusY float64 `json:"focusY"`
	Zoom   float64 `json:"zoom"`
}

// Detail is one label/value line under the headline.
type Detail struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Content is a poster's copy.
type Content struct {
	Eyebrow string   `json:"eyebrow"`
	Title   string   `json:"title"`
	Summary string   `json:"summary,omitempty"`
	Details []Detail `json:"details"`
	Action  string   `json:"action,omitempty"`
}

// TemplateMeta is a template's self-description.
type TemplateMeta struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Photos      struct {
		Min int `json:"min"`
		Max int `json:"max"`
	} `json:"photos"`
	Needs []string `json:"needs"`
}

// TemplateProps are the props a template is rendered with.
type TemplateProps struct {
	Content Content    `json:"content"`
	Photos  []PhotoUse `json:"photos"`
	Ground  string     `json:"ground"`
	Accent  string     `json:"accent"`
}

// RenderRequest renders one poster or template at one canvas.
type RenderRequest struct {
	Kind   string         `json:"kind"`
	Source string         `json:"source"`
	Format string         `json:"format"`
	Brand  Brand          `json:"brand"`
	Photos []ImageRef     `json:"photos"`
	Props  *TemplateProps `json:"props,omitempty"`
}

// RenderResponse is a render's outcome. PNG is empty when Problems is not.
type RenderResponse struct {
	PNG      []byte        `json:"png"`
	Width    int           `json:"width"`
	Height   int           `json:"height"`
	Problems []string      `json:"problems"`
	Warnings []string      `json:"warnings"`
	Meta     *TemplateMeta `json:"meta,omitempty"`
	Tree     string        `json:"tree,omitempty"`
}

// PlannedTemplate is a template the option generator may use.
type PlannedTemplate struct {
	ID     string       `json:"id"`
	Source string       `json:"source"`
	Weight float64      `json:"weight"`
	Meta   TemplateMeta `json:"meta"`
}

// OptionsRequest asks for up to Count varied posters.
type OptionsRequest struct {
	Content   Content           `json:"content"`
	Brand     Brand             `json:"brand"`
	Format    string            `json:"format"`
	Photos    []ImageRef        `json:"photos"`
	Hero      *PhotoUse         `json:"hero,omitempty"`
	Templates []PlannedTemplate `json:"templates"`
	Count     int               `json:"count"`
	Exclude   []string          `json:"exclude,omitempty"`
}

// Option is one generated poster.
type Option struct {
	TemplateID string     `json:"templateId"`
	Ground     string     `json:"ground"`
	Accent     string     `json:"accent"`
	Photos     []PhotoUse `json:"photos"`
	Source     string     `json:"source"`
	PNG        []byte     `json:"png"`
	Problems   []string   `json:"problems"`
	Warnings   []string   `json:"warnings"`
}

// Skipped records a candidate the generator could not use.
type Skipped struct {
	TemplateID string `json:"templateId"`
	Ground     string `json:"ground"`
	Reason     string `json:"reason"`
}

// OptionsResponse is the generator's output.
type OptionsResponse struct {
	Options []Option  `json:"options"`
	Skipped []Skipped `json:"skipped"`
}

// InspectResponse describes an image without a render.
type InspectResponse struct {
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Orientation string `json:"orientation"`
	C2PA        string `json:"c2pa"`
	Thumbnail   []byte `json:"thumbnail"`
}

// SheetItem is one numbered thumbnail on a contact sheet.
type SheetItem struct {
	Image ImageRef `json:"image"`
	Label string   `json:"label"`
}

// TemplateCheckResponse is the activation check's outcome: problems keyed by
// "format:sample", and a portrait/story/screen render each.
type TemplateCheckResponse struct {
	Meta     *TemplateMeta       `json:"meta"`
	Problems map[string][]string `json:"problems"`
	Renders  map[string][]byte   `json:"renders"`
}

// CopyCheck is the copy rules' verdict.
type CopyCheck struct {
	Problems []string `json:"problems"`
	Warnings []string `json:"warnings"`
}
