package dataset

import (
	"bytes"
	_ "embed"
	"fmt"
)

//go:embed data/latency_worldwide.csv
var embeddedLatencyCSV []byte

// LoadEmbedded returns the built-in worldwide latency dataset.
// This ships with the binary — no external files needed.
func LoadEmbedded() (*Dataset, error) {
	if len(embeddedLatencyCSV) == 0 {
		return nil, fmt.Errorf("embedded dataset is empty")
	}
	return LoadCSVFromReader(bytes.NewReader(embeddedLatencyCSV))
}
