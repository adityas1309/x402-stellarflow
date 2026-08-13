package config

import (
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config stores all configuration of the application
type Config struct {
	Env                  string        `mapstructure:"ENV"`
	DBSource             string        `mapstructure:"DB_SOURCE"`
	ServerAddress        string        `mapstructure:"SERVER_ADDRESS"`
	TokenSymmetricKey    string        `mapstructure:"TOKEN_SYMMETRIC_KEY"`
	AccessTokenDuration  time.Duration `mapstructure:"ACCESS_TOKEN_DURATION"`
	RefreshTokenDuration time.Duration `mapstructure:"REFRESH_TOKEN_DURATION"`
	RedisAddr            string        `mapstructure:"REDIS_ADDR"`
	AllowedOrigins       string        `mapstructure:"ALLOWED_ORIGINS"`
	SentryDSN            string        `mapstructure:"SENTRY_DSN"`

	// SEP-10 wallet auth
	Sep10ServerSecretKey string        `mapstructure:"SEP10_SERVER_SECRET_KEY"`
	Sep10ServerAccountID string        `mapstructure:"SEP10_SERVER_ACCOUNT_ID"`
	Sep10HomeDomain      string        `mapstructure:"SEP10_HOME_DOMAIN"`
	Sep10WebAuthDomain   string        `mapstructure:"SEP10_WEB_AUTH_DOMAIN"`
	Sep10JWTDuration     time.Duration `mapstructure:"SEP10_JWT_DURATION"`

	// x402 payments
	X402FacilitatorURL    string `mapstructure:"X402_FACILITATOR_URL"`
	X402FacilitatorAPIKey string `mapstructure:"X402_FACILITATOR_API_KEY"`
	X402Network           string `mapstructure:"X402_NETWORK"`
	X402TreasuryAddress   string `mapstructure:"X402_TREASURY_ADDRESS"`
	X402TreasurySecret    string `mapstructure:"X402_TREASURY_SECRET"` // S-key, REQUIRED for /api/agent/sponsor
	X402USDCIssuer        string `mapstructure:"X402_USDC_ISSUER"`     // classic G-issuer (Circle), informational
	X402USDCContract      string `mapstructure:"X402_USDC_CONTRACT"`   // Soroban SAC C-address (used in PaymentRequirements.asset)
	X402USDCCode          string `mapstructure:"X402_USDC_CODE"`
	X402DryRun            bool   `mapstructure:"X402_DRY_RUN"`
}

// LoadConfig reads configuration from environment variables or .env file
func LoadConfig(path string) (config Config, err error) {
	viper.SetConfigName(".env")
	viper.SetConfigType("env")
	viper.AddConfigPath(path)
	viper.AutomaticEnv()

	bindEnvs := []string{
		"ENV", "DB_SOURCE", "SERVER_ADDRESS",
		"TOKEN_SYMMETRIC_KEY", "ACCESS_TOKEN_DURATION", "REFRESH_TOKEN_DURATION",
		"REDIS_ADDR", "ALLOWED_ORIGINS",
		"SENTRY_DSN",
		// SEP-10
		"SEP10_SERVER_SECRET_KEY", "SEP10_SERVER_ACCOUNT_ID", "SEP10_HOME_DOMAIN",
		"SEP10_WEB_AUTH_DOMAIN", "SEP10_JWT_DURATION",
		// x402
		"X402_FACILITATOR_URL", "X402_FACILITATOR_API_KEY", "X402_NETWORK",
		"X402_TREASURY_ADDRESS", "X402_TREASURY_SECRET",
		"X402_USDC_ISSUER", "X402_USDC_CONTRACT",
		"X402_USDC_CODE", "X402_DRY_RUN",
	}

	for _, key := range bindEnvs {
		_ = viper.BindEnv(key)
	}

	// Try to read config file (optional, fall back to env vars)
	_ = viper.ReadInConfig()

	err = viper.Unmarshal(&config)

	// Trim spaces from critical config values
	config.DBSource = strings.TrimSpace(config.DBSource)
	config.TokenSymmetricKey = strings.TrimSpace(config.TokenSymmetricKey)

	// Defaults
	if config.ServerAddress == "" {
		config.ServerAddress = "0.0.0.0:8080"
	}
	if config.RedisAddr == "" {
		config.RedisAddr = "localhost:6379"
	}
	if config.Sep10HomeDomain == "" {
		config.Sep10HomeDomain = "localhost"
	}
	if config.Sep10WebAuthDomain == "" {
		config.Sep10WebAuthDomain = config.Sep10HomeDomain
	}
	if config.Sep10JWTDuration == 0 {
		config.Sep10JWTDuration = 24 * time.Hour
	}
	if config.X402FacilitatorURL == "" {
		config.X402FacilitatorURL = "https://channels.openzeppelin.com/x402/testnet"
	}
	if config.X402Network == "" {
		config.X402Network = "stellar:testnet"
	}
	if config.X402USDCIssuer == "" {
		// Stellar testnet USDC asset issuer (informational only).
		config.X402USDCIssuer = "GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5"
	}
	if config.X402USDCContract == "" {
		// Default to the Stellar testnet SAC contract address.
		config.X402USDCContract = "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA"
	}
	if config.X402USDCCode == "" {
		config.X402USDCCode = "USDC"
	}

	return config, err
}
