package proxy

import (
	"testing"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/price"
)

func TestOpenAIStreamCachedTokensBilledOnce(t *testing.T) {
	m := &meter{provider: "openai", stream: true}
	// Chunks split mid-line on purpose: the meter must reassemble lines.
	for _, c := range []string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\nda",
		"ta: {\"choices\":[],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":50,",
		"\"prompt_tokens_details\":{\"cached_tokens\":400}}}\n\ndata: [DONE]\n\n",
	} {
		m.write([]byte(c))
	}
	u := m.usage()
	if u != (price.Usage{Input: 600, Output: 50, CacheRead: 400}) {
		t.Fatalf("usage %+v", u)
	}
	e, _ := price.Default().Lookup("gpt-4o")
	// 600*$2.50 + 400*$1.25 cached + 50*$10 = 1500 + 500 + 500 micro-dollars
	if c := e.Cost(u); c != 2500 {
		t.Fatalf("cost %d", c)
	}
}
