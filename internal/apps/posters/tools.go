package posters

import "github.com/mrdon/kit/internal/services"

// posterTools is the single source of tool metadata for the agent and MCP
// surfaces. Both build from this slice and run the same dispatchCore.
//
// Tools that return a render attach the PNG as an image (agent) or image
// content (MCP) so the model can look at what it made.
var posterTools = []services.ToolMeta{
	{
		Name: "list_posters",
		Description: "List this workspace's event posters: id, event, status (on the event, draft, out of date) and when it changed. " +
			"A poster is TSX source rendered by Kit; see get_poster for one.",
		Schema: services.Props(map[string]any{}),
	},
	{
		Name: "get_poster",
		Description: "Read a poster: its current version's TSX source, the content it was made from, the canvas, " +
			"the photos it uses, and the problems of its last render. Read before editing.",
		Schema: services.PropsReq(map[string]any{
			"poster_id": services.Field("string", "Poster id."),
		}, "poster_id"),
	},
	{
		Name: "edit_poster_source",
		Description: "Replace a poster's TSX source. The renderer validates it against the brand (token colors only, approved pairings, " +
			"one accent element, numbers in the mono face, no drawing elements, type inside the safe margins) and renders it. " +
			"On success a new version is saved and the render comes back as an image: LOOK at it before replying. " +
			"On failure nothing is saved and the problems come back for you to fix. Photos are referenced by id only " +
			"(<Photo id=\"…\" />); find ids with search_photos.",
		Schema: services.PropsReq(map[string]any{
			"poster_id": services.Field("string", "Poster id."),
			"source":    services.Field("string", "The complete TSX file. Imports only from react and @poster/kit; default-exports Poster({ format })."),
			"summary":   services.Field("string", "One line saying what changed, e.g. 'Bigger title, photo on the left'. Shown in the version history."),
		}, "poster_id", "source", "summary"),
	},
	{
		Name: "set_poster_fields",
		Description: "Quick edits to a poster without rewriting its code: swap the template (layout), the ground, the hero photo, " +
			"or the hero's zoom and focus point. Renders and saves a new version like edit_poster_source. " +
			"Only works while the poster still carries the inlined content block a picked option starts with.",
		Schema: services.PropsReq(map[string]any{
			"poster_id": services.Field("string", "Poster id."),
			"layout":    services.Field("string", "Template id or name to switch to (see list_templates)."),
			"ground":    services.Field("string", "Ground token, e.g. paper or ink."),
			"photo":     services.Field("string", "Library photo id to use as the hero."),
			"zoom":      services.Field("number", "Crop tightness for the hero, 1 to 3."),
			"focus_x":   services.Field("number", "Hero focus point x, 0 to 1."),
			"focus_y":   services.Field("number", "Hero focus point y, 0 to 1."),
		}, "poster_id"),
	},
	{
		Name: "render_poster_formats",
		Description: "Render the poster's current version at other canvases the brand defines (story, screen, feed, website hero...). " +
			"Returns each render as an image plus any safe-area problems. Downloads are on the poster page.",
		Schema: services.PropsReq(map[string]any{
			"poster_id": services.Field("string", "Poster id."),
			"formats": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Format names from the brand's canvas list. Omit for every format.",
			},
		}, "poster_id"),
	},
	{
		Name: "search_photos",
		Description: "Full-text search over the indexed photo library (descriptions, tags, folder, notes). Returns ids to use in " +
			"<Photo id=\"…\" />, each with its description, orientation and focus point. Only indexed photos are offered; " +
			"a photo whose provenance says it was generated is never returned. If nothing fits, say so rather than using a photo of something else.",
		Schema: services.PropsReq(map[string]any{
			"query": services.Field("string", "Plain words, e.g. 'barrel room low light' or the event title."),
			"limit": services.Field("integer", "Max results, default 10."),
		}, "query"),
	},
	{
		Name: "find_stock_photos",
		Description: "Search Pixabay for a stock photo when the library has nothing that fits. Results are usable directly as " +
			"<Photo id=\"pixabay:<id>\" />. Screen each pick: no visible logos or trademarks, no recognisable person in a " +
			"misleading role, and prefer real photographs (Pixabay cannot guarantee a stock image was camera-made). " +
			"Name Pixabay as the source when you tell the user.",
		Schema: services.PropsReq(map[string]any{
			"query": services.Field("string", "Search words, e.g. 'vinyl record turntable'."),
		}, "query"),
	},
	{
		Name: "ask_for_photo",
		Description: "The library has no photo for what the user wants. Returns the message to relay: how to add one to the " +
			"shared Drive folder (then it is indexed and searchable for every poster) or to upload one here for this poster only " +
			"(reference it as <Photo id=\"attachment:<attachment id>\" />). Changes nothing.",
		Schema: services.PropsReq(map[string]any{
			"what": services.Field("string", "What kind of photo is missing, e.g. 'a photo of the trivia host'."),
		}, "what"),
	},
	{
		Name: "set_poster_on_event",
		Description: "Put a poster version on its event: renders the portrait canvas, stores it as the event's poster image, " +
			"and records the event facts the copy used so a later change flags the poster as out of date.",
		Schema: services.PropsReq(map[string]any{
			"poster_id":  services.Field("string", "Poster id."),
			"version_id": services.Field("string", "Version to set. Defaults to the current version."),
		}, "poster_id"),
	},
	{
		Name: "update_poster_facts",
		Description: "For a poster flagged out of date: re-read the event and change only the strings in the source that state a " +
			"changed fact (date, time, price, place). Saves a new version next to the current one; nothing reaches the event " +
			"until set_poster_on_event.",
		Schema: services.PropsReq(map[string]any{
			"poster_id": services.Field("string", "Poster id."),
		}, "poster_id"),
	},
	{
		Name: "list_templates",
		Description: "List poster templates with their meta (photos needed, content fields needed), origin, status and pick count. " +
			"Only active templates feed the option generator; built-ins ship with Kit and cannot be edited in place.",
		Schema: services.Props(map[string]any{
			"status": services.Field("string", "draft, active or archived. Omit for all."),
		}),
	},
	{
		Name:        "get_template",
		Description: "Read a template's current TSX source and its version history.",
		Schema: services.PropsReq(map[string]any{
			"template_id": services.Field("string", "Template id."),
		}, "template_id"),
	},
	{
		Name: "edit_template_source",
		Description: "Replace a tenant template's TSX source. The renderer checks it at portrait, story and screen with sample content; " +
			"on success a new template version is saved and the three renders come back as images. Built-ins cannot be edited: " +
			"create_template with their source makes a copy.",
		Schema: services.PropsReq(map[string]any{
			"template_id": services.Field("string", "Template id."),
			"source":      services.Field("string", "The complete TSX file: exports meta and default-exports Template({ content, format, photos, ground, accent })."),
			"summary":     services.Field("string", "One line saying what changed."),
		}, "template_id", "source", "summary"),
	},
	{
		Name: "create_template",
		Description: "Create a new DRAFT template from TSX source. Checked like edit_template_source. A person activates it on the " +
			"templates page (or set_template_status over MCP). origin says where it came from.",
		Schema: services.PropsReq(map[string]any{
			"source":                  services.Field("string", "The complete template TSX."),
			"name":                    services.Field("string", "Short name, 1 to 3 words. Defaults to meta.name."),
			"origin":                  services.Field("string", "poster (saved from a poster), image (from a reference image), or chat (written from a description). Default chat."),
			"source_poster_id":        services.Field("string", "For origin poster: the poster it came from."),
			"reference_attachment_id": services.Field("string", "For origin image: the uploaded reference image's attachment id."),
			"parent_template_id":      services.Field("string", "When duplicating an existing template, its id."),
		}, "source"),
	},
	{
		Name: "set_template_status",
		Description: "Activate or archive a tenant template, or hide/unhide a built-in for this workspace. " +
			"Only active templates are offered by the generator.",
		Schema: services.PropsReq(map[string]any{
			"template_id": services.Field("string", "Template id."),
			"status":      services.Field("string", "active, archived or draft for tenant templates; hidden or visible for built-ins."),
		}, "template_id", "status"),
	},
}

// mcpOnlyTools are registered on the MCP surface but not for Kit's in-app
// agent: either because the point is to spend the caller's model (photo
// indexing), or because the decision belongs to a person driving a harness
// (activating templates). An intended exception to agent/MCP parity.
var mcpOnlyTools = map[string]bool{
	"set_template_status":     true,
	"list_pending_photos":     true,
	"get_photo_sheet":         true,
	"get_photo":               true,
	"index_photos":            true,
	"update_photo_index":      true,
	"sync_poster_photos":      true,
	"generate_poster_options": true,
}

// indexTools describe photos. See the indexing-poster-photos skill for the
// description rules every indexer follows.
var indexTools = []services.ToolMeta{
	{
		Name: "list_pending_photos",
		Description: "Photos synced from Drive that have no description yet: id, folder, filename, orientation. " +
			"Describe them with index_photos following the indexing-poster-photos skill (load it first). " +
			"Pending photos are not offered to posters until indexed.",
		Schema: services.Props(map[string]any{
			"limit": services.Field("integer", "Max rows, default 48."),
		}),
	},
	{
		Name: "get_photo_sheet",
		Description: "One contact-sheet image of up to 12 numbered thumbnails with their ids: the cheap way to look at many " +
			"photos at once while describing them.",
		Schema: services.PropsReq(map[string]any{
			"ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Photo ids, at most 12."},
		}, "ids"),
	},
	{
		Name:        "get_photo",
		Description: "One photo as an image (default 1024px on the long edge), for placing the focus point precisely or checking a detail.",
		Schema: services.PropsReq(map[string]any{
			"id":   services.Field("string", "Photo id."),
			"size": services.Field("integer", "Long edge in pixels, 256 to 2048. Default 1024."),
		}, "id"),
	},
	{
		Name: "index_photos",
		Description: "Write descriptions for photos in one batch and mark them indexed. Follow the indexing-poster-photos skill: " +
			"describe the subject literally, note light and where there is clean space for type, faces and whether they are " +
			"prominent, duplicates and blur; tags from the small fixed vocabulary plus subject words; focus point on the subject.",
		Schema: services.PropsReq(map[string]any{
			"entries": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":          services.Field("string", "Photo id."),
						"description": services.Field("string", "One or two literal sentences."),
						"tags":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Fixed vocabulary plus subject words."},
						"focus_x":     services.Field("number", "0 to 1, left to right."),
						"focus_y":     services.Field("number", "0 to 1, top to bottom."),
						"notes":       services.Field("string", "Caveats: blur, duplicate of, permission needed, text in frame."),
					},
					"required": []string{"id", "description"},
				},
				"description": "Photos to index.",
			},
		}, "entries"),
	},
	{
		Name:        "update_photo_index",
		Description: "Correct one indexed photo's description, tags, focus point or notes.",
		Schema: services.PropsReq(map[string]any{
			"id":          services.Field("string", "Photo id."),
			"description": services.Field("string", "New description."),
			"tags":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "New tag list (replaces)."},
			"focus_x":     services.Field("number", "0 to 1."),
			"focus_y":     services.Field("number", "0 to 1."),
			"notes":       services.Field("string", "New notes."),
		}, "id"),
	},
	{
		Name:        "sync_poster_photos",
		Description: "List the Drive photo folder now and update the index: new files become pending, missing files are marked removed.",
		Schema:      services.Props(map[string]any{}),
		AdminOnly:   true,
	},
	{
		Name: "generate_poster_options",
		Description: "Generate a batch of poster options for an event (copy from the event, a hero photo from the index, seven " +
			"layouts). Returns the option version ids; look at one with get_poster after set_poster_fields or render it via " +
			"render_poster_formats. Pass more=true for another batch that avoids templates already shown.",
		Schema: services.PropsReq(map[string]any{
			"event_id": services.Field("string", "Event id."),
			"more":     services.Field("boolean", "Another batch for an event that already has options."),
		}, "event_id"),
	},
}
