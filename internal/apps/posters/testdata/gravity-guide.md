# Test brand guide

Prose the parser ignores. Section 9 below is the machine-readable block.

## 9. Machine-readable tokens

```json
{
  "brand": "Gravity Brewing",
  "color": {
    "ink":    { "hex": "#171A14", "role": "text, dark ground" },
    "forest": { "hex": "#1C3D1B", "role": "secondary dark" },
    "sage":   { "hex": "#6B8F4E", "role": "supporting mid" },
    "amber":  { "hex": "#F5A81C", "role": "accent, max one per composition" },
    "ember":  { "hex": "#E4571C", "role": "urgency only" },
    "stone":  { "hex": "#D9D3C4", "role": "dividers, fills" },
    "paper":  { "hex": "#F6F2E7", "role": "light ground, replaces white" }
  },
  "approved_pairs": [
    { "fg": "ink",    "bg": "paper",  "ratio": 15.72 },
    { "fg": "paper",  "bg": "ink",    "ratio": 15.72 },
    { "fg": "paper",  "bg": "forest", "ratio": 10.83 },
    { "fg": "forest", "bg": "paper",  "ratio": 10.83 },
    { "fg": "amber",  "bg": "ink",    "ratio": 8.79  },
    { "fg": "ink",    "bg": "amber",  "ratio": 8.79  },
    { "fg": "ink",    "bg": "stone",  "ratio": 11.78 },
    { "fg": "ink",    "bg": "sage",   "ratio": 4.75  },
    { "fg": "paper",  "bg": "sage",   "ratio": 3.31, "min_size_px": 24 },
    { "fg": "ember",  "bg": "paper",  "ratio": 3.31, "min_size_px": 24 },
    { "fg": "paper",  "bg": "ember",  "ratio": 3.31, "min_size_px": 18 }
  ],
  "type": {
    "display": { "family": "Archivo Narrow", "weight": 700 },
    "text":    { "family": "Archivo", "weights": [400, 600] },
    "mono":    { "family": "IBM Plex Mono", "weight": 500 }
  },
  "logo": {
    "variants": ["color", "white", "black", "forest"],
    "on_paper": "color",
    "on_ink": "white",
    "on_forest": "white",
    "on_photo": "white"
  },
  "canvas": {
    "ig_feed":      { "w": 1080, "h": 1080, "safe": 60 },
    "ig_portrait":  { "w": 1080, "h": 1350, "safe": 60 },
    "story":        { "w": 1080, "h": 1920, "safe_top": 100, "safe_bottom": 250 },
    "fb_cover":     { "w": 1640, "h": 856,  "safe": 120 },
    "screen":       { "w": 1920, "h": 1080, "safe": 64 }
  },
  "constraints": {
    "max_amber_elements": 1,
    "amber_and_ember_together": false,
    "gradients": false
  }
}
```
