package httpui

import (
	"net/http/httptest"
	"strings"
	"testing"

	"connarr/internal/model"
)

// TestLibraryTemplateShowsEmptyStateWithoutMediaLibrary guards the same
// gap as the Torrents page: with neither a Radarr nor a Sonarr instance
// configured, there's nothing to manage — showing an empty, filterable
// table gives no indication why.
func TestLibraryTemplateShowsEmptyStateWithoutMediaLibrary(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := libraryData{HasMediaLibrary: false}
	recorder := httptest.NewRecorder()
	if err := renderTemplate(recorder, server.libraryTpl, data); err != nil {
		t.Fatalf("render library template: %v", err)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "No media library registered") {
		t.Fatalf("expected the empty-state message, got:\n%s", body)
	}
	if !strings.Contains(body, `data-overlay-url="/services/add?category=medialibrary"`) || !strings.Contains(body, "Add media library") {
		t.Fatalf("expected an Add media library button scoped to the medialibrary category, got:\n%s", body)
	}
	if strings.Contains(body, "<table>") {
		t.Fatalf("expected no table when no media library is configured, got:\n%s", body)
	}
}

func TestLibraryTemplateHidesTypeFilterWithSingleType(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := libraryData{
		HasMediaLibrary: true,
		Rows:            []mediaRow{{Media: model.Media{Type: model.Movie, SourceID: 1, Title: "Test"}}},
		TypeOptions:     []mediaTypeOption{{Value: "movie", Label: "Movies"}},
		Page:            1, PageSize: 50, TotalPages: 1, TotalItems: 1, Sort: "title", Order: "asc",
		SortURLs:  map[string]string{"value": "/library", "title": "/library", "type": "/library", "rating": "/library", "votes": "/library", "views": "/library", "lastwatched": "/library", "requested": "/library", "size": "/library", "torrents": "/library"},
		SizeLinks: []navLink{{Value: 50, URL: "/library"}},
	}
	recorder := httptest.NewRecorder()
	if err := renderTemplate(recorder, server.libraryTpl, data); err != nil {
		t.Fatalf("render library template: %v", err)
	}
	body := recorder.Body.String()
	if strings.Contains(body, "name=type") {
		t.Fatalf("expected the Type filter to disappear with only one type present, got:\n%s", body)
	}
	if strings.Contains(body, "name=source") {
		t.Fatalf("expected no Source filter when SourceOptions is empty, got:\n%s", body)
	}
}

func TestLibraryTemplateShowsTypeAndSourceFiltersWhenPopulated(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := libraryData{
		HasMediaLibrary: true,
		Rows:            []mediaRow{{Media: model.Media{Type: model.Movie, SourceID: 1, Title: "Test"}}},
		TypeOptions:     []mediaTypeOption{{Value: "movie", Label: "Movies"}, {Value: "series", Label: "Series"}},
		SourceFilter:    "any",
		SourceOptions:   []mediaSource{{Key: "Movies", Label: "Movies"}, {Key: "Movies 4K", Label: "Movies 4K"}},
		Page:            1, PageSize: 50, TotalPages: 1, TotalItems: 1, Sort: "title", Order: "asc",
		SortURLs:  map[string]string{"value": "/library", "title": "/library", "type": "/library", "rating": "/library", "votes": "/library", "views": "/library", "lastwatched": "/library", "requested": "/library", "size": "/library", "torrents": "/library"},
		SizeLinks: []navLink{{Value: 50, URL: "/library"}},
	}
	recorder := httptest.NewRecorder()
	if err := renderTemplate(recorder, server.libraryTpl, data); err != nil {
		t.Fatalf("render library template: %v", err)
	}
	body := recorder.Body.String()
	for _, want := range []string{"name=type", "name=source", "Movies 4K"} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected %q in output, got:\n%s", want, body)
		}
	}
}

func TestMediaTypeLabelAndAvailableMediaTypes(t *testing.T) {
	items := []model.Media{{Type: model.Movie}, {Type: model.Movie}, {Type: model.Series}, {Type: model.MediaType("artist")}}
	types := availableMediaTypes(items)
	if len(types) != 3 || types[0] != model.Movie || types[1] != model.Series || types[2] != model.MediaType("artist") {
		t.Fatalf("expected [movie series artist] in that order, got %#v", types)
	}
	if mediaTypeLabel(model.Movie) != "Movies" || mediaTypeLabel(model.Series) != "Series" {
		t.Fatalf("expected established labels for movie/series")
	}
	if got := mediaTypeLabel(model.MediaType("artist")); got != "Artists" {
		t.Fatalf("expected a future type to get a naive title-case-plus-s label, got %q", got)
	}
}

func TestNormalizeMediaTypeFilterRejectsUnavailableType(t *testing.T) {
	available := []model.MediaType{model.Movie}
	if got := normalizeMediaTypeFilter("series", available); got != "any" {
		t.Fatalf("expected a type absent from the library to normalize to any, got %q", got)
	}
	if got := normalizeMediaTypeFilter("movie", available); got != "movie" {
		t.Fatalf("expected an available type to pass through, got %q", got)
	}
}

func TestMediaSourceForSuppressesRootLabelWithSingleRoot(t *testing.T) {
	roots := map[string][]string{"Movies": {"/data/movies"}}
	media := model.Media{ServiceName: "Movies", Path: "/data/movies/Film (2024)/film.mkv"}
	source := mediaSourceFor(media, roots)
	if source.Label != "Movies" {
		t.Fatalf("expected no root suffix for a single-root service, got %q", source.Label)
	}
}

func TestMediaSourceForAddsRootLabelWithMultipleRoots(t *testing.T) {
	roots := map[string][]string{"Movies": {"/data/movies", "/data/movies-4k"}}
	media := model.Media{ServiceName: "Movies", Path: "/data/movies-4k/Film (2024)/film.mkv"}
	source := mediaSourceFor(media, roots)
	if source.Label != "Movies · movies-4k" {
		t.Fatalf("expected a root-qualified label for a multi-root service, got %q", source.Label)
	}
	other := mediaSourceFor(model.Media{ServiceName: "Movies", Path: "/data/movies/Other (2020)/other.mkv"}, roots)
	if other.Key == source.Key {
		t.Fatalf("expected distinct sources for distinct roots of the same service")
	}
}
