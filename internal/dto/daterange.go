package dto

// RangeQuery is the date picker in query form: either ?range=<preset> or ?from=YYYY-MM-DD&to=YYYY-MM-DD.
type RangeQuery struct {
	Range string `form:"range"`
	From  string `form:"from"`
	To    string `form:"to"`
}

// YearQuery is the year picker of the yearly charts. Nil means the current year.
type YearQuery struct {
	Year *int `form:"year"`
}

// DateRange echoes the resolved range in responses. End is the last included day.
type DateRange struct {
	Preset string `json:"preset"`
	Start  string `json:"start"`
	End    string `json:"end"`
}
