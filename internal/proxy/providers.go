package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/ShadowIzzHerZ/agent-spend-firewall/internal/price"
)

// Provider is a named upstream reached at /p/<name>/...: an OpenAI-compatible
// service (OpenRouter, Gemini, Perplexity, a company gateway) or an
// Anthropic-compatible gateway. Same meter, caps and pauses as the built-in routes.
type Provider struct {
	BaseURL string `json:"base_url"`
	// Kind is the API format: "openai" (default) or "anthropic".
	Kind string `json:"kind,omitempty"`
	// UseOfficialPrices lets a model ID without a "<name>/<model>" price use the
	// official price for the same ID. Off by default: a reseller may charge more.
	UseOfficialPrices bool `json:"use_official_prices,omitempty"`

	url *url.URL
}

var providerName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// LoadProviders reads providers.json: {"providers":{"openrouter":{"base_url":"https://openrouter.ai/api/v1"}}}.
// A missing file means no named providers; an invalid one is an error (serve refuses to start).
func LoadProviders(path string) (map[string]*Provider, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f struct {
		Providers map[string]*Provider `json:"providers"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for name, pr := range f.Providers {
		if !providerName.MatchString(name) {
			return nil, fmt.Errorf("%s: provider name %q: use letters, digits, '.', '_' or '-'", path, name)
		}
		if pr == nil {
			return nil, fmt.Errorf("%s: provider %q is empty", path, name)
		}
		if pr.Kind == "" {
			pr.Kind = "openai"
		}
		if pr.Kind != "openai" && pr.Kind != "anthropic" {
			return nil, fmt.Errorf("%s: provider %q: kind must be \"openai\" or \"anthropic\"", path, name)
		}
		u, err := url.Parse(pr.BaseURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return nil, fmt.Errorf("%s: provider %q: base_url must be a full http(s) URL", path, name)
		}
		if u.Scheme == "http" && !isLocalHost(u.Hostname()) {
			return nil, fmt.Errorf("%s: provider %q: base_url must use https (keys pass through it); plain http is only allowed for localhost", path, name)
		}
		u.Path = strings.TrimSuffix(u.Path, "/")
		pr.url = u
	}
	return f.Providers, nil
}

func isLocalHost(h string) bool { return h == "localhost" || h == "127.0.0.1" || h == "::1" }

// metered reports whether a request to this provider is a priced model call.
// rest is the path after /p/<name>, relative to base_url.
func (pr *Provider) metered(method, rest string) bool {
	if method != http.MethodPost {
		return false
	}
	if pr.Kind == "anthropic" {
		return rest == "/v1/messages" || rest == "/messages"
	}
	return strings.HasSuffix(rest, "/chat/completions") || strings.HasSuffix(rest, "/responses")
}

// lookupFor prices a model for a provider: "<name>/<model>" first, then the plain
// ID only when the provider allows official prices. Unknown means blocked.
func (p *Proxy) lookupFor(name string, pr *Provider, model string) (price.Entry, error) {
	if pr == nil {
		return p.Prices.Lookup(model)
	}
	if e, err := p.Prices.Lookup(name + "/" + model); err == nil {
		return e, nil
	}
	if pr.UseOfficialPrices {
		if e, err := p.Prices.Lookup(model); err == nil {
			return e, nil
		}
	}
	return price.Entry{}, fmt.Errorf("Pitstop has no price for model %q on provider %s, so the call is blocked. Fix: add the provider's published price: pitstop prices add %s/%s --input <USD per MTok> --output <USD per MTok>", model, name, name, model)
}
