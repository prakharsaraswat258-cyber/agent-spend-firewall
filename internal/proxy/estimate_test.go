package proxy

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestImagesReservedByAllowanceNotBytes(t *testing.T) {
	img := strings.Repeat("A", 5_000_000) // 5 MB of base64
	anth := `{"model":"m","messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + img + `"}},{"type":"text","text":"what is this"}]}]}`
	got := estimateInputTokens([]byte(anth))
	if got < imageTokens || got > imageTokens+200 {
		t.Fatalf("anthropic image estimate %d", got)
	}
	oai := `{"model":"m","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/jpeg;base64,` + img + `"}}]}]}`
	if got := estimateInputTokens([]byte(oai)); got < imageTokens || got > imageTokens+200 {
		t.Fatalf("openai image estimate %d", got)
	}
}

func TestPDFReservedPerPage(t *testing.T) {
	pdf := "%PDF-1.4\n1 0 obj << /Type /Pages /Count 3 >>\n2 0 obj << /Type /Page >>\n3 0 obj << /Type /Page >>\n4 0 obj << /Type /Page >>\n" + strings.Repeat("x", 300_000)
	body := `{"messages":[{"role":"user","content":[{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"` + base64.StdEncoding.EncodeToString([]byte(pdf)) + `"}}]}]}`
	if got := estimateInputTokens([]byte(body)); got < 3*pdfPageTokens || got > 3*pdfPageTokens+100 {
		t.Fatalf("pdf estimate %d, want ~%d", got, 3*pdfPageTokens)
	}
}

func TestPlainTextStillCountedHigh(t *testing.T) {
	body := `{"messages":[{"role":"user","content":"` + strings.Repeat("word ", 3000) + `"}]}`
	if got := estimateInputTokens([]byte(body)); got < 3000 { // 15 KB of text: real ~3000 tokens
		t.Fatalf("text under-counted: %d", got)
	}
}
