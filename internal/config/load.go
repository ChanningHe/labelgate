package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
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
	durationType     = reflect.TypeFor[time.Duration]()
	unusedKeysRegexp = regexp.MustCompile(`'([^']*)' has invalid keys: ([^\n]+)`)
	fieldNameRegexp  = regexp.MustCompile(`'([^']+)'`)
	mapIndexRegexp   = regexp.MustCompile(`\[([^\]]+)\]`)
)

// Load reads configuration from file and environment, applies Defaults and
// returns a validated Config.
//
//   - File resolution: explicit path (CLI --config) > $LABELGATE_CONFIG >
//     ./labelgate.{yaml,yml,json,toml} > /etc/labelgate/labelgate.{...}.
//     A missing file in the default search paths is not an error; a missing
//     explicit file is.
//   - Every scalar/list key is overridable by LABELGATE_<PATH> (lists are
//     comma-separated, empty values are ignored). Map sections
//     (cloudflare.credentials, cloudflare.tunnels, agent.agents) are file-only.
//   - Any key in RemovedKeys present in file or env -> *ValidationError naming it.
//   - Unknown keys and undecodable values -> *ValidationError (strict decoding).
//   - Durations must be strings such as "30s"; bare numbers are rejected.
//   - Returns Validate(cfg) errors unchanged.
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
	if err := rejectRemoved(v, env); err != nil {
		return Config{}, err
	}
	cfg, err := decode(v, env)
	if err != nil {
		return Config{}, err
	}
	if err := Validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// decode overlays the environment on v and strictly decodes it over Defaults().
func decode(v *viper.Viper, env envLookup) (Config, error) {
	applyEnv(v, env)
	cfg := Defaults()
	if err := v.UnmarshalExact(&cfg, viper.DecodeHook(decodeHook)); err != nil {
		return Config{}, decodeError(err)
	}
	return cfg, nil
}

// resolveFile returns the config file to read, or "" when none applies.
func resolveFile(path string, env envLookup, dirs []string) (string, error) {
	if path == "" {
		path, _ = env(ConfigEnv)
	}
	if path != "" {
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("config: %w", err)
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
				return "", fmt.Errorf("config: %w", err)
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
				return fmt.Errorf("config: read %s: %w", file, err)
			}
			return nil
		}
	}
	return fmt.Errorf("config: %s: unsupported extension %q (use .yaml, .yml, .json or .toml)", file, ext)
}

// rejectRemoved fails when a removed v0.2 key (or its parent section) is
// present in the file or the environment.
func rejectRemoved(v *viper.Viper, env envLookup) error {
	keys := sortedKeys(RemovedKeys)
	for _, key := range keys {
		if _, inEnv := env(envName(key)); inEnv || v.IsSet(viperKey(key)) {
			return &ValidationError{Field: key, Message: RemovedKeys[key]}
		}
	}
	// A bare removed section (e.g. an empty docker.ssh block) is reported too.
	for _, key := range keys {
		if parent := key[:strings.LastIndex(key, ".")]; v.IsSet(viperKey(parent)) && !knownSection(parent) {
			return &ValidationError{Field: key, Message: RemovedKeys[key]}
		}
	}
	return nil
}

// knownSection reports whether section is still a valid config section, so a
// file merely containing it does not imply a removed key.
func knownSection(section string) bool {
	for _, k := range leafKeys(reflect.TypeFor[Config](), "") {
		if strings.HasPrefix(k, section+".") {
			return true
		}
	}
	return false
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

// decodeError converts a decoding failure into a *ValidationError.
func decodeError(err error) error {
	msg := err.Error()
	if m := unusedKeysRegexp.FindStringSubmatch(msg); m != nil {
		key, _, _ := strings.Cut(m[2], ",")
		return &ValidationError{Field: dotted(m[1] + "." + strings.TrimSpace(key)), Message: "unknown key"}
	}
	field := "config"
	if m := fieldNameRegexp.FindStringSubmatch(msg); m != nil {
		field = dotted(m[1])
	}
	return &ValidationError{Field: field, Message: strings.TrimSpace(strings.TrimPrefix(msg, "decoding failed due to the following error(s):"))}
}

// dotted rewrites mapstructure paths ("a.b[c].d", ".x") into dotted config keys.
func dotted(path string) string {
	return strings.Trim(mapIndexRegexp.ReplaceAllString(path, ".$1"), ".")
}
