package domain

import (
	"encoding/json"
	"testing"
)

func TestCheckScanSettings(test *testing.T) {
	camera, minilab, unknown := ScannerKindCamera, ScannerKindMinilab, "phone"
	decoded := ScanSettings{}
	if err := json.Unmarshal([]byte(`{"pixel_shift": true, "stitch_shots": 4, "format": "raw"}`), &decoded); err != nil {
		test.Fatal(err)
	}
	for name, testCase := range map[string]struct {
		kind     *string
		settings ScanSettings
		ok       bool
	}{
		"camera settings from JSON":      {&camera, decoded, true},
		"no settings":                    {&camera, nil, true},
		"integer set in Go":              {&camera, ScanSettings{"stitch_shots": 2}, true},
		"option of another kind":         {&camera, ScanSettings{"resolution": ResolutionHiRes}, false},
		"enum value not offered":         {&minilab, ScanSettings{"format": "png"}, false},
		"boolean given as text":          {&camera, ScanSettings{"pixel_shift": "yes"}, false},
		"framing on every kind":          {&camera, ScanSettings{"framing": FramingSprockets}, true},
		"framing not offered":            {&minilab, ScanSettings{"framing": "polaroid"}, false},
		"integer above max":              {&camera, ScanSettings{"stitch_shots": 17}, false},
		"integer with a fraction":        {&camera, ScanSettings{"stitch_shots": 1.5}, false},
		"scanner without a kind":         {nil, ScanSettings{"resolution": ResolutionHiRes}, true},
		"kind the catalog does not know": {&unknown, ScanSettings{"resolution": ResolutionHiRes}, false},
	} {
		if err := CheckScanSettings(testCase.kind, testCase.settings); (err == nil) != testCase.ok {
			test.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestScanSettingsRoundTrip(test *testing.T) {
	settings := ScanSettings{"dpi": 3200, "dust_removal": true}
	if !SameScanSettings(DecodeScanSettings(EncodeScanSettings(settings)), settings) {
		test.Fatal("settings must survive JSON")
	}
	if len(DecodeScanSettings([]byte("not json"))) != 0 || string(EncodeScanSettings(nil)) != "{}" {
		test.Fatal("bad or missing settings read as none")
	}
	if !IsHiRes(ScanSettings{"resolution": ResolutionHiRes}) || IsHiRes(nil) {
		test.Fatal("IsHiRes")
	}
}
