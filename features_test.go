package libext

import (
	"testing"
)

// TestGetFeatureStatus tests feature status lookup.
func TestGetFeatureStatus(t *testing.T) {
	tests := []struct {
		flagType     string
		flagValue    uint32
		expectStatus FeatureStatus
		name         string
	}{
		{"incompat", 0x0002, FeatureStatusSupported, "FILETYPE"},
		{"incompat", 0x0040, FeatureStatusSupported, "EXTENTS"},
		{"incompat", 0x0080, FeatureStatusSupported, "64BIT"},
		{"incompat", 0x0001, FeatureStatusUnsupported, "COMPRESSION"},
		{"ro_compat", 0x0001, FeatureStatusSupported, "SPARSE_SUPER"},
		{"ro_compat", 0x0002, FeatureStatusSupported, "LARGE_FILE"},
		{"ro_compat", 0x0400, FeatureStatusSupported, "METADATA_CSUM"},
		{"compat", 0x0020, FeatureStatusSupported, "DIR_INDEX"},
		{"unknown", 0xFFFF, FeatureStatusUnsupported, "UNKNOWN"},
	}

	for _, tt := range tests {
		status, _ := GetFeatureStatus(tt.flagType, tt.flagValue)
		if status != tt.expectStatus {
			t.Errorf("GetFeatureStatus(%s, 0x%x): got %v, want %v",
				tt.flagType, tt.flagValue, status, tt.expectStatus)
		}
	}
}

// TestCheckRequiredFeaturesPass tests successful feature validation.
func TestCheckRequiredFeaturesPass(t *testing.T) {
	fs := &FS{
		sb: Superblock{
			FeatureIncompat: 0x0002 | 0x0040, // FILETYPE and EXTENTS (both supported)
		},
	}

	err := fs.CheckRequiredFeatures()
	if err != nil {
		t.Errorf("CheckRequiredFeatures: expected nil, got %v", err)
	}
}

// TestCheckRequiredFeaturesFail tests unsupported feature detection.
func TestCheckRequiredFeaturesFail(t *testing.T) {
	fs := &FS{
		sb: Superblock{
			FeatureIncompat: 0x0001, // COMPRESSION (unsupported)
		},
	}

	err := fs.CheckRequiredFeatures()
	if err == nil {
		t.Error("CheckRequiredFeatures: expected error for unsupported COMPRESSION, got nil")
	}
}

// TestCheckRequiredFeaturesMultipleUnsupported tests multiple unsupported features.
func TestCheckRequiredFeaturesMultipleUnsupported(t *testing.T) {
	fs := &FS{
		sb: Superblock{
			FeatureIncompat: 0x0001 | 0x8000, // COMPRESSION and INLINE_DATA (both unsupported)
		},
	}

	err := fs.CheckRequiredFeatures()
	if err == nil {
		t.Error("CheckRequiredFeatures: expected error for multiple unsupported features, got nil")
	}
}

// TestCheckOptionalFeaturesEmpty tests empty optional features.
func TestCheckOptionalFeaturesEmpty(t *testing.T) {
	fs := &FS{
		sb: Superblock{
			FeatureROCompat: 0x0001 | 0x0002, // SPARSE_SUPER and LARGE_FILE (supported)
			FeatureCompat:   0,
		},
	}

	partial := fs.CheckOptionalFeatures()
	if len(partial) != 0 {
		t.Errorf("CheckOptionalFeatures: expected 0 partial features, got %d", len(partial))
	}
}

// TestCheckOptionalFeaturesPartial tests partial features detection.
func TestCheckOptionalFeaturesPartial(t *testing.T) {
	fs := &FS{
		sb: Superblock{
			FeatureROCompat: 0x0008, // HUGE_FILE (partial)
			FeatureCompat:   0x0004, // HAS_JOURNAL (partial)
			FeatureIncompat: 0,
		},
	}

	partial := fs.CheckOptionalFeatures()
	if len(partial) < 2 {
		t.Errorf("CheckOptionalFeatures: expected at least 2 partial features, got %d", len(partial))
	}
}

// TestDescribeFeatures tests feature description.
func TestDescribeFeatures(t *testing.T) {
	fs := &FS{
		sb: Superblock{
			FeatureCompat:   0x0020, // DIR_INDEX
			FeatureIncompat: 0x0040, // EXTENTS
			FeatureROCompat: 0x0400, // METADATA_CSUM
		},
	}

	desc := fs.DescribeFeatures()
	if len(desc) == 0 {
		t.Error("DescribeFeatures: expected non-empty description, got empty")
	}

	// Check that key features appear in description
	if !contains(desc, "DIR_INDEX") {
		t.Error("DescribeFeatures: missing DIR_INDEX")
	}
	if !contains(desc, "EXTENTS") {
		t.Error("DescribeFeatures: missing EXTENTS")
	}
	if !contains(desc, "METADATA_CSUM") {
		t.Error("DescribeFeatures: missing METADATA_CSUM")
	}
}

// TestFindUnsupportedFeatures tests unsupported features detection.
func TestFindUnsupportedFeatures(t *testing.T) {
	// COMPRESSION (0x0001) and JOURNAL_DEV (0x0008) are both unsupported.
	// INLINE_DATA is deliberately not used here: it is now supported.
	flags := uint32(0x0001 | 0x0008)
	unsupported := findUnsupportedFeatures(flags, "incompat")

	if len(unsupported) != 2 {
		t.Errorf("findUnsupportedFeatures: expected 2 features, got %d: %v",
			len(unsupported), unsupported)
	}
}

// TestFindPartialFeatures tests partial features detection.
func TestFindPartialFeatures(t *testing.T) {
	// HAS_JOURNAL (0x0004) is partial, IMAGIC_INODES (0x0002) is partial
	flags := uint32(0x0004 | 0x0002)
	partial := findPartialFeatures(flags, "compat")

	if len(partial) != 2 {
		t.Errorf("findPartialFeatures: expected 2 features, got %d", len(partial))
	}
}

// TestAllFeaturesValid tests that all features in AllFeatures are properly defined.
func TestAllFeaturesValid(t *testing.T) {
	seenCompat := make(map[uint32]bool)
	seenIncompat := make(map[uint32]bool)
	seenROCompat := make(map[uint32]bool)

	for _, f := range AllFeatures {
		if f.Name == "" {
			t.Error("Feature has empty name")
		}

		switch f.FlagType {
		case "compat":
			if seenCompat[f.FlagValue] {
				t.Errorf("Duplicate compat feature: %s (0x%x)", f.Name, f.FlagValue)
			}
			seenCompat[f.FlagValue] = true
		case "incompat":
			if seenIncompat[f.FlagValue] {
				t.Errorf("Duplicate incompat feature: %s (0x%x)", f.Name, f.FlagValue)
			}
			seenIncompat[f.FlagValue] = true
		case "ro_compat":
			if seenROCompat[f.FlagValue] {
				t.Errorf("Duplicate ro_compat feature: %s (0x%x)", f.Name, f.FlagValue)
			}
			seenROCompat[f.FlagValue] = true
		default:
			t.Errorf("Invalid flag type: %s", f.FlagType)
		}

		if f.Status < FeatureStatusSupported || f.Status > FeatureStatusUnsupported {
			t.Errorf("Invalid feature status for %s", f.Name)
		}
	}
}

// Helper function to check if string contains substring
func contains(s, substr string) bool {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
