package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/spf13/viper"
)

const (
	// ConfigEnv names the environment variable holding the config file path.
	ConfigEnv = EnvPrefix + "_CONFIG"
	// configBaseName is the base name searched in the default directories.
	configBaseName = "labelgate"
	// keyDelim separates viper key paths. It is not "." so map keys such as
	// dotted agent IDs (agent.agents."nas.lan") are never split into paths.
	keyDelim = "::"
)

// defaultSearchDirs are searched, in order, when no explicit file is given.
var defaultSearchDirs = []string{".", "/etc/labelgate"}

// configExts maps supported file extensions to viper config types, in search order.
var configExts = []struct{ ext, kind string }{
	{".yaml", "yaml"}, {".yml", "yaml"}, {".json", "json"}, {".toml", "toml"},
}

// envLookup resolves one environment variable (os.LookupEnv in production).
type envLookup func(string) (string, bool)

var (
	durationType = reflect.TypeFor[time.Duration]()
)

// Load reads configuration from file and environment, applies Defaults and
// returns a validated Config. On failure the error describes every problem,
// not just the first, in a stable order: removed keys, unknown keys, decode
// (type) errors, then Validate problems. Validate is skipped when a decode
// error means the Config cannot be trusted.
//
//   - File resolution: explicit path (CLI --config) > $LABELGATE_CONFIG >
//     ./labelgate.{yaml,yml,json,toml} > /etc/labelgate/labelgate.{...}.
//     A missing file in the default search paths is not an error; a missing
//     explicit file is.
//   - Every scalar/list key is overridable by LABELGATE_<PATH> (lists are
//     comma-separated, empty values are ignored). Map sections
//     (cloudflare.credentials, cloudflare.tunnels, agent.agents) are file-only.
//   - Every key in RemovedKeys present in file or env is reported (sorted).
//   - Unknown keys and undecodable values are reported too (strict decoding).
//   - One problem is returned as *ValidationError, several as ValidationErrors
//     (errors.As finds *ValidationError either way).
//   - Durations must be strings such as "30s"; bare numbers are rejected.
//   - Validate problems are reported unchanged.
//   - Touches no global state (no viper singleton, no os.Setenv).
func Load(path string) (Config, error) {
	return load(path, os.LookupEnv, defaultSearchDirs)
}

func load(path string, env envLookup, dirs []string) (Config, error) {
	v := newViper()
	file, err := resolveFile(path, env, dirs)
	if err != nil {
		return Config{}, err
	}
	if file != "" {
		if err := readFile(v, file); err != nil {
			return Config{}, err
		}
	}
	problems := removedProblems(v, env)
	cfg, unknown, failures := decode(v, env)
	problems = append(problems, unknown...)
	problems = append(problems, failures...)
	// Unknown keys leave the struct fully decoded; a type error does not, and
	// validating a half-decoded config would only add noise.
	if len(failures) == 0 {
		problems = append(problems, validate(cfg)...)
	}
	if err := problems.err(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// decode overlays the environment on v and strictly decodes it over Defaults().
// Unknown keys (other than removed ones, which removedProblems reports) and
// decode failures are returned separately because only the latter make the
// decoded Config untrustworthy.
//
// The Config comes from a lenient decode, because strict decoding drops map
// entries holding an unknown key, which would make validation report phantom
// missing references. A second, strict decode into a scratch Config only
// discovers the unknown keys.
func decode(v *viper.Viper, env envLookup) (cfg Config, unknown, failures ValidationErrors) {
	applyEnv(v, env)
	hook := viper.DecodeHook(decodeHook)
	cfg = Defaults()
	if err := v.Unmarshal(&cfg, hook); err != nil {
		_, failures = decodeProblems(err)
	}
	scratch := Defaults()
	if err := v.UnmarshalExact(&scratch, hook); err != nil {
		unknown, _ = decodeProblems(err)
	}
	return cfg, unknown, failures
}

// resolveFile returns the config file to read, or "" when none applies.
func resolveFile(path string, env envLookup, dirs []string) (string, error) {
	origin := "--config"
	if path == "" {
		path, _ = env(ConfigEnv)
		origin = ConfigEnv
	}
	if path != "" {
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("config: cannot open the config file set by %s: %w; fix the path or mount the file into the container", origin, err)
		}
		return path, nil
	}
	for _, dir := range dirs {
		for _, e := range configExts {
			candidate := filepath.Join(dir, configBaseName+e.ext)
			info, err := os.Stat(candidate)
			switch {
			case err == nil && info.Mode().IsRegular():
				return candidate, nil
			case err != nil && !errors.Is(err, fs.ErrNotExist):
				return "", fmt.Errorf("config: cannot check for a config file: %w; make it readable by the labelgate user or pass --config", err)
			}
		}
	}
	return "", nil
}

// readFile loads file into v, choosing the parser by extension.
func readFile(v *viper.Viper, file string) error {
	ext := strings.ToLower(filepath.Ext(file))
	for _, e := range configExts {
		if e.ext == ext {
			v.SetConfigFile(file)
			v.SetConfigType(e.kind)
			if err := v.ReadInConfig(); err != nil {
				return fmt.Errorf("config: cannot parse %s: %w; fix the syntax at the reported position", file, err)
			}
			return nil
		}
	}
	return fmt.Errorf("config: %s: unsupported extension %q (use .yaml, .yml, .json or .toml)", file, ext)
}

// applyEnv overlays LABELGATE_<PATH> variables for every scalar/list key.
func applyEnv(v *viper.Viper, env envLookup) {
	for _, key := range leafKeys(reflect.TypeFor[Config](), "") {
		if val, ok := env(envName(key)); ok && val != "" {
			v.Set(viperKey(key), val)
		}
	}
}

// newViper returns an isolated viper instance using keyDelim.
func newViper() *viper.Viper { return viper.NewWithOptions(viper.KeyDelimiter(keyDelim)) }

// viperKey converts a dotted config key into a viper key path.
func viperKey(key string) string { return strings.ReplaceAll(key, ".", keyDelim) }

// KeyWithEnv names a config key for messages: "api.token (env
// LABELGATE_API_TOKEN)" when an environment variable can set it, the bare key
// otherwise (map sections such as agent.agents.* are config-file only).
func KeyWithEnv(key string) string {
	if !envBound(key) {
		return key
	}
	return key + " (env " + envName(key) + ")"
}

// KeyAndEnv is KeyWithEnv for use inside parentheses: "api.token, env
// LABELGATE_API_TOKEN", so the message has no nested brackets.
func KeyAndEnv(key string) string {
	if !envBound(key) {
		return key
	}
	return key + ", env " + envName(key)
}

// envBound reports whether an environment variable can set key: every
// scalar/list key, and removed keys (Load reports them when set in the env).
func envBound(key string) bool {
	if _, removed := RemovedKeys[key]; removed {
		return true
	}
	return slices.Contains(leafKeys(reflect.TypeFor[Config](), ""), key)
}

// envName returns the environment variable bound to a config key.
func envName(key string) string {
	return EnvPrefix + "_" + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
}

// leafKeys lists the dotted paths of all scalar and list fields of t. Map
// fields are skipped (file-only sections).
func leafKeys(t reflect.Type, prefix string) []string {
	var keys []string
	for i := 0; i < t.NumField(); i++ {
		tag, _, _ := strings.Cut(t.Field(i).Tag.Get("mapstructure"), ",")
		if tag == "" || tag == "-" {
			continue
		}
		path := prefix + tag
		switch t.Field(i).Type.Kind() {
		case reflect.Struct:
			keys = append(keys, leafKeys(t.Field(i).Type, path+".")...)
		case reflect.Map:
		default:
			keys = append(keys, path)
		}
	}
	return keys
}

// decodeHook converts env/file strings to durations and string lists, and
// rejects bare numbers for durations (a bare 30 would silently mean 30ns).
func decodeHook(from, to reflect.Type, data any) (any, error) {
	switch {
	case to == durationType:
		return toDuration(from, data)
	case to.Kind() == reflect.Slice && to.Elem().Kind() == reflect.String && from.Kind() == reflect.String:
		return splitList(data.(string)), nil
	}
	return data, nil
}

func toDuration(from reflect.Type, data any) (any, error) {
	if from == durationType {
		return data, nil
	}
	s, ok := data.(string)
	if !ok {
		return nil, fmt.Errorf("duration must be a string such as \"30s\", got %v", data)
	}
	d, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("invalid duration %q (use e.g. 30s, 5m, 1h)", s)
	}
	return d, nil
}

// splitList splits a comma-separated list, trimming blanks and dropping empties.
func splitList(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
