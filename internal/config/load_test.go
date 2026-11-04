//go:build !development

package config

import (
	"reflect"
	"testing"
)

func TestLoadHasNoDevelopmentMode(t *testing.T) {
	setDevelopmentVariables(t)
	t.Setenv("VERIRELAY_MODE", "development")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, productionConfig) {
		t.Fatalf("config = %+v, want %+v", cfg, productionConfig)
	}
}
