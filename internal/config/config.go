package config

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level daemon configuration loaded from YAML.
type Config struct {
	Server     ServerConfig     `yaml:"server"`
	Database   DatabaseConfig   `yaml:"database"`
	Embeddings EmbeddingsConfig `yaml:"embeddings"`
	LLM        LLMConfig        `yaml:"llm"`
	Sleeper    SleeperConfig    `yaml:"sleeper"`
	Sources    []SourceConfig   `yaml:"sources"`
	FantasyPros FantasyProsConfig `yaml:"fantasypros"`
	Corpus     CorpusConfig     `yaml:"corpus"`
	Telemetry  TelemetryConfig  `yaml:"telemetry"`
	Scheduler  SchedulerConfig  `yaml:"scheduler"`
}

type ServerConfig struct {
	MCPAddr    string `yaml:"mcp_addr"`
	MetricsAddr string `yaml:"metrics_addr"`
}

type DatabaseConfig struct {
	DSN string `yaml:"dsn"`
}

type EmbeddingsConfig struct {
	Provider   string `yaml:"provider"`
	URL        string `yaml:"url"`
	Model      string `yaml:"model"`
	Dimensions int    `yaml:"dimensions"`
	BatchSize  int    `yaml:"batch_size"`
}

type LLMConfig struct {
	Provider     string `yaml:"provider"`
	URL          string `yaml:"url"`
	Model        string `yaml:"model"`
	APIKey       string `yaml:"api_key"`
	APIKeyPass   string `yaml:"api_key_pass"`
	TimeoutSecs  int    `yaml:"timeout_secs"`
	MaxConcurrency int  `yaml:"max_concurrency"`
}

type SleeperConfig struct {
	BaseURL        string `yaml:"base_url"`
	RateLimitPerMin int   `yaml:"rate_limit_per_min"`
	MarkisUsername string `yaml:"markis_username"`
	MarkisUserID   string `yaml:"markis_user_id"`
	Seasons        []int  `yaml:"seasons"`
}

type SourceConfig struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
}

type FantasyProsConfig struct {
	APIKeyPass   string `yaml:"api_key_pass"`
	CookiePass   string `yaml:"cookie_pass"`
}

type CorpusConfig struct {
	RepoDir     string `yaml:"repo_dir"`
	GitURL      string `yaml:"git_url"`
	GitPAT      string `yaml:"git_pat"`
	GitPATPass  string `yaml:"git_pat_pass"`
	AuthorName  string `yaml:"author_name"`
	AuthorEmail string `yaml:"author_email"`
}

type TelemetryConfig struct {
	OTelEndpoint string `yaml:"otel_endpoint"`
	ServiceName  string `yaml:"service_name"`
}

type SchedulerConfig struct {
	Timezone string    `yaml:"timezone"`
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

// Load reads the YAML config file and resolves secrets from env vars / pass.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.setDefaults()
	if err := cfg.resolveSecrets(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) setDefaults() {
	if c.Server.MCPAddr == "" {
		c.Server.MCPAddr = ":3100"
	}
	if c.Embeddings.Provider == "" {
		c.Embeddings.Provider = "llama-server"
	}
	if c.Embeddings.URL == "" {
		c.Embeddings.URL = "http://llama-server:8080"
	}
	if c.Embeddings.Model == "" {
		c.Embeddings.Model = "nomic-embed-text-v1.5"
	}
	if c.Embeddings.Dimensions == 0 {
		c.Embeddings.Dimensions = 768
	}
	if c.Embeddings.BatchSize == 0 {
		c.Embeddings.BatchSize = 32
	}
	if c.LLM.Provider == "" {
		c.LLM.Provider = "ollama-cloud"
	}
	if c.LLM.URL == "" {
		c.LLM.URL = "https://ollama.com"
	}
	if c.LLM.Model == "" {
		c.LLM.Model = "minimax-m3"
	}
	if c.LLM.TimeoutSecs == 0 {
		c.LLM.TimeoutSecs = 300
	}
	if c.LLM.MaxConcurrency == 0 {
		c.LLM.MaxConcurrency = 4
	}
	if c.LLM.APIKeyPass == "" {
		c.LLM.APIKeyPass = "news/ollama-cloud"
	}
	if c.Sleeper.BaseURL == "" {
		c.Sleeper.BaseURL = "https://api.sleeper.app/v1"
	}
	if c.Sleeper.RateLimitPerMin == 0 {
		c.Sleeper.RateLimitPerMin = 1000
	}
	if c.Sleeper.MarkisUsername == "" {
		c.Sleeper.MarkisUsername = "markis"
	}
	if c.Sleeper.MarkisUserID == "" {
		c.Sleeper.MarkisUserID = "558115100726579200"
	}
	if len(c.Sleeper.Seasons) == 0 {
		c.Sleeper.Seasons = []int{2026}
	}
	if c.FantasyPros.APIKeyPass == "" {
		c.FantasyPros.APIKeyPass = "football/fantasypros-api"
	}
	if c.FantasyPros.CookiePass == "" {
		c.FantasyPros.CookiePass = "football/fantasypros-cookies"
	}
	if c.Corpus.GitURL == "" {
		c.Corpus.GitURL = "https://github.com/markis/fantasy-football-corpus"
	}
	if c.Corpus.GitPATPass == "" {
		c.Corpus.GitPATPass = "football/fantasy-github-pat"
	}
	if c.Corpus.AuthorName == "" {
		c.Corpus.AuthorName = "Markis Taylor"
	}
	if c.Corpus.AuthorEmail == "" {
		c.Corpus.AuthorEmail = "m@rkis.net"
	}
	if c.Telemetry.ServiceName == "" {
		c.Telemetry.ServiceName = "fantasy-football-engine"
	}
	if c.Scheduler.Timezone == "" {
		c.Scheduler.Timezone = "America/New_York"
	}
}

// resolveSecrets resolves ${ENV_VAR} references and pass-store fallbacks.
func (c *Config) resolveSecrets() error {
	// Database DSN
	c.Database.DSN = expandEnv(c.Database.DSN)

	// LLM API key: env OLLAMA_API_KEY, or pass show <api_key_pass>
	if c.LLM.APIKey == "" {
		c.LLM.APIKey = os.Getenv("OLLAMA_API_KEY")
	}
	if c.LLM.APIKey == "" {
		key, err := passShow(c.LLM.APIKeyPass)
		if err != nil {
			slog.Warn("LLM API key not found", "pass_path", c.LLM.APIKeyPass, "err", err)
		} else {
			c.LLM.APIKey = key
		}
	}
	c.LLM.APIKey = expandEnv(c.LLM.APIKey)

	// Corpus Git PAT: env FF_GITHUB_PAT, or pass show <git_pat_pass>
	if c.Corpus.GitPAT == "" {
		c.Corpus.GitPAT = os.Getenv("FF_GITHUB_PAT")
	}
	if c.Corpus.GitPAT == "" {
		key, err := passShow(c.Corpus.GitPATPass)
		if err != nil {
			slog.Warn("Git PAT not found", "pass_path", c.Corpus.GitPATPass, "err", err)
		} else {
			c.Corpus.GitPAT = key
		}
	}
	c.Corpus.GitPAT = expandEnv(c.Corpus.GitPAT)

	return nil
}

// expandEnv replaces ${VAR} with the environment variable value.
func expandEnv(s string) string {
	return os.Expand(s, func(key string) string {
		return os.Getenv(key)
	})
}

// passShow retrieves a secret from the pass password store.
func passShow(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty pass path")
	}
	cmd := exec.Command("pass", "show", path)
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("pass show %s: %w", path, err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 {
		return "", fmt.Errorf("pass show %s: empty output", path)
	}
	return strings.TrimSpace(lines[0]), nil
}

// PassShow is the exported version for other packages (FantasyPros API key, cookies).
func PassShow(path string) (string, error) {
	return passShow(path)
}

// LLMTimeout returns the LLM timeout as a duration.
func (c *Config) LLMTimeout() time.Duration {
	return time.Duration(c.LLM.TimeoutSecs) * time.Second
}