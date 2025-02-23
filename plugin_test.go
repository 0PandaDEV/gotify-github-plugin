package main

import (
	"testing"

	"github.com/gotify/plugin-api"
	"github.com/stretchr/testify/assert"
)

func TestAPICompatibility(t *testing.T) {
	assert.Implements(t, (*plugin.Plugin)(nil), new(MyPlugin))
	assert.Implements(t, (*plugin.Messenger)(nil), new(MyPlugin))
	assert.Implements(t, (*plugin.Configurer)(nil), new(MyPlugin))
	assert.Implements(t, (*plugin.Webhooker)(nil), new(MyPlugin))
}
