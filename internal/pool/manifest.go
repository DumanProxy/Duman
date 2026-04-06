package pool

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// RelayManifest is a versioned list of relay entries that can be
// loaded from a local file or fetched from a URL.
type RelayManifest struct {
	Version string          `json:"version"`
	Relays  []ManifestEntry `json:"relays"`
}

// ManifestEntry describes a single relay in the manifest.
type ManifestEntry struct {
	Address  string `json:"address"`
	Protocol string `json:"protocol"`
	Tier     int    `json:"tier"`
	Domain   string `json:"domain"`
	Weight   int    `json:"weight"`
}

// LoadManifest reads a relay manifest from a local JSON file.
func LoadManifest(path string) (*RelayManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	var m RelayManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	return &m, nil
}

// LoadManifestURL fetches a relay manifest from a URL with a 30-second timeout.
func LoadManifestURL(url string) (*RelayManifest, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch manifest: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("manifest HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read manifest body: %w", err)
	}

	var m RelayManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	return &m, nil
}

// ToRelayInfos converts manifest entries into pool-ready RelayInfo values.
func (m *RelayManifest) ToRelayInfos() []RelayInfo {
	infos := make([]RelayInfo, 0, len(m.Relays))
	for _, e := range m.Relays {
		tier := Tier(e.Tier)
		if tier < TierCommunity || tier > TierTrusted {
			tier = TierCommunity
		}
		weight := e.Weight
		if weight <= 0 {
			weight = 1
		}
		infos = append(infos, RelayInfo{
			Address:  e.Address,
			Protocol: e.Protocol,
			Tier:     tier,
			State:    StateHealthy,
			Weight:   weight,
		})
	}
	return infos
}
