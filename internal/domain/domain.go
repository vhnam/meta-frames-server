// Package domain holds the vocabulary shared by every layer: statuses, job types, scanners
// and small value types.
package domain

import "github.com/google/uuid"

// Roll lifecycle statuses (see the use-case spec, section 1.3).
const (
	RollStatusInStock      = "in_stock"
	RollStatusInCamera     = "in_camera"
	RollStatusDoneShooting = "done_shooting"
	RollStatusAtLab        = "at_lab"
	RollStatusDeveloped    = "developed"
	RollStatusScanned      = "scanned"
)

// Processing job types.
const (
	JobTypeDevelop     = "develop"
	JobTypeDevelopScan = "develop_scan"
	JobTypeScan        = "scan"
	JobTypePrint       = "print"
)

// Built-in scanners. Their ids are fixed by migration 00011.
var (
	ScannerNoritsuHS1800  = uuid.MustParse("d177cb3c-a850-4232-a4ca-75189b7b279b")
	ScannerFrontierSP3000 = uuid.MustParse("e2bf4cbd-87a8-4a17-8340-516b1a1cc6bc")
)

// Scanner codes are the old way the API named a scanner, kept until clients send scanner ids.
// Every scanner other than the two built-in ones reads as "other".
const (
	ScannerCodeNoritsu  = "noritsu"
	ScannerCodeFrontier = "frontier"
	ScannerCodeOther    = "other"
)

// Scanner kinds.
const (
	ScannerKindMinilab       = "minilab"
	ScannerKindVirtualDrum   = "virtual_drum"
	ScannerKindDrum          = "drum"
	ScannerKindDedicatedFilm = "dedicated_film"
	ScannerKindFlatbed       = "flatbed"
	ScannerKindCamera        = "camera"
)

// ScannerCode is the old code of a scanner.
func ScannerCode(scannerID uuid.UUID) string {
	switch scannerID {
	case ScannerNoritsuHS1800:
		return ScannerCodeNoritsu
	case ScannerFrontierSP3000:
		return ScannerCodeFrontier
	default:
		return ScannerCodeOther
	}
}

// ScannerName is how a scanner is shown: its brand, then its model when it is known.
func ScannerName(brand string, model *string) string {
	if model == nil {
		return brand
	}
	return brand + " " + *model
}

type ExpiryMonth struct {
	Year  int32
	Month *int32 // nil when the month is unknown
}
