package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

const cardPath = "/.well-known/tailhome.json"

type previewCard struct {
	Title       string
	Description string
	Icon        string
	Preview     bool
	Set         string
	Version     string
}

// parseCard rejects ambiguous objects before interpreting presentation fields.
func parseCard(r io.Reader) (*previewCard, error) {
	body, err := io.ReadAll(io.LimitReader(r, 4097))
	if err != nil {
		return nil, err
	}
	// An upstream HTML fallback (or any non-object response) means no card.
	if trimmed := bytes.TrimSpace(body); len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, nil
	}
	if len(body) > 4096 || !utf8.Valid(body) {
		return nil, fmt.Errorf("invalid size or UTF-8")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, fmt.Errorf("expected object")
	}
	fields := map[string]json.RawMessage{}
	for dec.More() {
		tok, err = dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("invalid key")
		}
		if _, exists := fields[key]; exists {
			return nil, fmt.Errorf("duplicate key")
		}
		var value json.RawMessage
		if err = dec.Decode(&value); err != nil {
			return nil, err
		}
		fields[key] = value
	}
	if _, err = dec.Token(); err != nil {
		return nil, err
	}
	if _, err = dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing data")
	}
	card := &previewCard{}
	for _, f := range []struct {
		key    string
		target *string
		limit  int
	}{{"title", &card.Title, 60}, {"description", &card.Description, 140}, {"icon", &card.Icon, 64}, {"set", &card.Set, 60}, {"version", &card.Version, 2}} {
		key, target := f.key, f.target
		raw, ok := fields[key]
		if !ok {
			continue
		}
		if bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, target) != nil {
			return nil, fmt.Errorf("invalid %s type", key)
		}
		*target = strings.TrimSpace(*target)
		if utf8.RuneCountInString(*target) > f.limit {
			return nil, fmt.Errorf("invalid %s length", key)
		}
		for _, c := range *target {
			if unicode.IsControl(c) || c == '\u2028' || c == '\u2029' {
				return nil, fmt.Errorf("invalid %s character", key)
			}
			if key == "icon" && !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return nil, fmt.Errorf("invalid icon")
			}
			if key == "version" && !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
				return nil, fmt.Errorf("invalid version")
			}
		}
	}
	if raw, ok := fields["preview"]; ok {
		if bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &card.Preview) != nil {
			return nil, fmt.Errorf("invalid preview type")
		}
	}
	return card, nil
}

type endpointState struct {
	card         *previewCard
	priorPreview bool
	tlsUp        bool
	diagnostic   string
	temporary    bool
	dnsName      string
	insecure     bool
}

// applyCard replaces a whole card; recovery never reuses a prior occupant's text.
func (s *endpointState) applyCard(card *previewCard, result string, manual, recovery bool) {
	switch result {
	case "ok":
		s.card = card
		s.priorPreview = card.Preview
	case "absent", "invalid":
		s.card = nil
		s.priorPreview = false
	default:
		if recovery || !manual {
			s.card = nil
		}
	}
}
