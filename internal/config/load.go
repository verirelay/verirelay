//go:build !development

package config

// Load returns the production configuration. Only a build with the
// development tag has a development mode, so no environment variable changes
// the configuration of this build.
func Load() (Config, error) {
	return production(), nil
}
