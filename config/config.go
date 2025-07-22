package config

import "time"

type Config struct {
	WatchDir      string        `mapstructure:"watchdir"`
	FilePatterns  []string      `mapstructure:"filePatterns"`
	MaxGoroutines int           `mapstructure:"maxGoroutines"`
	ProcessDelay  time.Duration `mapstructure:"processDelay"`
	ChromeUrl     string        `envconfig:"CHROME_URL" required:"true"`
}
