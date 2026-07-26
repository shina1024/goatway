package config

import "fmt"

type TargetGroupID string

type ClientType string

type TargetAddress string

type Milliseconds int

type Weight int

type Config struct {
	TargetGroups          map[TargetGroupID]TargetGroupConfig
	Routes                []RouteConfig
	APIClientTokens       map[ClientType][]string
	IPRangeGroups         map[string][]string
	MaxConcurrentRequests map[ClientType]int
	Deployment            DeploymentConfig
	Gateway               GatewayConfig
	gatewayFilePresent    bool
}

type TargetGroupConfig struct {
	Targets              []TargetConfig `yaml:"targets"`
	Scheme               string         `yaml:"scheme"`
	MaxTryCount          int            `yaml:"max_try_count"`
	RetryCases           []string       `yaml:"retry_cases"`
	RetryNonIdempotent   bool           `yaml:"retry_non_idempotent"`
	RetryBaseInterval    Milliseconds   `yaml:"retry_base_interval"`
	RetryMaxInterval     Milliseconds   `yaml:"retry_max_interval"`
	RetryToTargetGroupID TargetGroupID  `yaml:"retry_to_target_group_id"`
	ConnectTimeout       Milliseconds   `yaml:"connect_timeout"`
	ReadTimeout          Milliseconds   `yaml:"read_timeout"`
	IdleConnTimeout      Milliseconds   `yaml:"idle_conn_timeout"`
	MaxIdleConnsPerHost  int            `yaml:"max_idle_conns_per_host"`
}

type TargetConfig struct {
	Host            string        `yaml:"host"`
	Scheme          string        `yaml:"scheme"`
	Port            int           `yaml:"port"`
	Weight          Weight        `yaml:"weight"`
	RetryTo         TargetAddress `yaml:"retry_to"`
	ConnectTimeout  Milliseconds  `yaml:"connect_timeout"`
	ReadTimeout     Milliseconds  `yaml:"read_timeout"`
	IdleConnTimeout Milliseconds  `yaml:"idle_conn_timeout"`
}

type RouteConfig struct {
	From RouteFromConfig `yaml:"from"`
	To   RouteToConfig   `yaml:"to"`
}

type RouteFromConfig struct {
	Path          string       `yaml:"path"`
	Clients       []ClientType `yaml:"clients"`
	IPRangeGroups []string     `yaml:"ip_range_groups"`
}

type RouteToConfig struct {
	Destinations []DestinationConfig `yaml:"destinations"`
}

type DestinationConfig struct {
	TargetGroup TargetGroupID `yaml:"target_group"`
	Path        string        `yaml:"path"`
	Weight      Weight        `yaml:"weight"`
}

type DeploymentConfig struct {
	PrimaryPods   int    `yaml:"primary_pods"`
	CanaryPods    int    `yaml:"canary_pods"`
	PrimaryWeight Weight `yaml:"primary_weight"`
	CanaryWeight  Weight `yaml:"canary_weight"`
}

type GatewayConfig struct {
	SchemaVersion int            `yaml:"schema_version"`
	Proxy         ProxyConfig    `yaml:"proxy"`
	Throttle      ThrottleConfig `yaml:"throttle"`
}

type ProxyConfig struct {
	MaxResponseBodySizeBytes int64 `yaml:"max_response_body_size_bytes"`
}

type ThrottleConfig struct {
	FailPolicy string `yaml:"fail_policy"`
}

type DecodeError struct {
	File string
	Err  error
}

func (err *DecodeError) Error() string {
	return fmt.Sprintf("decode configuration file %q: %v", err.File, err.Err)
}

func (err *DecodeError) Unwrap() error {
	return err.Err
}

type ValidationError struct {
	File  string
	Field string
	Rule  string
	Value string
}

func (err *ValidationError) Error() string {
	return fmt.Sprintf("invalid configuration %s.%s: %s (%s)", err.File, err.Field, err.Rule, err.Value)
}

func invalid(file, field, rule, value string) *ValidationError {
	return &ValidationError{File: file, Field: field, Rule: rule, Value: value}
}
