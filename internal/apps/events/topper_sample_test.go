package events

// sampleTopper is a full week of realistic copy, used by the layout tests and
// by the preview renderer. It deliberately includes the awkward cases: a band
// with three bullets, a band with one long one, and a band with no door time.
func sampleTopper() Topper {
	return Topper{
		Heading:   "This week at Gravity Brewing",
		DateRange: "August 2-8",
		Site:      "gravitybrewing.com",
		Rows: []TopperRow{
			{Day: "MON", Title: "BOGO Pizza", Bullets: []string{"Buy one get one pizzas from Double D's", "Members only. Must register in advance", "Dine in only"}},
			{Day: "WED", Time: "6:30pm", Title: "Trivia Night", Bullets: []string{"Geeks Who Drink trivia", "Drink beer. Know things. Win prizes."}},
			{Day: "THU", Time: "5pm", Title: "Pints in the Park", Bullets: []string{"Join the crew from Pints in the Park at Gravity Brewing for special games, giveaways, and offers"}},
			{Day: "FRI", Time: "6pm", Title: "Bike Night", Bullets: []string{"Come and show off your bike - pedal or powered", "Stop in before or after the street fair"}},
			{Day: "SAT", Time: "2pm", Title: "Re-Launch Party", Bullets: []string{"Join Don and Matt as we celebrate the next orbit of Gravity Brewing", "Special beer release and discounts"}},
		},
	}
}
