package lens

const apertureDecimalScale = 10

type Input struct {
	Brand       *string
	Model       *string
	Mount       *string
	Description *string
	FocalLength int
	MaxAperture float64
}
