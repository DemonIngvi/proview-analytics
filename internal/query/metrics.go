package query

// Metrics are the counters of a slice plus derived ratios. Derived values
// are computed here (not in the UI) so that every consumer uses the same
// definitions (see proview-docs, "Metric definitions").
type Metrics struct {
	Impressions uint64 `json:"impressions"`
	Viewable    uint64 `json:"viewable"`
	Clicks      uint64 `json:"clicks"`
	Hovers      uint64 `json:"hovers"`
	HoverMS     uint64 `json:"hover_ms"`
	ActiveMS    uint64 `json:"active_ms"`
	Visitors    uint64 `json:"visitors"`

	CTR         float64 `json:"ctr"`           // clicks / impressions
	Viewability float64 `json:"viewability"`   // viewable / impressions
	HoverRate   float64 `json:"hover_rate"`    // hovers / impressions
	AvgHoverMS  float64 `json:"avg_hover_ms"`  // hover_ms / hovers
	AvgActiveMS float64 `json:"avg_active_ms"` // active_ms / impressions
}

func ratio(a, b uint64) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

// Derive fills the ratio fields.
func (m *Metrics) Derive() {
	m.CTR = ratio(m.Clicks, m.Impressions)
	m.Viewability = ratio(m.Viewable, m.Impressions)
	m.HoverRate = ratio(m.Hovers, m.Impressions)
	m.AvgHoverMS = ratio(m.HoverMS, m.Hovers)
	m.AvgActiveMS = ratio(m.ActiveMS, m.Impressions)
}

// Add accumulates counters (derived fields must be recomputed afterwards).
func (m *Metrics) Add(o Metrics) {
	m.Impressions += o.Impressions
	m.Viewable += o.Viewable
	m.Clicks += o.Clicks
	m.Hovers += o.Hovers
	m.HoverMS += o.HoverMS
	m.ActiveMS += o.ActiveMS
	m.Visitors += o.Visitors
}
