// Package adapters holds the concrete document sources behind
// research.DocumentSource: a local corpus file and Google Drive.
//
// Both adapters apply the permission rules themselves and then hand the engine
// everything they hold, because the engine re-checks the rule on what it
// receives. An adapter is never trusted with the boundary; it is only trusted
// to be useful.
package adapters

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/GhandyP/owned-document-research/internal/research"
)

// corpusFile is the on-disk shape of a local corpus.
type corpusFile struct {
	Documents []research.Document `json:"documents"`
}

// LoadJSONCorpus reads, validates and indexes a corpus file.
func LoadJSONCorpus(path string) (*research.MemorySource, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("corpus: %w", err)
	}
	source, err := DecodeJSONCorpus(data)
	if err != nil {
		return nil, fmt.Errorf("corpus %s: %w", path, err)
	}
	return source, nil
}

// DecodeJSONCorpus builds a source from corpus bytes.
//
// Unknown fields are refused rather than ignored: a misspelled key in a
// permissions file would otherwise drop an owner or an allow entry, and the
// corpus would quietly mean something other than what it says.
func DecodeJSONCorpus(data []byte) (*research.MemorySource, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var parsed corpusFile
	if err := decoder.Decode(&parsed); err != nil {
		return nil, fmt.Errorf("corpus: %w", err)
	}
	if len(parsed.Documents) == 0 {
		return nil, fmt.Errorf("corpus: no documents")
	}
	source, err := research.NewMemorySource(parsed.Documents)
	if err != nil {
		return nil, fmt.Errorf("corpus: %w", err)
	}
	return source, nil
}
