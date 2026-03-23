package config

import (
	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

type Config struct {
	State        StateConfig        `mapstructure:"state"`
	Provisioners ProvisionersConfig `mapstructure:"provisioners"`
	Deployer     DeployerConfig     `mapstructure:"deployers"`
}

type StateConfig struct {
	Backend string          `mapstructure:"backend"` // "file" | "s3"
	File    FileStateConfig `mapstructure:"file"`
	S3      S3StateConfig   `mapstructure:"s3"`
}

type FileStateConfig struct {
	BasePath string `mapstructure:"base_path"`
}

type S3StateConfig struct {
	Bucket string `mapstructure:"bucket"`
	Prefix string `mapstructure:"prefix"`
	Region string `mapstructure:"region"`
}

type ProvisionersConfig struct {
	Source string          `mapstructure:"source"` // "git" | "local"
	Git    GitProvConfig   `mapstructure:"git"`
	Local  LocalProvConfig `mapstructure:"local"`
}

type GitProvConfig struct {
	URL  string     `mapstructure:"url"`
	Ref  string     `mapstructure:"ref"`
	Path string     `mapstructure:"path"`
	Auth AuthConfig `mapstructure:"auth"`
}

type LocalProvConfig struct {
	Path string `mapstructure:"path"`
}

type AuthConfig struct {
	Type       string `mapstructure:"type"` // "ssh" | "https"
	SSHKeyFile string `mapstructure:"ssh_key_file"`
	TokenEnv   string `mapstructure:"token_env"`
}

type DeployerConfig struct {
	Source  string            `mapstructure:"source"`
	Kubectl KubectlConfig     `mapstructure:"kubectl"`
	Git     GitDeployerConfig `mapstructure:"git"`
	Webhook WebhookConfig     `mapstructure:"webhook"`
}

type KubectlConfig struct {
	Kubeconfig string `mapstructure:"kubeconfig"`
	Namespace  string `mapstructure:"namespace"`
	Context    string `mapstructure:"context"`
}

type GitDeployerConfig struct {
	URL  string     `mapstructure:"url"`
	Ref  string     `mapstructure:"ref"`
	Path string     `mapstructure:"path"`
	Auth AuthConfig `mapstructure:"auth"`
}

type WebhookConfig struct {
	URL            string            `mapstructure:"url"`
	Method         string            `mapstructure:"method"`
	TimeoutSeconds int               `mapstructure:"timeout_seconds"`
	Headers        map[string]string `mapstructure:"headers"`
	Body           string            `mapstructure:"body"`
}

// Load reads the config file at path and returns a populated Config.
func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}
	var cfg Config
	if err := v.Unmarshal(&cfg, func(dc *mapstructure.DecoderConfig) {
		dc.TagName = "mapstructure"
	}); err != nil {
		return nil, err
	}
	return &cfg, nil
}
