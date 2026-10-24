package mcp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// AgentBriefURI is the resource with Pantech's guide for AI agents.
const AgentBriefURI = "pantech://docs/agent-brief"

// DefaultDocsURL is where the agent brief is published.
const DefaultDocsURL = "https://docs.pantechdynamics.com/agent-brief.md"

const (
	briefMaxBytes = 512 << 10
	briefTTL      = 15 * time.Minute
)

// briefCache keeps the last good copy of the agent brief for briefTTL, so
// a busy server does not fetch it on every read.
type briefCache struct {
	url    string
	client *http.Client
	mu     sync.Mutex
	text   string
	at     time.Time
}

func (b *briefCache) get(ctx context.Context) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.text != "" && time.Since(b.at) < briefTTL {
		return b.text, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "pantech-mcp/"+Version)
	res, err := b.client.Do(req)
	if err != nil {
		return b.stale(fmt.Errorf("the agent brief could not be fetched"))
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return b.stale(fmt.Errorf("the agent brief could not be fetched (status %d)", res.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, briefMaxBytes))
	if err != nil {
		return b.stale(fmt.Errorf("the agent brief could not be read"))
	}
	b.text, b.at = string(body), time.Now()
	return b.text, nil
}

// stale serves the last good copy when a refresh fails.
func (b *briefCache) stale(err error) (string, error) {
	if b.text != "" {
		return b.text, nil
	}
	return "", err
}

func addResources(s *sdk.Server, opts Options) {
	url := opts.DocsURL
	if url == "" {
		url = DefaultDocsURL
	}
	cache := &briefCache{url: url, client: &http.Client{Timeout: 15 * time.Second}}
	s.AddResource(&sdk.Resource{
		URI:         AgentBriefURI,
		Name:        "agent-brief",
		Title:       "Pantech agent brief",
		Description: "How Pantech Dynamics works, for AI agents: resources, regions, billing and safe practice.",
		MIMEType:    "text/markdown",
	}, func(ctx context.Context, req *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
		text, err := cache.get(ctx)
		if err != nil {
			return nil, err
		}
		return &sdk.ReadResourceResult{Contents: []*sdk.ResourceContents{{URI: AgentBriefURI, MIMEType: "text/markdown", Text: text}}}, nil
	})
}
