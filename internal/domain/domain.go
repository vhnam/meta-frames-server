// Package domain holds the vocabulary shared by every layer: statuses, job types, scanners
// and small value types.
package domain

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

// Scanners.
const (
	ScannerNoritsu  = "noritsu"
	ScannerFrontier = "frontier"
	ScannerOther    = "other"
)

type ExpiryMonth struct {
	Year  int32
	Month *int32 // nil when the month is unknown
}
