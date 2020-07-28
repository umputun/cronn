package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDeDup(t *testing.T) {
	d := NewDeDup(true)
	assert.True(t, d.Add("cmd1"), "passed, first time")
	assert.False(t, d.Add("cmd1"), "failed, dup")
	assert.True(t, d.Add("cmd2"), "passed, different cmd")
	d.Remove("cmd1")
	assert.True(t, d.Add("cmd1"), "passed, removed before")
	assert.False(t, d.Add("cmd2"), "failed, dup")
}

func TestDeDupDisabled(t *testing.T) {
	d := NewDeDup(false)
	assert.True(t, d.Add("cmd1"))
	assert.True(t, d.Add("cmd1"))
	d.Remove("cmd1")
	assert.True(t, d.Add("cmd1"), "passed, removed before")
	assert.True(t, d.Add("cmd1"))
	assert.True(t, d.Add("cmd1"))
}
