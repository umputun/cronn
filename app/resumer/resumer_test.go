package resumer

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResumer_OnStart(t *testing.T) {
	r := New("/tmp/resumer.test", true)
	defer os.RemoveAll("/tmp/resumer.test")

	s, err := r.OnStart("cmd 1 2 blah")
	require.NoError(t, err)
	t.Log(s)

	data, err := os.ReadFile(s) // nolint gosec
	require.NoError(t, err)
	assert.Equal(t, "cmd 1 2 blah", string(data))
}

func TestResumer_OnFinish(t *testing.T) {
	r := New("/tmp/resumer.test", true)
	defer os.RemoveAll("/tmp/resumer.test")

	s, err := r.OnStart("cmd 1 2 blah")
	require.NoError(t, err)
	err = r.OnFinish(s)

	require.NoError(t, err)
	_, err = os.ReadFile(s) // nolint gosec
	assert.Error(t, err)
}

func TestResumer_List(t *testing.T) {
	r := New("/tmp/resumer.test", true)
	defer os.RemoveAll("/tmp/resumer.test")

	_, e := r.OnStart("cmd1 1 2 blah")
	require.NoError(t, e)
	_, e = r.OnStart("cmd2 1 2 3 blah")
	require.NoError(t, e)
	_, e = r.OnStart("cmd3 blah")
	require.NoError(t, e)

	err := os.WriteFile("/tmp/resumer.test/old.cronn", []byte("something"), 0600) //nolint
	require.NoError(t, err)
	defer os.Remove("/tmp/resumer.test/old.cronn")

	res := r.List()
	assert.Len(t, res, 4)

	err = os.Chtimes("/tmp/resumer.test/old.cronn",
		time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	res = r.List()
	assert.Len(t, res, 3)

	r.enabled = false
	res = r.List()
	assert.Empty(t, res)
}

func TestResumer_ListEdgeCases(t *testing.T) {
	t.Run("list with non-existent location", func(t *testing.T) {
		r := New("/nonexistent/path/that/does/not/exist", true)
		res := r.List()
		assert.Empty(t, res)
	})

	t.Run("list with file instead of directory", func(t *testing.T) {
		// create a temporary file
		tmpFile, err := os.CreateTemp("", "resumer-test-*.tmp")
		require.NoError(t, err)
		defer os.Remove(tmpFile.Name())
		require.NoError(t, tmpFile.Close())

		r := New(tmpFile.Name(), true)
		res := r.List()
		assert.Empty(t, res)
	})

	t.Run("list with empty cronn files", func(t *testing.T) {
		tmpDir := t.TempDir()
		r := New(tmpDir, true)

		// create empty cronn file
		err := os.WriteFile(filepath.Join(tmpDir, "empty.cronn"), []byte(""), 0o600)
		require.NoError(t, err)

		// create cronn file with just whitespace
		err = os.WriteFile(filepath.Join(tmpDir, "whitespace.cronn"), []byte("   \n\t  "), 0o600)
		require.NoError(t, err)

		// create valid cronn file
		err = os.WriteFile(filepath.Join(tmpDir, "valid.cronn"), []byte("echo test"), 0o600)
		require.NoError(t, err)

		res := r.List()
		// check that we get all 3 files
		assert.Len(t, res, 3)
		// verify that the echo test command is in there
		commands := make([]string, len(res))
		for i, cmd := range res {
			commands[i] = cmd.Command
		}
		assert.Contains(t, commands, "echo test")
	})
}
