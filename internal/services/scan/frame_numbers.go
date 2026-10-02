package scan

import (
	"bytes"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var trailingNumber = regexp.MustCompile(`(\d+)\D*$`)

// FrameNumberFromName reads the frame number from a scan file name, e.g. "000012.jpg" -> 12.
func FrameNumberFromName(fileName string) (int, bool) {
	baseName := strings.TrimSuffix(path.Base(fileName), path.Ext(fileName))
	match := trailingNumber.FindStringSubmatch(baseName)
	if match == nil {
		return 0, false
	}
	number, err := strconv.Atoi(match[1])
	return number, err == nil
}

// hasImageExtension is a cheap pre-upload check used by the import preview.
func hasImageExtension(fileName string) bool {
	switch strings.ToLower(path.Ext(fileName)) {
	case ".jpg", ".jpeg", ".png", ".tif", ".tiff", ".webp":
		return true
	}
	return false
}

// detectImageType sniffs the content type from the first bytes of a file,
// returning "" when it is not a supported image.
func detectImageType(head []byte) string {
	if bytes.HasPrefix(head, []byte("II*\x00")) || bytes.HasPrefix(head, []byte("MM\x00*")) {
		return "image/tiff"
	}
	switch contentType := http.DetectContentType(head); contentType {
	case "image/jpeg", "image/png", "image/webp":
		return contentType
	}
	return ""
}

var imageExtensions = map[string]string{
	"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp", "image/tiff": ".tif",
}

// buildImportPreview maps file names to frame numbers (UC-30 steps 4-6, UC-31).
// With StartFrame, files are numbered sequentially in file-name order; otherwise the number
// is read from each name and Offset is added.
func buildImportPreview(input ImportPreviewInput) []ImportPreviewItem {
	items := make([]ImportPreviewItem, len(input.FileNames))

	rankByIndex := map[int]int{}
	if input.StartFrame != nil {
		order := make([]int, len(input.FileNames))
		for index := range order {
			order[index] = index
		}
		sort.SliceStable(order, func(left, right int) bool {
			return input.FileNames[order[left]] < input.FileNames[order[right]]
		})
		for rank, index := range order {
			rankByIndex[index] = rank
		}
	}
	offset := 0
	if input.Offset != nil {
		offset = *input.Offset
	}

	for index, fileName := range input.FileNames {
		item := ImportPreviewItem{FileName: fileName}
		switch {
		case !hasImageExtension(fileName):
			item.Problem = "not an image file"
		case input.StartFrame != nil:
			number := *input.StartFrame + rankByIndex[index]
			item.FrameNumber = &number
		default:
			number, found := FrameNumberFromName(fileName)
			switch {
			case !found:
				item.Problem = "no frame number in file name"
			case number+offset < 0:
				item.Problem = "offset makes the frame number negative"
			default:
				shifted := number + offset
				item.FrameNumber = &shifted
			}
		}
		items[index] = item
	}
	return items
}
