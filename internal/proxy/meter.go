package proxy

import (
	"bytes"
	"encoding/json"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/price"
)

// meter extracts token usage from a response while it streams to the client.
type meter struct {
	provider string
	stream   bool
	buf      bytes.Buffer
	u        price.Usage
}

func (m *meter) write(p []byte) {
	m.buf.Write(p)
	if !m.stream {
		return
	}
	for {
		i := bytes.IndexByte(m.buf.Bytes(), '\n')
		if i < 0 {
			return
		}
		line := bytes.TrimSpace(m.buf.Next(i + 1))
		if rest, ok := bytes.CutPrefix(line, []byte("data:")); ok {
			m.absorb(bytes.TrimSpace(rest))
		}
	}
}

func (m *meter) usage() price.Usage {
	if !m.stream {
		m.absorb(m.buf.Bytes())
	}
	return m.u
}

type usageObj struct {
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	CacheRead        int64 `json:"cache_read_input_tokens"`
	CacheCreate      int64 `json:"cache_creation_input_tokens"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	PromptDetails    struct {
		Cached int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	InputDetails struct {
		Cached int64 `json:"cached_tokens"`
	} `json:"input_tokens_details"`
}

type envelope struct {
	Usage   *usageObj `json:"usage"`
	Message *struct {
		Usage *usageObj `json:"usage"`
	} `json:"message"`
	Response *struct {
		Usage *usageObj `json:"usage"`
	} `json:"response"`
}

func (m *meter) absorb(b []byte) {
	var e envelope
	if json.Unmarshal(b, &e) != nil {
		return
	}
	for _, uo := range []*usageObj{e.Usage, msgUsage(e), respUsage(e)} {
		if uo != nil {
			m.merge(uo)
		}
	}
}

func msgUsage(e envelope) *usageObj {
	if e.Message != nil {
		return e.Message.Usage
	}
	return nil
}

func respUsage(e envelope) *usageObj {
	if e.Response != nil {
		return e.Response.Usage
	}
	return nil
}

// merge keeps the largest value seen per field: Anthropic sends input in
// message_start and cumulative output in message_delta.
func (m *meter) merge(o *usageObj) {
	cached := max(o.CacheRead, o.PromptDetails.Cached, o.InputDetails.Cached)
	in := o.InputTokens
	if o.PromptTokens > 0 {
		in = o.PromptTokens
	}
	if m.provider == "openai" {
		in -= cached // OpenAI input counts cached tokens; bill them at the cached rate only
		if in < 0 {
			in = 0
		}
	}
	out := max(o.OutputTokens, o.CompletionTokens)
	m.u.Input = max(m.u.Input, in)
	m.u.Output = max(m.u.Output, out)
	m.u.CacheRead = max(m.u.CacheRead, cached)
	m.u.CacheWrite = max(m.u.CacheWrite, o.CacheCreate)
}
