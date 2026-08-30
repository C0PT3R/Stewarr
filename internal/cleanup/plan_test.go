package cleanup

import (
	"testing"

	"connarr/internal/model"
)

func TestBuildUnavailableStorage(t *testing.T) {
	p, err := Build("/definitely/not/a/connarr/storage/path", 90, 95, nil, true)
	if err == nil {
		t.Fatal("expected storage probe error")
	}
	if p.Available {
		t.Fatal("unavailable storage must not be marked available")
	}
	if p.Path == "" || p.Error == "" {
		t.Fatalf("expected path and error in unavailable plan: %#v", p)
	}
	if p.SelectedBytes != 0 || len(p.Selected) != 0 {
		t.Fatal("unavailable storage must not produce a cleanup selection")
	}
}

func TestBuildUsesPhysicalReclaimabilityAndSkipsProtectedMedia(t *testing.T) {
	items := []model.Media{
		{Title: "Protected", Protected: true, ReclaimableKnown: true, ReclaimableBytes: 1000, SizeBytes: 1000},
		{Title: "Shared", ReclaimableKnown: true, ReclaimableBytes: 0, SizeBytes: 2000},
		{Title: "Physical", ReclaimableKnown: true, ReclaimableBytes: 7, SizeBytes: 3000},
	}
	p, err := Build(t.TempDir(), 0, 0, items, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Selected) != 1 || p.Selected[0].Title != "Physical" || p.SelectedBytes != 7 {
		t.Fatalf("cleanup did not use safe physical bytes: %#v", p)
	}
}

func TestBuildPausesWhenValuationIsUnreliable(t *testing.T) {
	p, err := Build(t.TempDir(), 0, 0, []model.Media{{Title: "unsafe", SizeBytes: 1}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Reliable || len(p.Selected) != 0 || p.SelectedBytes != 0 {
		t.Fatalf("unreliable valuation produced cleanup candidates: %#v", p)
	}
}

func TestBuildUsesTargetWithoutWaitingForCritical(t *testing.T) {
	items := []model.Media{{Title: "candidate", ReclaimableKnown: true, ReclaimableBytes: 1}}
	p, err := Build(t.TempDir(), 0, 99.999, items, true)
	if err != nil {
		t.Fatal(err)
	}
	if p.NeedBytes == 0 || len(p.Selected) != 1 {
		t.Fatalf("critical incorrectly gated target planning: %#v", p)
	}
}
