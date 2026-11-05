package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"tailscale.com/atomicfile"
)

type storedShare struct {
	ShareRecord
	Binding            ShareBinding `json:"binding"`
	PreviewFingerprint string       `json:"previewFingerprint,omitempty"`
}
type shareLedger struct {
	Version int                     `json:"version"`
	Records map[string]*ShareRecord `json:"-"`
	Shares  []storedShare           `json:"shares"`
}
type shareStore struct {
	dir    string
	ledger shareLedger
	write  func(string, []byte, os.FileMode) error
}

func openShareStore(root string) (*shareStore, error) {
	dir := filepath.Join(root, "shares")
	if err := os.MkdirAll(filepath.Join(dir, "nodes"), 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	store := &shareStore{dir: dir, write: atomicfile.WriteFile, ledger: shareLedger{Version: 1, Records: map[string]*ShareRecord{}}}
	data, err := os.ReadFile(filepath.Join(dir, "shares.json"))
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err == nil {
		err = json.Unmarshal(data, &store.ledger)
	}
	if err != nil {
		return nil, fmt.Errorf("reading sharing state: %w", err)
	}
	if store.ledger.Version != 1 {
		return nil, fmt.Errorf("unsupported sharing state version %d", store.ledger.Version)
	}
	store.ledger.Records = map[string]*ShareRecord{}
	for _, stored := range store.ledger.Shares {
		record := stored.ShareRecord
		record.Binding = stored.Binding
		record.PreviewFingerprint = stored.PreviewFingerprint
		record.TargetID = targetIDForBinding(record.Binding)
		if !validShareID(record.ID) || record.Hostname != "public-"+record.ID || store.ledger.Records[record.ID] != nil || record.CreatedAt.IsZero() || record.NextReviewAt.IsZero() {

			return nil, errors.New(shareStorageMessage)
		}
		switch record.State {
		case "starting", "public", "stopped", "expired", "failed", "cleanup":
		default:

			return nil, errors.New(shareStorageMessage)
		}
		store.ledger.Records[record.ID] = &record
	}
	return store, nil
}
func (s *shareStore) Save(records map[string]*ShareRecord) error {
	ledger := shareLedger{Version: 1, Shares: make([]storedShare, 0, len(records))}
	for _, r := range records {
		ledger.Shares = append(ledger.Shares, storedShare{ShareRecord: *r, Binding: r.Binding, PreviewFingerprint: r.PreviewFingerprint})
	}
	data, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return err
	}
	if err = s.write(filepath.Join(s.dir, "shares.json"), data, 0600); err != nil {
		return err
	}
	directory, err := os.Open(s.dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	if runtime.GOOS == "windows" {
		return nil
	}
	return directory.Sync()
}
func validShareID(id string) bool {
	if len(id) != 16 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= '2' && c <= '7') {
			return false
		}
	}
	return true
}
