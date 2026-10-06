package index

import (
	"os"
	"testing"
)

func TestSearchAndScoring(t *testing.T) {
	lib := NewLibrary("test_cache.json")
	defer os.Remove("test_cache.json")

	tracks := []*Track{
		{
			ID:       "1",
			Title:    "SexyBack (feat. Timbaland)",
			Artist:   "Justin Timberlake",
			Format:   "flac",
			Duration: 243,
			ISRC:     "USJCE0600401",
		},
		{
			ID:       "2",
			Title:    "SexyBack (feat. Timbaland) (Dolby Atmos)",
			Artist:   "Justin Timberlake",
			Format:   "eac3-joc",
			IsAtmos:  true,
			Duration: 243,
			ISRC:     "USJCE0600401",
		},
		{
			ID:       "3",
			Title:    "I Was Made For Lovin' You",
			Artist:   "KISS",
			Format:   "flac",
			Duration: 270,
			ISRC:     "USPR37901004",
		},
	}

	lib.SetTracks(tracks)

	// Test 1: Exact search
	results := lib.Search("sexyback justin timberlake", false, 0)
	if len(results) == 0 || (results[0].ID != "1" && results[0].ID != "2") {
		t.Fatalf("Expected hit for sexyback, got: %v", results)
	}

	// Test 2: Search with Atmos preference
	resultsAtmos := lib.Search("sexyback justin timberlake", true, 0)
	if len(resultsAtmos) == 0 || resultsAtmos[0].ID != "2" {
		t.Fatalf("Expected Atmos version (ID 2) to be prioritized when atmos=auto, got: %v", resultsAtmos)
	}

	// Test 3: ISRC lookup
	isrcMatches := lib.LookupISRC("USPR37901004")
	if len(isrcMatches) != 1 || isrcMatches[0].ID != "3" {
		t.Fatalf("Expected ISRC match ID 3, got: %v", isrcMatches)
	}

	// Test 4: Cache save and load
	if err := lib.SaveCache(); err != nil {
		t.Fatalf("SaveCache failed: %v", err)
	}

	lib2 := NewLibrary("test_cache.json")
	if err := lib2.LoadCache(); err != nil {
		t.Fatalf("LoadCache failed: %v", err)
	}

	if lib2.Count() != 3 {
		t.Errorf("Expected 3 tracks in loaded cache, got %d", lib2.Count())
	}
}

