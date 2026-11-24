package proxy

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strings"
)

// Upper bounds for the pre-call reservation. Images are billed by pixels, not by
// their base64 size, so counting their bytes as text over-reserves ~1000x and can
// block a cheap call under a small cap.
const (
	// imageTokens covers one image on any supported model, including high-resolution
	// modes (Anthropic: about width x height / 750 after resizing, a few thousand at most).
	imageTokens = 6000
	// pdfPageTokens covers one PDF page: its extracted text plus its page image.
	pdfPageTokens = 6000
)

var pdfPage = regexp.MustCompile(`/Type\s*/Page[^s]`)

// estimateInputTokens is a high-side input token count for the reservation:
// text at ~3 bytes per token (over-counts), plus a fixed allowance per image and
// per PDF page instead of their encoded size.
func estimateInputTokens(body []byte) int64 {
	var v any
	if json.Unmarshal(body, &v) != nil {
		return int64(len(body))/3 + 1
	}
	var media, allowance int64 // encoded bytes removed from the text count, tokens added back
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case map[string]any:
			// Anthropic: {"type":"base64","media_type":"image/png"|"application/pdf","data":"..."}
			if t["type"] == "base64" {
				if data, ok := t["data"].(string); ok {
					mt, _ := t["media_type"].(string)
					media += int64(len(data))
					allowance += mediaTokens(mt, data)
					return
				}
			}
			for _, c := range t {
				walk(c)
			}
		case []any:
			for _, c := range t {
				walk(c)
			}
		case string:
			// OpenAI: image_url / input_image / input_file as data: URIs.
			if mt, data, ok := dataURI(t); ok {
				media += int64(len(t))
				allowance += mediaTokens(mt, data)
			}
		}
	}
	walk(v)
	return max(int64(len(body))-media, 0)/3 + 1 + allowance
}

func dataURI(s string) (mediaType, data string, ok bool) {
	rest, ok := strings.CutPrefix(s, "data:")
	if !ok {
		return "", "", false
	}
	head, data, ok := strings.Cut(rest, ",")
	if !ok || !strings.HasSuffix(head, ";base64") {
		return "", "", false
	}
	return strings.TrimSuffix(head, ";base64"), data, true
}

func mediaTokens(mediaType, data string) int64 {
	switch {
	case strings.HasPrefix(mediaType, "image/"):
		return imageTokens
	case mediaType == "application/pdf":
		raw, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			return int64(len(data))/3 + 1
		}
		pages := int64(len(pdfPage.FindAllIndex(raw, -1)))
		if pages == 0 || bytes.Contains(raw, []byte("/ObjStm")) && pages < 2 {
			// Page objects hidden in compressed object streams: fall back to the size.
			return int64(len(data))/3 + 1
		}
		return pages * pdfPageTokens
	default:
		return int64(len(data))/3 + 1 // unknown media: keep the conservative size count
	}
}
