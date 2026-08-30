package model

import "testing"

func TestNormalizeTorrentStatusMigratesLegacyStates(t *testing.T) {
	cases := map[string]string{
		"ASSOCIATED":   TorrentCurrent,
		"OPEN":         TorrentCurrent,
		"SUPERSEDED":   TorrentSuperseded,
		"ORPHANED":     TorrentUnassociated,
		"UNMATCHED":    TorrentUnassociated,
		"UNASSOCIATED": TorrentUnassociated,
		"":             TorrentUnassociated,
	}
	for input, want := range cases {
		if got := NormalizeTorrentStatus(input); got != want {
			t.Errorf("NormalizeTorrentStatus(%q)=%q, want %q", input, got, want)
		}
	}
}
