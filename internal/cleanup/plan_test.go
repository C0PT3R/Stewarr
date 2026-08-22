package cleanup

import "testing"

func TestBuildUnavailableStorage(t *testing.T) {
	p, err := Build("/definitely/not/a/spartarr/storage/path", 90, 95, nil)
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
