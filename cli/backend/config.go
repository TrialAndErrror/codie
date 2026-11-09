package backend

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Environment variable names.
const (
	EnvAPIKey  = "OPENROUTER_API_KEY"
	EnvModel   = "CODIE_MODEL"
	EnvBaseURL = "CODIE_BASE_URL"
)

// DefaultBaseURL is OpenRouter's OpenAI-compatible API root.
const DefaultBaseURL = "https://openrouter.ai/api/v1"

// Config is everything needed to reach a model.
type Config struct {
	APIKey  string
	Model   string
	BaseURL string
}

// ConfigFromEnv reads the config from environment variables. getenv is
// os.Getenv in production and a map lookup in tests.
func ConfigFromEnv(getenv func(string) string) Config {
	c := Config{
		APIKey:  strings.TrimSpace(getenv(EnvAPIKey)),
		Model:   strings.TrimSpace(getenv(EnvModel)),
		BaseURL: strings.TrimSpace(getenv(EnvBaseURL)),
	}
	if c.BaseURL == "" {
		c.BaseURL = DefaultBaseURL
	}
	return c
}

// Validate reports the first problem that would stop a request, in words a
// user can act on. It never includes the API key in its output.
func (c Config) Validate() error {
	if c.APIKey == "" {
		return fmt.Errorf("%s is not set. Export your OpenRouter key, or run with --demo to try sample answers", EnvAPIKey)
	}
	if c.Model == "" {
		return fmt.Errorf("no model chosen. Set %s (or pass --model) to an OpenRouter model ID that supports tool calling", EnvModel)
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%s is not a valid URL: %q", EnvBaseURL, c.BaseURL)
	}
	switch u.Scheme {
	case "https":
	case "http":
		// The key goes in a header on every request, so refuse to send it in
		// the clear anywhere except the local machine (used for testing).
		if !isLoopback(u.Hostname()) {
			return errors.New("refusing to send your API key over plain http to " + u.Hostname() + ": use https")
		}
	default:
		return fmt.Errorf("%s must start with https:// (got %q)", EnvBaseURL, c.BaseURL)
	}
	return nil
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// MaskedKey describes the key without revealing it: enough to recognise which
// key is in use, never enough to use it.
func (c Config) MaskedKey() string {
	switch {
	case c.APIKey == "":
		return "not set"
	case len(c.APIKey) < 12:
		return "set"
	default:
		return fmt.Sprintf("set (ends …%s)", c.APIKey[len(c.APIKey)-4:])
	}
}
