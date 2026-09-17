package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server     ServerConfig     `yaml:"server"`
	Auth       AuthConfig       `yaml:"auth"`
	Services   []ServiceConfig  `yaml:"services"`
	Discovery  DiscoveryConfig  `yaml:"discovery"`
	FileList   FileListConfig   `yaml:"fileList"`
	Download   DownloadConfig   `yaml:"download"`
	Agent      AgentConfig      `yaml:"agent"`
	Kubernetes KubernetesConfig `yaml:"kubernetes"`
}

type ServerConfig struct {
	Listen            string   `yaml:"listen"`
	ReadHeaderTimeout Duration `yaml:"readHeaderTimeout"`
	TrustedProxyCIDRs []string `yaml:"trustedProxyCIDRs"`
}

type AuthConfig struct {
	Username      string   `yaml:"username"`
	PasswordHash  string   `yaml:"passwordHash"`
	SessionKey    []byte   `yaml:"-"`
	SessionKeyRaw string   `yaml:"sessionKey"`
	SessionTTL    Duration `yaml:"sessionTTL"`
	CookieName    string   `yaml:"cookieName"`
	CookieSecure  bool     `yaml:"cookieSecure"`
	FailureLimit  int      `yaml:"failureLimit"`
	FailureWindow Duration `yaml:"failureWindow"`
}

type ServiceConfig struct {
	ID          string `yaml:"id" json:"id"`
	DisplayName string `yaml:"displayName" json:"displayName"`
	Directory   string `yaml:"directory" json:"-"`
}

type DiscoveryConfig struct {
	Mode                 string       `yaml:"mode"`
	AgentPort            int          `yaml:"agentPort"`
	StaleAfter           Duration     `yaml:"staleAfter"`
	WatchRefreshInterval Duration     `yaml:"watchRefreshInterval"`
	StaticNodes          []StaticNode `yaml:"staticNodes"`
}

type StaticNode struct {
	Name       string `yaml:"name"`
	AgentURL   string `yaml:"agentURL"`
	NodeReady  bool   `yaml:"nodeReady"`
	AgentReady bool   `yaml:"agentReady"`
}

type KubernetesConfig struct {
	Namespace      string `yaml:"namespace"`
	PodSelector    string `yaml:"podSelector"`
	KubeconfigPath string `yaml:"kubeconfigPath"`
}

type FileListConfig struct {
	MaxItems         int      `yaml:"maxItems"`
	MaxResponseBytes ByteSize `yaml:"maxResponseBytes"`
}

type DownloadConfig struct {
	BodyIdleTimeout Duration `yaml:"bodyIdleTimeout"`
	MaxDuration     Duration `yaml:"maxDuration"`
	BufferBytes     int      `yaml:"bufferBytes"`
}

type AgentConfig struct {
	ProbeFileName string   `yaml:"probeFileName"`
	ProbeScope    string   `yaml:"probeScope"`
	HTTPTimeout   Duration `yaml:"httpTimeout"`
}

type Duration struct {
	time.Duration
}

type ByteSize int64

var (
	serviceDirPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	nodeNamePattern   = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)
)

func Load(path string) (*Config, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := defaults()
	if err := yaml.Unmarshal(content, &cfg); err != nil {
		return nil, err
	}
	applyEnv(&cfg)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func defaults() Config {
	return Config{
		Server: ServerConfig{
			Listen:            ":8080",
			ReadHeaderTimeout: Duration{5 * time.Second},
		},
		Auth: AuthConfig{
			SessionTTL:    Duration{8 * time.Hour},
			CookieName:    "log_portal_session",
			CookieSecure:  true,
			FailureLimit:  5,
			FailureWindow: Duration{5 * time.Minute},
		},
		Discovery: DiscoveryConfig{
			Mode:                 "kubernetes",
			AgentPort:            8080,
			StaleAfter:           Duration{30 * time.Second},
			WatchRefreshInterval: Duration{20 * time.Second},
		},
		FileList: FileListConfig{
			MaxItems:         10000,
			MaxResponseBytes: ByteSize(8 * 1024 * 1024),
		},
		Download: DownloadConfig{
			BodyIdleTimeout: Duration{60 * time.Second},
			BufferBytes:     64 * 1024,
		},
		Agent: AgentConfig{
			ProbeFileName: ".log-portal-read-probe",
			ProbeScope:    "allServices",
			HTTPTimeout:   Duration{15 * time.Second},
		},
		Kubernetes: KubernetesConfig{
			Namespace:   "log-portal",
			PodSelector: "app.kubernetes.io/component=agent,app.kubernetes.io/name=log-download-portal",
		},
	}
}

func applyEnv(cfg *Config) {
	if value := os.Getenv("PORTAL_USERNAME"); value != "" {
		cfg.Auth.Username = value
	}
	if value := os.Getenv("PORTAL_PASSWORD_HASH"); value != "" {
		cfg.Auth.PasswordHash = value
	}
	if value := os.Getenv("PORTAL_SESSION_KEY"); value != "" {
		cfg.Auth.SessionKeyRaw = value
	}
	if value := os.Getenv("PORTAL_LISTEN"); value != "" {
		cfg.Server.Listen = value
	}
	if value := os.Getenv("KUBECONFIG"); value != "" && cfg.Kubernetes.KubeconfigPath == "" {
		cfg.Kubernetes.KubeconfigPath = value
	}
}

func (c *Config) Validate() error {
	if c.Server.Listen == "" {
		return errors.New("server.listen is required")
	}
	for _, cidr := range c.Server.TrustedProxyCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return fmt.Errorf("invalid server.trustedProxyCIDRs entry %q", cidr)
		}
	}
	if c.Auth.Username == "" {
		return errors.New("auth.username or PORTAL_USERNAME is required")
	}
	if c.Auth.PasswordHash == "" {
		return errors.New("auth.passwordHash or PORTAL_PASSWORD_HASH is required")
	}
	sessionKey, err := decodeSessionKey(c.Auth.SessionKeyRaw)
	if err != nil {
		return err
	}
	c.Auth.SessionKey = sessionKey
	if c.Auth.SessionTTL.Duration <= 0 {
		return errors.New("auth.sessionTTL must be positive")
	}
	if c.Auth.CookieName == "" {
		return errors.New("auth.cookieName is required")
	}
	if c.Auth.FailureLimit <= 0 {
		return errors.New("auth.failureLimit must be positive")
	}
	if c.Auth.FailureWindow.Duration <= 0 {
		return errors.New("auth.failureWindow must be positive")
	}
	if len(c.Services) == 0 {
		return errors.New("at least one service is required")
	}
	seenIDs := map[string]struct{}{}
	seenDirs := map[string]struct{}{}
	for _, svc := range c.Services {
		if !serviceDirPattern.MatchString(svc.ID) {
			return fmt.Errorf("service id %q must match %s", svc.ID, serviceDirPattern.String())
		}
		if !serviceDirPattern.MatchString(svc.Directory) {
			return fmt.Errorf("service directory %q must match %s", svc.Directory, serviceDirPattern.String())
		}
		if svc.DisplayName == "" {
			return fmt.Errorf("service %q displayName is required", svc.ID)
		}
		if _, ok := seenIDs[svc.ID]; ok {
			return fmt.Errorf("duplicate service id %q", svc.ID)
		}
		if _, ok := seenDirs[svc.Directory]; ok {
			return fmt.Errorf("duplicate service directory %q", svc.Directory)
		}
		seenIDs[svc.ID] = struct{}{}
		seenDirs[svc.Directory] = struct{}{}
	}
	if c.Discovery.Mode != "kubernetes" && c.Discovery.Mode != "static" {
		return errors.New("discovery.mode must be kubernetes or static")
	}
	if c.Discovery.AgentPort <= 0 || c.Discovery.AgentPort > 65535 {
		return errors.New("discovery.agentPort must be a valid TCP port")
	}
	if c.Discovery.StaleAfter.Duration <= 0 {
		return errors.New("discovery.staleAfter must be positive")
	}
	if c.Discovery.WatchRefreshInterval.Duration <= 0 {
		return errors.New("discovery.watchRefreshInterval must be positive")
	}
	if c.Discovery.Mode == "static" && len(c.Discovery.StaticNodes) == 0 {
		return errors.New("discovery.staticNodes is required in static mode")
	}
	if c.Discovery.Mode == "static" {
		for _, node := range c.Discovery.StaticNodes {
			if !validNodeName(node.Name) {
				return fmt.Errorf("static node name %q is invalid", node.Name)
			}
			parsed, err := url.Parse(node.AgentURL)
			if err != nil || parsed.Scheme != "http" || parsed.Host == "" {
				return fmt.Errorf("static node %q has invalid agentURL", node.Name)
			}
		}
	}
	if c.Discovery.Mode == "kubernetes" {
		if c.Kubernetes.Namespace == "" {
			return errors.New("kubernetes.namespace is required")
		}
		if c.Kubernetes.PodSelector == "" {
			return errors.New("kubernetes.podSelector is required")
		}
	}
	if c.FileList.MaxItems <= 0 {
		return errors.New("fileList.maxItems must be positive")
	}
	if c.FileList.MaxResponseBytes <= 0 {
		return errors.New("fileList.maxResponseBytes must be positive")
	}
	if c.Download.BodyIdleTimeout.Duration <= 0 {
		return errors.New("download.bodyIdleTimeout must be positive")
	}
	if c.Download.BufferBytes <= 0 {
		return errors.New("download.bufferBytes must be positive")
	}
	if c.Agent.ProbeFileName == "" || strings.ContainsAny(c.Agent.ProbeFileName, `/\%`) {
		return errors.New("agent.probeFileName must be a simple filename")
	}
	return nil
}

func validNodeName(value string) bool {
	if value == "" || len(value) > 253 || !nodeNamePattern.MatchString(value) {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) > 63 {
			return false
		}
	}
	return true
}

func (c *Config) ServiceByID(id string) (ServiceConfig, bool) {
	for _, svc := range c.Services {
		if svc.ID == id {
			return svc, true
		}
	}
	return ServiceConfig{}, false
}

func decodeSessionKey(value string) ([]byte, error) {
	if value == "" {
		return nil, errors.New("auth.sessionKey or PORTAL_SESSION_KEY is required")
	}
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil && len(decoded) >= 32 {
		return decoded, nil
	}
	if len(value) < 32 {
		return nil, errors.New("session key must be at least 32 bytes or base64-encoded 32 bytes")
	}
	return []byte(value), nil
}

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		if value.Value == "0" || value.Value == "" {
			d.Duration = 0
			return nil
		}
		parsed, err := time.ParseDuration(value.Value)
		if err != nil {
			return err
		}
		d.Duration = parsed
		return nil
	}
	return fmt.Errorf("duration must be a scalar")
}

func (s *ByteSize) UnmarshalYAML(value *yaml.Node) error {
	parsed, err := parseByteSize(value.Value)
	if err != nil {
		return err
	}
	*s = ByteSize(parsed)
	return nil
}

func parseByteSize(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, errors.New("byte size is required")
	}
	units := []struct {
		suffix string
		value  int64
	}{
		{"GiB", 1024 * 1024 * 1024},
		{"MiB", 1024 * 1024},
		{"KiB", 1024},
		{"GB", 1000 * 1000 * 1000},
		{"MB", 1000 * 1000},
		{"KB", 1000},
		{"B", 1},
	}
	for _, unit := range units {
		if strings.HasSuffix(value, unit.suffix) {
			number := strings.TrimSpace(strings.TrimSuffix(value, unit.suffix))
			n, err := strconv.ParseInt(number, 10, 64)
			if err != nil {
				return 0, err
			}
			return n * unit.value, nil
		}
	}
	return strconv.ParseInt(value, 10, 64)
}
