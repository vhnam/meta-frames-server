package scanner

import "meta-frames-server/internal/domain"

// Input is a scanner as the account describes it. Brand is required; Model, Kind and Notes are
// optional, since a lab may run a Noritsu whose model nobody knows. DefaultSettings are the
// settings its scan orders start from, checked against the options of its kind.
type Input struct {
	Brand           string
	Model           *string
	Kind            *string
	Notes           *string
	DefaultSettings domain.ScanSettings
}
