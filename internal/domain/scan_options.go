package domain

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
)

// Scan option types: the JSON type of a setting's value.
const (
	ScanOptionEnum    = "enum"    // a string from Values
	ScanOptionBoolean = "boolean" // true or false
	ScanOptionInteger = "integer" // a whole number between Min and Max
)

// ScanOptionValue is one choice of an enum option.
type ScanOptionValue struct {
	Value string
	Label string
}

// ScanOption is a choice made when ordering a scan, such as a minilab's resolution.
type ScanOption struct {
	Key     string
	Label   string
	Type    string
	Values  []ScanOptionValue // enum only
	Min     int               // integer only
	Max     int               // integer only
	Default any               // string, bool or int, matching Type
}

// ScanSettings holds the chosen value of each option, by key.
type ScanSettings map[string]any

// Framing values: how much of the film around the frame a scan shows.
const (
	FramingCropped   = "cropped"
	FramingBorder    = "border"
	FramingSprockets = "sprockets"
)

// Resolution values of a minilab; "hi_res" is what the old hiRes flag meant.
const (
	ResolutionStandard = "standard"
	ResolutionHiRes    = "hi_res"
)

var (
	resolutionOption = ScanOption{
		Key: "resolution", Label: "Resolution", Type: ScanOptionEnum, Default: ResolutionStandard,
		Values: []ScanOptionValue{{ResolutionStandard, "Standard"}, {ResolutionHiRes, "Hi-res"}},
	}
	labFormatOption = ScanOption{
		Key: "format", Label: "File format", Type: ScanOptionEnum, Default: "jpeg",
		Values: []ScanOptionValue{{"jpeg", "JPEG"}, {"tiff", "TIFF"}},
	}
	// framingOption is whether the frame is cropped inside the image, or the film border (and the
	// sprocket holes) show around it. Every kind offers it.
	framingOption = ScanOption{
		Key: "framing", Label: "Framing", Type: ScanOptionEnum, Default: FramingCropped,
		Values: []ScanOptionValue{
			{FramingCropped, "Cropped (no border)"}, {FramingBorder, "With film border"}, {FramingSprockets, "Border and sprockets"},
		},
	}
	dpiOption      = ScanOption{Key: "dpi", Label: "Resolution (dpi)", Type: ScanOptionInteger, Min: 300, Max: 12800, Default: 3200}
	bitDepthOption = ScanOption{
		Key: "bit_depth", Label: "Bit depth", Type: ScanOptionEnum, Default: "48",
		Values: []ScanOptionValue{{"24", "24-bit"}, {"48", "48-bit"}},
	}
	tiffFormatOption = ScanOption{
		Key: "format", Label: "File format", Type: ScanOptionEnum, Default: "tiff",
		Values: []ScanOptionValue{{"tiff", "TIFF"}, {"jpeg", "JPEG"}},
	}
	dustRemovalOption   = ScanOption{Key: "dust_removal", Label: "Dust removal (ICE)", Type: ScanOptionBoolean, Default: false}
	multiExposureOption = ScanOption{Key: "multi_exposure", Label: "Multi-exposure", Type: ScanOptionBoolean, Default: false}
	cameraFormatOption  = ScanOption{
		Key: "format", Label: "File format", Type: ScanOptionEnum, Default: "raw",
		Values: []ScanOptionValue{{"raw", "RAW"}, {"tiff", "TIFF"}, {"jpeg", "JPEG"}},
	}
	pixelShiftOption = ScanOption{Key: "pixel_shift", Label: "Pixel shift", Type: ScanOptionBoolean, Default: false}
	stitchOption     = ScanOption{Key: "stitch_shots", Label: "Shots stitched per frame", Type: ScanOptionInteger, Min: 1, Max: 16, Default: 1}
)

// scanOptionsByKind lists the options of each scanner kind; "" is a scanner without a kind.
var scanOptionsByKind = map[string][]ScanOption{
	"":                       {resolutionOption, labFormatOption, framingOption},
	ScannerKindMinilab:       {resolutionOption, labFormatOption, framingOption},
	ScannerKindFlatbed:       {dpiOption, bitDepthOption, tiffFormatOption, framingOption, dustRemovalOption, multiExposureOption},
	ScannerKindDedicatedFilm: {dpiOption, bitDepthOption, tiffFormatOption, framingOption, dustRemovalOption, multiExposureOption},
	ScannerKindVirtualDrum:   {dpiOption, bitDepthOption, tiffFormatOption, framingOption},
	ScannerKindDrum:          {dpiOption, bitDepthOption, tiffFormatOption, framingOption},
	ScannerKindCamera:        {cameraFormatOption, framingOption, pixelShiftOption, stitchOption},
}

// ScannerKinds is every kind, in the order a picker shows them.
var ScannerKinds = []string{
	ScannerKindMinilab, ScannerKindVirtualDrum, ScannerKindDrum, ScannerKindDedicatedFilm, ScannerKindFlatbed, ScannerKindCamera,
}

// ScanOptionsFor returns the options of a scanner kind; nil is a scanner without a kind.
func ScanOptionsFor(kind *string) []ScanOption {
	if kind == nil {
		return scanOptionsByKind[""]
	}
	return scanOptionsByKind[*kind]
}

// ScanSettingsError explains why settings do not fit the options of a kind.
type ScanSettingsError struct{ Reason string }

func (settingsError *ScanSettingsError) Error() string { return settingsError.Reason }

// CheckScanSettings reports the first setting that is not an option of the kind or has a value
// the option does not allow. Settings may leave options out.
func CheckScanSettings(kind *string, settings ScanSettings) error {
	options := map[string]ScanOption{}
	for _, option := range ScanOptionsFor(kind) {
		options[option.Key] = option
	}
	keys := make([]string, 0, len(settings))
	for key := range settings {
		keys = append(keys, key)
	}
	sort.Strings(keys) // the same settings always report the same problem
	for _, key := range keys {
		option, known := options[key]
		if !known {
			return &ScanSettingsError{Reason: fmt.Sprintf("%s is not an option of this scanner", key)}
		}
		if !option.allows(settings[key]) {
			return &ScanSettingsError{Reason: fmt.Sprintf("%s has a value this scanner does not offer", key)}
		}
	}
	return nil
}

func (option ScanOption) allows(value any) bool {
	switch option.Type {
	case ScanOptionBoolean:
		_, ok := value.(bool)
		return ok
	case ScanOptionInteger:
		number, ok := wholeNumber(value)
		return ok && number >= option.Min && number <= option.Max
	case ScanOptionEnum:
		text, ok := value.(string)
		if !ok {
			return false
		}
		for _, choice := range option.Values {
			if choice.Value == text {
				return true
			}
		}
	}
	return false
}

// wholeNumber accepts an integer as decoded from JSON (float64) or set in Go (int).
func wholeNumber(value any) (int, bool) {
	switch number := value.(type) {
	case int:
		return number, true
	case float64:
		if number != math.Trunc(number) || math.Abs(number) > math.MaxInt32 {
			return 0, false
		}
		return int(number), true
	}
	return 0, false
}

// IsHiRes is what the old hiRes flag reads from settings.
func IsHiRes(settings ScanSettings) bool { return settings["resolution"] == ResolutionHiRes }

// DecodeScanSettings reads settings stored as JSON; anything but an object reads as no settings.
func DecodeScanSettings(raw []byte) ScanSettings {
	settings := ScanSettings{}
	if err := json.Unmarshal(raw, &settings); err != nil || settings == nil {
		return ScanSettings{}
	}
	return settings
}

// EncodeScanSettings writes settings as a JSON object; nil is an empty one.
func EncodeScanSettings(settings ScanSettings) json.RawMessage {
	if settings == nil {
		return json.RawMessage("{}")
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		return json.RawMessage("{}")
	}
	return encoded
}

// SameScanSettings compares settings by value, whether they were decoded from JSON or built in Go.
func SameScanSettings(left, right ScanSettings) bool {
	return reflect.DeepEqual(DecodeScanSettings(EncodeScanSettings(left)), DecodeScanSettings(EncodeScanSettings(right)))
}
