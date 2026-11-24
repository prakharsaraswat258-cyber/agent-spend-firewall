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
	outChars int64 // generated text seen in stream deltas, for estimating cut-off streams
	billable bool  // upstream answered 200: the provider charges for this call
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

// cutUsage is the usage for a stream that ended early (client hung up, or the
// upstream connection dropped). The provider still bills input and the output it
// generated, but the final usage event never arrived. Missing numbers are estimated
// on the high side: ~3 characters per token, and the request size for unknown input.
// estimated is true when any number was guessed.
func (m *meter) cutUsage(reqBytes int64) (u price.Usage, estimated bool) {
	u = m.usage()
	if est := m.outChars/3 + 1; est > u.Output {
		u.Output, estimated = est, true
	}
	if u.Input+u.CacheRead+u.CacheWrite == 0 {
		u.Input, estimated = reqBytes/3+1, true
	}
	return u, estimated
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
	// Anthropic splits cache writes by lifetime; 1-hour writes cost more.
	CacheCreation *struct {
		FiveMin int64 `json:"ephemeral_5m_input_tokens"`
		OneHour int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

type envelope struct {
	Usage   *usageObj `json:"usage"`
	Message *struct {
		Usage *usageObj `json:"usage"`
	} `json:"message"`
	Response *struct {
		Usage *usageObj `json:"usage"`
	} `json:"response"`
	// Generated text, only used to estimate output of a stream that was cut off.
	// Anthropic: {"delta":{"text"|"partial_json"|"thinking":...}}; OpenAI Responses: {"delta":"..."}.
	Delta   json.RawMessage `json:"delta"`
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Function struct {
					Args string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
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
	if m.stream {
		m.outChars += deltaChars(e)
	}
}

func deltaChars(e envelope) int64 {
	var n int64
	if len(e.Delta) > 0 {
		var s string
		var d struct {
			Text        string `json:"text"`
			PartialJSON string `json:"partial_json"`
			Thinking    string `json:"thinking"`
		}
		if json.Unmarshal(e.Delta, &s) == nil {
			n += int64(len(s))
		} else if json.Unmarshal(e.Delta, &d) == nil {
			n += int64(len(d.Text) + len(d.PartialJSON) + len(d.Thinking))
		}
	}
	for _, c := range e.Choices {
		n += int64(len(c.Delta.Content))
		for _, tc := range c.Delta.ToolCalls {
			n += int64(len(tc.Function.Args))
		}
	}
	return n
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
	write, write1h := o.CacheCreate, int64(0)
	if cc := o.CacheCreation; cc != nil && cc.FiveMin+cc.OneHour > 0 {
		// The split is authoritative when present; the total field equals its sum.
		write, write1h = cc.FiveMin, cc.OneHour
	}
	m.u.Input = max(m.u.Input, in)
	m.u.Output = max(m.u.Output, out)
	m.u.CacheRead = max(m.u.CacheRead, cached)
	m.u.CacheWrite = max(m.u.CacheWrite, write)
	m.u.CacheWrite1h = max(m.u.CacheWrite1h, write1h)
}
