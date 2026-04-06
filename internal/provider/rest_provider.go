package provider

import (
	"context"

	"github.com/dumanproxy/duman/internal/crypto"
	"github.com/dumanproxy/duman/internal/restapi"
)

// RestProvider implements Provider using the REST API facade.
type RestProvider struct {
	client  *restapi.Client
	config  RestProviderConfig
	healthy bool
}

// RestProviderConfig configures a REST provider.
type RestProviderConfig struct {
	BaseURL      string
	APIKey       string
	SharedSecret []byte
}

// NewRestProvider creates a new REST API provider.
func NewRestProvider(cfg RestProviderConfig) *RestProvider {
	client := restapi.NewClient(restapi.ClientConfig{
		BaseURL:      cfg.BaseURL,
		APIKey:       cfg.APIKey,
		SharedSecret: cfg.SharedSecret,
	})
	return &RestProvider{
		client: client,
		config: cfg,
	}
}

func (p *RestProvider) Connect(ctx context.Context) error {
	if err := p.client.Connect(ctx); err != nil {
		return err
	}
	p.healthy = true
	return nil
}

func (p *RestProvider) SendQuery(query string) error {
	return p.client.SendQuery(query)
}

func (p *RestProvider) SendTunnelInsert(chunk *crypto.Chunk, sessionID string, authToken string) error {
	return p.client.SendTunnelInsert(chunk, sessionID, authToken)
}

func (p *RestProvider) FetchResponses(sessionID string) ([]*crypto.Chunk, error) {
	return p.client.FetchResponses(sessionID)
}

func (p *RestProvider) Close() error {
	p.healthy = false
	return p.client.Close()
}

func (p *RestProvider) Type() string {
	return "rest"
}

func (p *RestProvider) IsHealthy() bool {
	return p.healthy
}
