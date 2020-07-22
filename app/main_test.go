package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_makeHostName(t *testing.T) {
	opts.HostName = "test"
	assert.Equal(t, "test", makeHostName())

	opts.HostName = ""
	exp, err := os.Hostname()
	require.NoError(t, err)
	assert.Equal(t, exp, makeHostName())
}

func Test_makeNotifier(t *testing.T) {
	opts.Notify.EnabledCompletion, opts.Notify.EnabledError = false, false
	assert.Nil(t, makeNotifier())

	opts.Notify.EnabledCompletion = true
	notif := makeNotifier()
	require.NotNil(t, notif)
	assert.Equal(t, "cronn@"+makeHostName(), notif.From)
}
