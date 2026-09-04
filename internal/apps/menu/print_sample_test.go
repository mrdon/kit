package menu

// Sample configuration for the print tests and the live preview.
//
// Only the chrome lives here -- the masthead wording, the section colours, and
// the rows Untappd does not carry. Beers, prices, ABVs and descriptions are
// never hard-coded: they are fetched, because a fixture of them goes stale the
// first time a keg blows and then quietly shows a menu that was true in August.

func samplePrintConfig() PrintConfig {
	return PrintConfig{
		Brand:     "gravitybrewing",
		Title:     "Beers",
		Subtitle:  "& Beverages",
		Flight:    "Try any set of four 4oz pours as a flight",
		FootLeft:  "WIFI: Gravity  PW: beerparlor1",
		FootRight: "@thegravitybrewing",
		Colors: map[string]string{
			"Lagers":           "#fec111",
			"Pub Ales":         "#1b4525",
			"Belgian Styles":   "#f58a22",
			"Pale Ales & IPAs": "#fec111",
			"Stouts & Porters": "#678f42",
			"Specialty":        "#1b4525",
			"Non-Alcoholic":    "#f58a22",
			"Sodas & Juices":   "#fec111",
		},
		Extras: []Beer{
			{Section: "Non-Alcoholic", Name: "Athletic Upside Dawn", Style: "Golden Ale",
				Pours: []Pour{{Size: "12oz", Label: "12oz Can", Price: "6"}}},
			{Section: "Non-Alcoholic", Name: "Sierra Nevada Trail Pass", Style: "IPA",
				Pours: []Pour{{Size: "12oz", Label: "12oz Can", Price: "6"}}},
			{Section: "Non-Alcoholic", Name: "Athletic Lite Lime & Salt",
				Style: "Light Beer. With Lime. And Salt.",
				Pours: []Pour{{Size: "12oz", Label: "12oz Can", Price: "6"}}},
			{Section: "Sodas & Juices", Name: "Craft Root Beer",
				Pours: []Pour{{Size: "12oz", Label: "12oz Glass", Price: "4.50"}}},
			{Section: "Sodas & Juices", Name: "Lemonade",
				Pours: []Pour{{Size: "12oz", Label: "12oz Glass", Price: "3.50"}}},
			{Section: "Sodas & Juices", Name: "Juice Boxes",
				Pours: []Pour{{Size: "12oz", Label: "Box", Price: "2"}}},
			{Section: "Sodas & Juices", Name: "Sodas (Coke Products)",
				Pours: []Pour{{Size: "12oz", Label: "12oz Glass", Price: "2"}}},
		},
	}
}
