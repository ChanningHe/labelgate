package config

import (
	"errors"
	"strings"

	"github.com/spf13/viper"
)

// Load reads configuration from file and environment, applies Defaults and
// returns a validated Config.
//
// Required behavior (WP1):
//   - File resolution: explicit path (CLI --config) > $LABELGATE_CONFIG >
//     ./labelgate.{yaml,yml,json,toml} > /etc/labelgate/labelgate.{...}.
//     A missing file in the default search paths is not an error; a missing
//     explicit file is.
//   - Every scalar/list key is overridable by LABELGATE_<PATH> (lists are
//     comma-separated). LABELGATE_CLOUDFLARE_API_TOKEN/ACCOUNT_ID/TUNNEL_ID must
//     work although cloudflare.* also contains maps.
//   - Any key in RemovedKeys present in file or env -> *ValidationError naming it.
//   - Unknown keys -> *ValidationError (strict decoding), so typos fail fast.
//   - Returns Validate(cfg) errors unchanged.
//   - Must not touch global state (no viper singleton, no os.Setenv).
func Load(path string) (Config, error) {
	return Config{}, errors.New("config.Load: not implemented")
}

// newViper returns an isolated viper instance bound to LABELGATE_* variables.
func newViper() *viper.Viper {
	v := viper.New()
	v.SetEnvPrefix(EnvPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	return v
}
