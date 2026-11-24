package web

// ExpiryChoices are the relative expiries every list offers (composer, edit
// drawer, bulk dialog, defaults): the one place their values and words live.
var ExpiryChoices = []struct{ Value, Label string }{
	{"never", "never"}, {"1h", "1 hour"}, {"1d", "1 day"}, {"7d", "7 days"}, {"30d", "30 days"}, {"90d", "90 days"},
}

// ExpiryOptions is ExpiryChoices as the value/label maps templates and the
// pb.select options take (the template function "expiries").
func ExpiryOptions() []map[string]any {
	out := make([]map[string]any, len(ExpiryChoices))
	for i, c := range ExpiryChoices {
		out[i] = map[string]any{"value": c.Value, "label": c.Label}
	}
	return out
}

// ExpiryValues are just the values.
func ExpiryValues() []string {
	out := make([]string, len(ExpiryChoices))
	for i, c := range ExpiryChoices {
		out[i] = c.Value
	}
	return out
}
