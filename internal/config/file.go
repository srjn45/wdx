package config

import (
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/settings"
)

// Rewrite returns wdx's config file at path written anew from its settings,
// as settings.Table.Rewrite does.
func Rewrite(path string, set map[string]string) (settings.Rewritten, error) {
	return table.Rewrite(path, set)
}

// Set sets the setting key in the config file of home, under its lock.
func Set(home, key, value string) (settings.Rewritten, error) {
	return table.Set(identity.ConfigPath(home), identity.ConfigLockPath(home), key, value)
}

// Lock keeps others from rewriting the config file of home, which must exist,
// until unlock is called.
func Lock(home string) (unlock func(), err error) {
	return settings.Lock(identity.ConfigLockPath(home))
}

// WriteFile writes content to the config file at path, whole or not at all.
func WriteFile(path string, content []byte) error {
	return settings.WriteFile(path, content)
}
