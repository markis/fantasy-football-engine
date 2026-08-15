package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/creasty/defaults"
	"gopkg.in/yaml.v3"
)

var (
	errEmptySecretName = errors.New("empty secret name")
	errEmptySecret     = errors.New("secret file is empty")
	errSecretEscape    = errors.New("secret name escapes secrets directory")
)

// defaultSecretsDir is where Docker (and most runtimes) mount secrets.
const defaultSecretsDir = "/run/secrets"

// Config is the top-level daemon configuration loaded from YAML.
type Config struct {
	Server      ServerConfig       `yaml:"server"`
	Database    DatabaseConfig     `yaml:"database"`
	Embeddings  EmbeddingsConfig   `yaml:"embeddings"`
	LLM         LLMConfig          `yaml:"llm"`
	Sleeper     SleeperConfig      `yaml:"sleeper"`
	Sources     []SourceConfig     `yaml:"sources"`
	Evergreen   []EvergreenPattern `yaml:"evergreen"`
	FantasyPros FantasyProsConfig  `yaml:"fantasyPros"`
	Corpus      CorpusConfig       `yaml:"corpus"`
	Telemetry   TelemetryConfig    `yaml:"telemetry"`
	Scheduler   SchedulerConfig    `yaml:"scheduler"`
}

type ServerConfig struct {
	MCPAddr string `default:":3100" yaml:"mcpAddr"`
}

type DatabaseConfig struct {
	DSN string `yaml:"dsn"`
}

type EmbeddingsConfig struct {
	Provider       string `default:"llama-server"             yaml:"provider"`
	URL            string `default:"http://llama-server:8080" yaml:"url"`
	Model          string `default:"nomic-embed-text-v1.5"    yaml:"model"`
	Dimensions     int    `default:"768"                      yaml:"dimensions"`
	BatchSize      int    `default:"32"                       yaml:"batchSize"`
	MaxConcurrency int    `default:"8"                        yaml:"maxConcurrency"`
}

type LLMConfig struct {
	Provider       string `default:"ollama-cloud"       yaml:"provider"`
	URL            string `default:"https://ollama.com" yaml:"url"`
	Model          string `default:"minimax-m3"         yaml:"model"`
	APIKey         string `yaml:"apiKey"`
	APIKeySecret   string `default:"llm-api-key"        yaml:"apiKeySecret"`
	TimeoutSecs    int    `default:"300"                yaml:"timeoutSecs"`
	MaxConcurrency int    `default:"4"                  yaml:"maxConcurrency"`
}

type SleeperConfig struct {
	BaseURL         string `default:"https://api.sleeper.app/v1" yaml:"baseUrl"`
	RateLimitPerMin int    `default:"1000"                       yaml:"rateLimitPerMin"`
	Seasons         []int  `default:"[2026]"                     yaml:"seasons"`
}

type SourceConfig struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
}

// EvergreenPattern marks a URL substring whose articles are evergreen
// aggregator/trackers republished with a fresh pubDate but stale body. Items
// whose URL matches are ingested but de-flagged (is_news=false, is_relevant=false)
// so they don't surface as current news. Patterns are matched as substrings
// against the item's canonical URL (case-sensitive).
type EvergreenPattern struct {
	Pattern string `yaml:"pattern"`
	Reason  string `yaml:"reason"`
}

type FantasyProsConfig struct {
	APIKeySecret string `default:"fantasypros-api"     yaml:"apiKeySecret"`
	CookieSecret string `default:"fantasypros-cookies" yaml:"cookieSecret"`
}

type CorpusConfig struct {
	RepoDir      string `yaml:"repoDir"`
	GitURL       string `default:"https://github.com/markis/fantasy-football-corpus" yaml:"gitUrl"`
	GitPAT       string `yaml:"gitPat"`
	GitPATSecret string `default:"fantasy-github-pat"                                yaml:"gitPatSecret"`
	AuthorName   string `default:"Markis Taylor"                                     yaml:"authorName"`
	AuthorEmail  string `default:"m@rkis.net"                                        yaml:"authorEmail"`
}

type TelemetryConfig struct {
	OTelEndpoint string  `yaml:"oTelEndpoint"`
	ServiceName  string  `default:"fantasy-football-engine" yaml:"serviceName"`
	MetricsAddr  string  `yaml:"metricsAddr"`
	Env          string  `default:"production"              yaml:"env"`
	SampleRate   float64 `default:"1.0"                     yaml:"sampleRate"`
}

type SchedulerConfig struct {
	Timezone string      `default:"America/New_York" yaml:"timezone"`
	Jobs     []JobConfig `yaml:"jobs"`
}

type JobConfig struct {
	Name     string `yaml:"name"`
	Schedule string `yaml:"schedule"`
	Step     string `yaml:"step"`
	Limit    int    `yaml:"limit"`
	Market   int    `yaml:"market"`
	Source   string `yaml:"source"`
	Mode     string `yaml:"mode"`
}

// Load reads the YAML config file and resolves secrets from env vars / docker secrets.
func Load(path string) (*Config, error) {
	// filepath.Clean makes the variable path a "cleaned" value that gosec
	// recognizes as safe (G304), avoiding a //nolint directive.
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := defaults.Set(&cfg); err != nil {
		return nil, fmt.Errorf("set config defaults: %w", err)
	}
	cfg.resolveSecrets()
	return &cfg, nil
}

// resolveSecrets resolves ${ENV_VAR} references and docker-secret fallbacks.
// Secret fields name a file under the secrets directory (default
// /run/secrets, override with FF_SECRETS_DIR); the file contents are the
// secret value.
func (c *Config) resolveSecrets() {
	// Database DSN
	c.Database.DSN = expandEnv(c.Database.DSN)

	// LLM API key: env LLM_API_KEY, or docker secret <apiKeySecret>
	if c.LLM.APIKey == "" {
		c.LLM.APIKey = os.Getenv("LLM_API_KEY")
	}
	if c.LLM.APIKey == "" {
		key, err := readSecret(c.LLM.APIKeySecret)
		if err != nil {
			slog.Warn("LLM API key not found", "secret", c.LLM.APIKeySecret, "err", err)
		} else {
			c.LLM.APIKey = key
		}
	}
	c.LLM.APIKey = expandEnv(c.LLM.APIKey)

	// Corpus Git PAT: env FF_GITHUB_PAT, or docker secret <gitPatSecret>
	if c.Corpus.GitPAT == "" {
		c.Corpus.GitPAT = os.Getenv("FF_GITHUB_PAT")
	}
	if c.Corpus.GitPAT == "" {
		key, err := readSecret(c.Corpus.GitPATSecret)
		if err != nil {
			slog.Warn("Git PAT not found", "secret", c.Corpus.GitPATSecret, "err", err)
		} else {
			c.Corpus.GitPAT = key
		}
	}
	c.Corpus.GitPAT = expandEnv(c.Corpus.GitPAT)
}

// expandEnv replaces ${VAR} with the environment variable value.
func expandEnv(s string) string {
	return os.Expand(s, os.Getenv)
}

// secretsDir returns the directory where secret files are mounted.
// Defaults to /run/secrets (Docker convention); override with FF_SECRETS_DIR
// for other runtimes (e.g. a Kubernetes projected volume mount).
func secretsDir() string {
	if d := os.Getenv("FF_SECRETS_DIR"); d != "" {
		return d
	}
	return defaultSecretsDir
}

// readSecret reads a secret file from the secrets directory and returns its
// trimmed contents. The name is resolved relative to the secrets directory;
// traversal outside that directory is rejected.
func readSecret(name string) (string, error) {
	if name == "" {
		return "", errEmptySecretName
	}
	dir := secretsDir()
	path := filepath.Clean(filepath.Join(dir, name)) // #nosec G304 -- trusted secret id from config, confined below
	if path != dir && !strings.HasPrefix(path, dir+string(os.PathSeparator)) {
		return "", fmt.Errorf("%w: %s", errSecretEscape, name)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read secret %s: %w", name, err)
	}
	if len(data) == 0 {
		return "", errEmptySecret
	}
	return strings.TrimSpace(string(data)), nil
}

// ReadSecret is the exported version for other packages (FantasyPros API key, cookies).
func ReadSecret(name string) (string, error) {
	return readSecret(name)
}

// LLMTimeout returns the LLM timeout as a duration.
func (c *Config) LLMTimeout() time.Duration {
	return time.Duration(c.LLM.TimeoutSecs) * time.Second
}
