package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"unicode/utf8"
)

const maxPaperBundleBytes = 4 * maxBodyBytes

// The envelope carries data, never authority. Execution still revalidates the
// selected research, exact input hashes, stored bars, SMA, leases and risk.
func decodePaperInputBundle(raw []byte) (proposal, bars, research []byte, err error) {
	invalid := errors.New("paper input bundle contract is invalid")
	if len(raw) == 0 || len(raw) > maxPaperBundleBytes || !utf8.Valid(raw) || !validJSONSurrogates(raw) {
		return nil, nil, nil, invalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if start, err := d.Token(); err != nil || start != json.Delim('{') {
		return nil, nil, nil, invalid
	}
	fields := make(map[string]json.RawMessage)
	for d.More() {
		key, err := d.Token()
		name, ok := key.(string)
		if err != nil || !ok || fields[name] != nil {
			return nil, nil, nil, invalid
		}
		switch name {
		case "schema_version", "mode", "proposal", "bars_csv", "research_csv":
		default:
			return nil, nil, nil, invalid
		}
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return nil, nil, nil, invalid
		}
		fields[name] = value
	}
	if end, err := d.Token(); err != nil || end != json.Delim('}') || len(fields) != 5 || ensureJSONEOF(d) != nil {
		return nil, nil, nil, invalid
	}
	values := make(map[string]string)
	for _, key := range []string{"schema_version", "mode", "bars_csv", "research_csv"} {
		var value *string
		if json.Unmarshal(fields[key], &value) != nil || value == nil {
			return nil, nil, nil, invalid
		}
		values[key] = *value
	}
	if values["schema_version"] != "paper-input-bundle.v1" || values["mode"] != "paper_bundle_only" ||
		len(values["bars_csv"]) > maxBodyBytes || len(values["research_csv"]) > maxBodyBytes {
		return nil, nil, nil, invalid
	}
	if _, err := decodePaperProposal(fields["proposal"]); err != nil {
		return nil, nil, nil, invalid
	}
	return fields["proposal"], []byte(values["bars_csv"]), []byte(values["research_csv"]), nil
}

// encoding/json v1 repairs lone UTF-16 surrogates. Reject instead: a byte-bound
// envelope must not silently replace data. The JSON decoder checks other syntax.
func validJSONSurrogates(raw []byte) bool {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) || raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		code, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if code >= 0xdc00 && code <= 0xdfff {
			return false
		}
		if code >= 0xd800 && code <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}
