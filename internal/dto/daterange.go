package dto

// DateRange echoes the resolved range in responses. End is the last included day.
type DateRange struct {
	Preset string `json:"preset"`
	Start  string `json:"start"`
	End    string `json:"end"`
}
